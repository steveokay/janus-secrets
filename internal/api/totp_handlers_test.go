package api

import (
	"context"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/store"
)

var apiB32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// totpCodeFromSecret computes a live code from a base32 TOTP secret.
func totpCodeFromSecret(t *testing.T, secretB32 string) string {
	t.Helper()
	raw, err := apiB32.DecodeString(secretB32)
	if err != nil {
		t.Fatalf("decode secret: %v", err)
	}
	return crypto.TOTPCodeAt(raw, time.Now())
}

// enrollAndConfirmHTTP enrolls + confirms TOTP for the authed session and
// returns (base32 secret, recovery codes).
func enrollAndConfirmHTTP(t *testing.T, base, cookie string) (string, []string) {
	t.Helper()
	var enroll struct {
		Secret     string `json:"secret"`
		OtpauthURL string `json:"otpauth_url"`
	}
	if code := doAuthed(t, "POST", base+"/v1/auth/totp/enroll", cookie, "", "", &enroll); code != 200 {
		t.Fatalf("enroll: %d", code)
	}
	if enroll.Secret == "" || !strings.HasPrefix(enroll.OtpauthURL, "otpauth://") {
		t.Fatalf("bad enroll response: %+v", enroll)
	}
	var confirm struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	body := fmt.Sprintf(`{"code":%q}`, totpCodeFromSecret(t, enroll.Secret))
	if code := doAuthed(t, "POST", base+"/v1/auth/totp/confirm", cookie, "", body, &confirm); code != 200 {
		t.Fatalf("confirm: %d", code)
	}
	if len(confirm.RecoveryCodes) == 0 {
		t.Fatal("no recovery codes returned")
	}
	return enroll.Secret, confirm.RecoveryCodes
}

// Note on rate limiting: /v1/auth/login and the TOTP mutation endpoints share a
// per-IP login limiter (10/min sustained, burst 5). Each test uses a fresh
// server (fresh limiter) and keeps its count of rate-limited calls at or under
// the burst; the unlimited GET /v1/auth/totp is used for state assertions.

func TestTOTPHandlersE2E(t *testing.T) {
	ts, email, password, _ := authStack(t)
	cookie := login(t, ts.URL, email, password) // limited #1

	// Status starts disabled (GET status is not rate-limited).
	var status struct {
		Enabled           bool `json:"enabled"`
		RecoveryRemaining int  `json:"recovery_remaining"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/auth/totp", cookie, "", "", &status); code != 200 || status.Enabled {
		t.Fatalf("initial status: %d %+v", code, status)
	}

	secret, recovery := enrollAndConfirmHTTP(t, ts.URL, cookie) // limited #2,#3

	// Status now enabled with a full recovery set.
	if code := doAuthed(t, "GET", ts.URL+"/v1/auth/totp", cookie, "", "", &status); code != 200 || !status.Enabled || status.RecoveryRemaining != len(recovery) {
		t.Fatalf("post-confirm status: %d %+v", code, status)
	}

	// Disable with a valid code succeeds (limited #4).
	disableBody := fmt.Sprintf(`{"code":%q}`, totpCodeFromSecret(t, secret))
	if code := doAuthed(t, "POST", ts.URL+"/v1/auth/totp/disable", cookie, "", disableBody, nil); code != 204 {
		t.Fatalf("disable valid code: %d", code)
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/auth/totp", cookie, "", "", &status); code != 200 || status.Enabled {
		t.Fatalf("status after disable: %d %+v", code, status)
	}
}

func TestTOTPRecoveryCodesRegenerateE2E(t *testing.T) {
	ts, email, password, _ := authStack(t)
	cookie := login(t, ts.URL, email, password) // limited #1
	secret, recovery := enrollAndConfirmHTTP(t, ts.URL, cookie) // limited #2,#3

	var regen struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	body := fmt.Sprintf(`{"code":%q}`, totpCodeFromSecret(t, secret))
	if code := doAuthed(t, "POST", ts.URL+"/v1/auth/totp/recovery-codes", cookie, "", body, &regen); code != 200 { // limited #4
		t.Fatalf("recovery-codes: %d", code)
	}
	if len(regen.RecoveryCodes) != len(recovery) {
		t.Fatalf("regen set size: %d, want %d", len(regen.RecoveryCodes), len(recovery))
	}
}

func TestTOTPDisableRejectsBadCodeE2E(t *testing.T) {
	ts, email, password, _ := authStack(t)
	cookie := login(t, ts.URL, email, password) // limited #1
	enrollAndConfirmHTTP(t, ts.URL, cookie)      // limited #2,#3

	var env errEnvelope
	if code := doAuthed(t, "POST", ts.URL+"/v1/auth/totp/disable", cookie, "", `{"code":"000000"}`, &env); code != 401 || env.Error.Code != "invalid_credentials" { // limited #4
		t.Fatalf("disable bad code: %d %+v", code, env)
	}
	// Factor is still enabled after a rejected disable.
	var status struct {
		Enabled bool `json:"enabled"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/auth/totp", cookie, "", "", &status); code != 200 || !status.Enabled {
		t.Fatalf("factor wrongly disabled: %d %+v", code, status)
	}
}

