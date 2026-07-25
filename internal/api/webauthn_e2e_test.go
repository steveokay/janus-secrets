package api

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"github.com/steveokay/janus-secrets/internal/auth"
	"github.com/steveokay/janus-secrets/internal/crypto"
)

// The passkey e2e tests drive the real HTTP surface with a minimal software
// authenticator (below). What they CANNOT cover is the browser half:
// navigator.credentials.create()/.get(), the user agent's own RP-ID and origin
// enforcement, and the platform consent / user-verification gesture. Those need
// a real browser.

const (
	waRPID   = "localhost"
	waOrigin = "http://localhost"
)

// ── minimal software authenticator (test-only) ─────────────────────────────

type testAuthenticator struct {
	key       *ecdsa.PrivateKey
	credID    []byte
	signCount uint32
}

func newTestAuthenticator(t *testing.T) *testAuthenticator {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 32)
	if _, err := rand.Read(id); err != nil {
		t.Fatal(err)
	}
	return &testAuthenticator{key: key, credID: id, signCount: 1}
}

func waB64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func (a *testAuthenticator) authData(t *testing.T, flags byte, count uint32, attested bool) []byte {
	t.Helper()
	h := sha256.Sum256([]byte(waRPID))
	out := append([]byte{}, h[:]...)
	if attested {
		flags |= 0x40
	}
	out = append(out, flags)
	var c [4]byte
	binary.BigEndian.PutUint32(c[:], count)
	out = append(out, c[:]...)
	if attested {
		out = append(out, make([]byte, 16)...) // zero AAGUID
		var l [2]byte
		binary.BigEndian.PutUint16(l[:], uint16(len(a.credID))) //nolint:gosec // fixed 32-byte test id
		out = append(out, l[:]...)
		out = append(out, a.credID...)
		x := make([]byte, 32)
		y := make([]byte, 32)
		a.key.PublicKey.X.FillBytes(x)
		a.key.PublicKey.Y.FillBytes(y)
		cose, err := webauthncbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: x, -3: y})
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, cose...)
	}
	return out
}

