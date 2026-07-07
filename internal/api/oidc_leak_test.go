package api

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/store"
)

// TestOIDCClientSecretNeverLeaks configures a real OIDC provider whose
// client_secret is a unique canary, drives the config-write + read + full
// login flow (so the secret is actually unwrapped and used in the token
// exchange), and asserts the canary appears in NONE of: captured logs, any
// HTTP response body observed along the way, or any audit_events row.
func TestOIDCClientSecretNeverLeaks(t *testing.T) {
	const canary = "CANARY-oidc-client-secret-4f2a9e"

	dsn := bootPostgres(t)
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	ctx := context.Background()
	srv, st, err := Boot(ctx, BootConfig{
		DatabaseURL: dsn, SealType: crypto.SealTypeShamir, Logger: logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	var bodies []string
	capture := func(b string) { bodies = append(bodies, b) }

	// init + unseal (1-of-1).
	var ir struct {
		Shares []string `json:"shares"`
		Admin  *struct{ Email, Password string } `json:"admin"`
	}
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/init",
		`{"shares":1,"threshold":1,"admin_email":"root@corp.io"}`, &ir); code != 200 {
		t.Fatalf("init: %d", code)
	}
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/unseal",
		fmt.Sprintf(`{"share":%q}`, ir.Shares[0]), nil); code != 200 {
		t.Fatalf("unseal failed")
	}
	owner := login(t, ts.URL, ir.Admin.Email, ir.Admin.Password)

	// Pre-provision the user the mock IdP's claims will match, so the login
	// flow completes and the secret is actually unwrapped and exchanged.
	if _, _, err := srv.auth.CreateUser(ctx, "seed@example.com"); err != nil {
		t.Fatal(err)
	}

	idp := newMockIdP(t, "test-client")

	// Configure the provider with client_secret = canary via the real
	// admin endpoint (exercises the config-write + audit path).
	putBody := fmt.Sprintf(
		`{"name":"default","issuer":%q,"client_id":"test-client","client_secret":%q,"scopes":["openid","email"],"redirect_url":"https://app/cb","enabled":true}`,
		idp.srv.URL, canary)
	putResp := doRawBody(t, "PUT", ts.URL+"/v1/sys/oidc", owner, putBody)
	if putResp.status != 200 {
		t.Fatalf("PUT /v1/sys/oidc: %d %s", putResp.status, putResp.body)
	}
	capture(putResp.body)

	// GET must show secret_set:true and never the canary.
	getResp := doRawBody(t, "GET", ts.URL+"/v1/sys/oidc", owner, "")
	if getResp.status != 200 {
		t.Fatalf("GET /v1/sys/oidc: %d", getResp.status)
	}
	if !strings.Contains(getResp.body, `"secret_set":true`) {
		t.Fatalf("GET missing secret_set:true: %s", getResp.body)
	}
	capture(getResp.body)

	// Drive the full login flow: login redirect -> mock IdP -> callback.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Get(ts.URL + "/v1/auth/oidc/login")
	if err != nil {
		t.Fatal(err)
	}
	loginBody, _ := io.ReadAll(res.Body)
	res.Body.Close()
	capture(string(loginBody))
	capture(res.Header.Get("Location"))
	if res.StatusCode != http.StatusFound {
		t.Fatalf("login: want 302, got %d", res.StatusCode)
	}
	loc := res.Header.Get("Location")
	if !strings.HasPrefix(loc, idp.srv.URL+"/authorize") {
		t.Fatalf("login location: %s", loc)
	}
	state := queryParam(t, loc, "state")
	nonce := queryParam(t, loc, "nonce")

	idp.sub, idp.email, idp.emailVer, idp.nonce = "sub-1", "seed@example.com", true, nonce
	res, err = client.Get(ts.URL + "/v1/auth/oidc/callback?state=" + state + "&code=xyz")
	if err != nil {
		t.Fatal(err)
	}
	cbBody, _ := io.ReadAll(res.Body)
	res.Body.Close()
	capture(string(cbBody))
	if res.StatusCode != http.StatusFound {
		t.Fatalf("callback: want 302, got %d", res.StatusCode)
	}
	var hasSession bool
	for _, c := range res.Cookies() {
		if c.Name == sessionCookieName && c.Value != "" {
			hasSession = true
		}
	}
	if !hasSession {
		t.Fatal("callback did not set janus_session (login flow did not complete)")
	}

	// --- Assertions: the canary must appear nowhere. ---

	logs := logBuf.String()
	if strings.Contains(logs, canary) {
		t.Fatalf("client secret leaked into logs: %s", logs)
	}
	if !strings.Contains(logs, "/v1/auth/login") && !strings.Contains(logs, "/v1/auth/oidc") {
		t.Fatalf("expected request logs proving the logger was wired, got: %q", logs)
	}

	for i, b := range bodies {
		if strings.Contains(b, canary) {
			t.Fatalf("client secret leaked into response/body/header #%d: %s", i, b)
		}
	}

	rec := store.NewAuditRepo(st)
	if err := rec.Iterate(ctx, func(row store.AuditRow) error {
		detail := ""
		if row.Detail != nil {
			detail = *row.Detail
		}
		resultCode := ""
		if row.ResultCode != nil {
			resultCode = *row.ResultCode
		}
		if strings.Contains(row.Action+row.Resource+detail+row.Result+resultCode, canary) {
			t.Fatalf("client secret leaked into audit row: %+v", row)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// doRawBody issues an authenticated request with an optional body and
// returns the raw status + body (unlike doAuthed, which only JSON-decodes).
func doRawBody(t *testing.T, method, url, cookie, body string) struct {
	status int
	body   string
} {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return struct {
		status int
		body   string
	}{resp.StatusCode, string(b)}
}