func TestTOTPRejectsServiceToken(t *testing.T) {
	ts, _, email, password, configID := authStackFull(t)
	cookie := login(t, ts.URL, email, password)

	// Mint a service token.
	var minted struct {
		Token string `json:"token"`
	}
	mintBody := fmt.Sprintf(`{"name":"ci","scope":{"kind":"config","id":%q},"access":"read"}`, configID)
	if code := doAuthed(t, "POST", ts.URL+"/v1/tokens", cookie, "", mintBody, &minted); code != 200 {
		t.Fatalf("mint: %d", code)
	}

	// A service-token principal is not a user → 401 on all TOTP surfaces.
	for _, ep := range []struct{ method, path, body string }{
		{"GET", "/v1/auth/totp", ""},
		{"POST", "/v1/auth/totp/enroll", ""},
		{"POST", "/v1/auth/totp/confirm", `{"code":"123456"}`},
		{"POST", "/v1/auth/totp/disable", `{"code":"123456"}`},
		{"POST", "/v1/auth/totp/recovery-codes", `{"code":"123456"}`},
	} {
		var env errEnvelope
		code := doAuthed(t, ep.method, ts.URL+ep.path, "", minted.Token, ep.body, &env)
		if code != 401 || env.Error.Code != "unauthenticated" {
			t.Fatalf("%s %s with token: %d %+v", ep.method, ep.path, code, env)
		}
	}
}

func TestLoginTOTPGateE2E(t *testing.T) {
	ts, email, password, _ := authStack(t)
	cookie := login(t, ts.URL, email, password)               // limited #1
	secret, _ := enrollAndConfirmHTTP(t, ts.URL, cookie)       // limited #2,#3

	// Login with no code → 401 totp_required.
	var env errEnvelope
	resp, err := http.Post(ts.URL+"/v1/auth/login", "application/json", // limited #4
		strings.NewReader(fmt.Sprintf(`{"email":%q,"password":%q}`, email, password)))
	if err != nil {
		t.Fatal(err)
	}
	code := resp.StatusCode
	decodeResp(t, resp, &env)
	if code != 401 || env.Error.Code != "totp_required" {
		t.Fatalf("no-code login: %d %+v", code, env)
	}

	// Login with a valid code → 200 + session cookie (limited #5).
	sessCookie := loginWithTOTP(t, ts.URL, email, password, totpCodeFromSecret(t, secret))
	if sessCookie == "" {
		t.Fatal("no session cookie after TOTP login")
	}
}