func waClientData(t *testing.T, ceremony, challenge, origin string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"type": ceremony, "challenge": challenge, "origin": origin, "crossOrigin": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (a *testAuthenticator) attestation(t *testing.T, challenge, origin string) string {
	t.Helper()
	cd := waClientData(t, "webauthn.create", challenge, origin)
	ad := a.authData(t, 0x01|0x04, a.signCount, true)
	att, err := webauthncbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": ad})
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{
		"id": waB64(a.credID), "rawId": waB64(a.credID), "type": "public-key",
		"response": map[string]any{"clientDataJSON": waB64(cd), "attestationObject": waB64(att)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func (a *testAuthenticator) assertion(t *testing.T, challenge, origin string) string {
	t.Helper()
	cd := waClientData(t, "webauthn.get", challenge, origin)
	a.signCount++
	ad := a.authData(t, 0x01|0x04, a.signCount, false)
	sum := sha256.Sum256(cd)
	digest := sha256.Sum256(append(append([]byte{}, ad...), sum[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, a.key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{
		"id": waB64(a.credID), "rawId": waB64(a.credID), "type": "public-key",
		"response": map[string]any{
			"clientDataJSON": waB64(cd), "authenticatorData": waB64(ad), "signature": waB64(sig),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// ── harness ────────────────────────────────────────────────────────────────

// webauthnStack boots the real stack with passkeys configured (or not, when
// enabled is false) and returns the test server, admin email and password.
func webauthnStack(t *testing.T, enabled bool) (*httptest.Server, string, string) {
	t.Helper()
	dsn := bootPostgres(t)
	bc := BootConfig{DatabaseURL: dsn, SealType: crypto.SealTypeShamir}
	if enabled {
		bc.WebAuthn = auth.WebAuthnConfig{RPID: waRPID, RPDisplayName: "Janus Test", Origins: []string{waOrigin}}
	}
	srv, st, err := Boot(context.Background(), bc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

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
		t.Fatal("unseal failed")
	}
	return ts, ir.Admin.Email, ir.Admin.Password
}

type waCredential struct {
	ID           string  `json:"id"`
	Nickname     string  `json:"nickname"`
	CredentialID string  `json:"credential_id"`
	LastUsedAt   *string `json:"last_used_at"`
}

type waList struct {
	Enabled     bool           `json:"enabled"`
	RPID        string         `json:"rp_id"`
	Credentials []waCredential `json:"credentials"`
}

// registerPasskey drives register/begin → register/finish over HTTP.
func registerPasskey(t *testing.T, ts *httptest.Server, cookie, nickname string) (*testAuthenticator, waCredential) {
	t.Helper()
	var opts map[string]any
	if code := doAuthed(t, "POST", ts.URL+"/v1/auth/webauthn/register/begin", cookie, "", "", &opts); code != 200 {
		t.Fatalf("register/begin: %d", code)
	}
	challenge, _ := opts["challenge"].(string)
	if challenge == "" {
		t.Fatalf("no challenge in creation options: %v", opts)
	}
	a := newTestAuthenticator(t)

	req, err := http.NewRequest("POST", ts.URL+"/v1/auth/webauthn/register/finish",
		strings.NewReader(a.attestation(t, challenge, waOrigin)))
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	req.Header.Set(webauthnNicknameHeader, nickname)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("register/finish: %d", resp.StatusCode)
	}
	var cred waCredential
	if err := json.NewDecoder(resp.Body).Decode(&cred); err != nil {
		t.Fatal(err)
	}
	return a, cred
}

// ── tests ──────────────────────────────────────────────────────────────────

// The full passkey lifecycle over HTTP: enroll → sign in with the passkey →
// replay-rejection → audit → rename → delete. Kept as one test because each
// e2e test boots its own Postgres container.
func TestWebAuthnE2ELifecycle(t *testing.T) {
	ts, email, password := webauthnStack(t, true)
	cookie := login(t, ts.URL, email, password)

	// Pre-auth probe advertises the feature and the RP ID, nothing else.
	var status struct {
		Enabled bool   `json:"enabled"`
		RPID    string `json:"rp_id"`
	}
	if code := doJSON(t, "GET", ts.URL+"/v1/auth/webauthn/status", "", &status); code != 200 {
		t.Fatalf("status: %d", code)
	}
	if !status.Enabled || status.RPID != waRPID {
		t.Fatalf("status = %+v", status)
	}

	// The management surface requires a user session.
	if code := doAuthed(t, "GET", ts.URL+"/v1/auth/webauthn", "", "", "", nil); code != 401 {
		t.Fatalf("unauthenticated list should be 401, got %d", code)
	}

	var list waList
	if code := doAuthed(t, "GET", ts.URL+"/v1/auth/webauthn", cookie, "", "", &list); code != 200 {
		t.Fatalf("list: %d", code)
	}
	if !list.Enabled || len(list.Credentials) != 0 {
		t.Fatalf("expected an empty enabled list, got %+v", list)
	}

	a, cred := registerPasskey(t, ts, cookie, "Work laptop")
	if cred.Nickname != "Work laptop" || cred.CredentialID == "" {
		t.Fatalf("registered credential = %+v", cred)
	}

	// Log in with the passkey from a clean client (no existing cookie).
	var loginOpts map[string]any
	if code := doJSON(t, "POST", ts.URL+"/v1/auth/webauthn/login/begin",
		fmt.Sprintf(`{"email":%q}`, email), &loginOpts); code != 200 {
		t.Fatalf("login/begin: %d", code)
	}
	challenge, _ := loginOpts["challenge"].(string)
	if challenge == "" {
		t.Fatalf("no challenge in request options: %v", loginOpts)
	}
	assertionBody := a.assertion(t, challenge, waOrigin)
	resp, err := http.Post(ts.URL+"/v1/auth/webauthn/login/finish", "application/json",
		strings.NewReader(assertionBody))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("login/finish: %d", resp.StatusCode)
	}
	var passkeyCookie string
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookieName {
			passkeyCookie = c.Value
			if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
				t.Fatalf("passkey session cookie is not hardened: %+v", c)
			}
		}
	}
	if passkeyCookie == "" {
		t.Fatal("login/finish did not set a session cookie")
	}
	// The minted cookie is a real session.
	var me struct{ ID, Name string }
	if code := doAuthed(t, "GET", ts.URL+"/v1/auth/me", passkeyCookie, "", "", &me); code != 200 {
		t.Fatalf("me with the passkey session: %d", code)
	}
	if me.Name != email {
		t.Fatalf("passkey session resolved to %q, want %q", me.Name, email)
	}

	// The assertion stamped last-used.
	if code := doAuthed(t, "GET", ts.URL+"/v1/auth/webauthn", passkeyCookie, "", "", &list); code != 200 {
		t.Fatalf("list after login: %d", code)
	}
	if len(list.Credentials) != 1 || list.Credentials[0].LastUsedAt == nil {
		t.Fatalf("last_used_at not stamped: %+v", list.Credentials)
	}

	// A replayed assertion — same challenge, same signed response — must be
	// refused: the challenge was consumed by the first finish, and the signature
	// counter has already moved past it.
	var replayEnv errEnvelope
	if code := doJSON(t, "POST", ts.URL+"/v1/auth/webauthn/login/finish", assertionBody, &replayEnv); code != 401 {
		t.Fatalf("replayed assertion returned %d, want 401", code)
	}
	if replayEnv.Error.Code != "invalid_credentials" {
		t.Fatalf("replay error code = %q", replayEnv.Error.Code)
	}

	// Both ceremonies are audited, and the trail carries no key material.
	auditCode, auditBody := rawGet(t, ts.URL+"/v1/audit/events?limit=100", passkeyCookie)
	if auditCode != 200 {
		t.Fatalf("audit events: %d %s", auditCode, auditBody)
	}
	for _, want := range []string{"webauthn.register", "webauthn.login"} {
		if !strings.Contains(auditBody, want) {
			t.Fatalf("audit trail is missing %q:\n%s", want, auditBody)
		}
	}
	// The credential id is a public handle and is deliberately recorded; nothing
	// derived from the private key may appear.
	if strings.Contains(auditBody, waB64(a.key.D.Bytes())) {
		t.Fatal("audit trail leaked private key material")
	}

	// Rename, then delete.
	if code := doAuthed(t, "PATCH", ts.URL+"/v1/auth/webauthn/credentials/"+cred.ID,
		passkeyCookie, "", `{"nickname":"Desk laptop"}`, nil); code != 204 {
		t.Fatalf("rename: %d", code)
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/auth/webauthn", passkeyCookie, "", "", &list); code != 200 {
		t.Fatal("list after rename")
	}
	if list.Credentials[0].Nickname != "Desk laptop" {
		t.Fatalf("rename did not take: %+v", list.Credentials[0])
	}
	if code := doAuthed(t, "DELETE", ts.URL+"/v1/auth/webauthn/credentials/"+cred.ID,
		passkeyCookie, "", "", nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/auth/webauthn", passkeyCookie, "", "", &list); code != 200 {
		t.Fatal("list after delete")
	}
	if len(list.Credentials) != 0 {
		t.Fatalf("credential survived delete: %+v", list.Credentials)
	}
	// Deleting the last passkey does not lock the account out.
	_ = login(t, ts.URL, email, password)
}

// With no RP configured, the feature reports disabled and every ceremony
// endpoint refuses rather than half-working.
func TestWebAuthnE2EDisabled(t *testing.T) {
	ts, email, password := webauthnStack(t, false)
	cookie := login(t, ts.URL, email, password)

	var status struct {
		Enabled bool   `json:"enabled"`
		RPID    string `json:"rp_id"`
	}
	if code := doJSON(t, "GET", ts.URL+"/v1/auth/webauthn/status", "", &status); code != 200 {
		t.Fatalf("status: %d", code)
	}
	if status.Enabled || status.RPID != "" {
		t.Fatalf("status = %+v, want disabled", status)
	}
	var list waList
	if code := doAuthed(t, "GET", ts.URL+"/v1/auth/webauthn", cookie, "", "", &list); code != 200 {
		t.Fatalf("list: %d", code)
	}
	if list.Enabled || len(list.Credentials) != 0 {
		t.Fatalf("list = %+v, want disabled+empty", list)
	}
	var env errEnvelope
	if code := doAuthed(t, "POST", ts.URL+"/v1/auth/webauthn/register/begin", cookie, "", "", &env); code != 409 {
		t.Fatalf("register/begin while disabled: %d", code)
	}
	if env.Error.Code != "webauthn_not_configured" {
		t.Fatalf("code = %q", env.Error.Code)
	}
	if code := doJSON(t, "POST", ts.URL+"/v1/auth/webauthn/login/begin",
		fmt.Sprintf(`{"email":%q}`, email), nil); code != 409 {
		t.Fatal("login/begin while disabled should be 409")
	}
}

// An invalid RP configuration must fail the boot, not present as "passkeys
// mysteriously do not work".
func TestWebAuthnE2EInvalidConfigFailsBoot(t *testing.T) {
	dsn := bootPostgres(t)
	_, _, err := Boot(context.Background(), BootConfig{
		DatabaseURL: dsn, SealType: crypto.SealTypeShamir,
		WebAuthn: auth.WebAuthnConfig{RPID: "janus.example.com", Origins: []string{"https://evil.example.com"}},
	})
	if err == nil {
		t.Fatal("boot accepted an origin that does not match the RP ID")
	}
	if !strings.Contains(err.Error(), "webauthn") {
		t.Fatalf("unhelpful boot error: %v", err)
	}
}