func TestLoginTOTPRecoveryAndBadCodeE2E(t *testing.T) {
	ts, email, password, _ := authStack(t)
	cookie := login(t, ts.URL, email, password)          // limited #1
	_, recovery := enrollAndConfirmHTTP(t, ts.URL, cookie) // limited #2,#3

	// Login with a recovery code works (single-use, limited #4).
	if c := loginWithTOTP(t, ts.URL, email, password, recovery[0]); c == "" {
		t.Fatal("recovery-code login produced no session cookie")
	}

	// Login with a wrong code → 401 invalid_credentials (limited #5).
	resp, err := http.Post(ts.URL+"/v1/auth/login", "application/json",
		strings.NewReader(fmt.Sprintf(`{"email":%q,"password":%q,"totp_code":"000000"}`, email, password)))
	if err != nil {
		t.Fatal(err)
	}
	var env errEnvelope
	code := resp.StatusCode
	decodeResp(t, resp, &env)
	if code != 401 || env.Error.Code != "invalid_credentials" {
		t.Fatalf("bad-code login: %d %+v", code, env)
	}
}

// loginWithTOTP posts email+password+totp_code and returns the session cookie.
func loginWithTOTP(t *testing.T, base, email, password, code string) string {
	t.Helper()
	resp, err := http.Post(base+"/v1/auth/login", "application/json",
		strings.NewReader(fmt.Sprintf(`{"email":%q,"password":%q,"totp_code":%q}`, email, password, code)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("TOTP login: %d", resp.StatusCode)
	}
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookieName {
			return c.Value
		}
	}
	return ""
}

func decodeResp(t *testing.T, resp *http.Response, out any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

// TestTOTPAuditAndLeak asserts enroll/confirm/disable emit the expected audit
// actions and that no TOTP secret, otpauth URL, or recovery code ever appears in
// any audit row.
func TestTOTPAuditAndLeak(t *testing.T) {
	ts, srv, email, password, _ := authStackFull(t)
	cookie := login(t, ts.URL, email, password)
	secret, recovery := enrollAndConfirmHTTP(t, ts.URL, cookie)

	// Disable to exercise the third audited action.
	disableBody := fmt.Sprintf(`{"code":%q}`, totpCodeFromSecret(t, secret))
	if code := doAuthed(t, "POST", ts.URL+"/v1/auth/totp/disable", cookie, "", disableBody, nil); code != 204 {
		t.Fatalf("disable: %d", code)
	}

	repo := store.NewAuditRepo(srv.st)
	seen := map[string]bool{}
	// Assemble the set of sensitive strings that must NOT appear anywhere.
	raw, _ := apiB32.DecodeString(secret)
	otpauth := "otpauth://" // any provisioning URI fragment is a leak marker
	needles := append([]string{secret, otpauth}, recovery...)

	if err := repo.Iterate(context.Background(), func(a store.AuditRow) error {
		if a.Result == "success" {
			seen[a.Action] = true
		}
		// Concatenate every string-bearing field of the row.
		fields := []string{a.Action, a.Resource, a.Result, a.ActorName}
		if a.ActorID != nil {
			fields = append(fields, *a.ActorID)
		}
		if a.Detail != nil {
			fields = append(fields, *a.Detail)
		}
		if a.ResultCode != nil {
			fields = append(fields, *a.ResultCode)
		}
		hay := strings.Join(fields, "\x00")
		for _, n := range needles {
			if n != "" && strings.Contains(hay, n) {
				t.Fatalf("sensitive material %q leaked into audit row %+v", n, a)
			}
		}
		// The base32 secret in its raw code form must also be absent.
		if len(raw) > 0 && strings.Contains(hay, string(raw)) {
			t.Fatalf("raw TOTP secret leaked into audit row %+v", a)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	for _, action := range []string{"totp.enroll", "totp.confirm", "totp.disable"} {
		if !seen[action] {
			t.Fatalf("expected audit action %q not recorded", action)
		}
	}
}
