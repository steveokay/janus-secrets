package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/steveokay/janus-secrets/internal/crypto"
)

// authStackFull boots the real stack (Postgres, shamir 1-of-1), initializes
// with an admin, unseals, creates one config as a token-scope target, and
// returns the httptest server, the *Server, admin email, admin password, and
// the config id.
func authStackFull(t *testing.T) (*httptest.Server, *Server, string, string, string) {
	ts, srv, email, password, cid, _ := authStackFullDSN(t)
	return ts, srv, email, password, cid
}

// authStackFullDSN is authStackFull plus the underlying Postgres DSN, so a test
// can open its own pool for direct-table assertions (e.g. proving a table stores
// no secret). All other behavior is identical.
func authStackFullDSN(t *testing.T) (*httptest.Server, *Server, string, string, string, string) {
	t.Helper()
	dsn := bootPostgres(t)
	ctx := context.Background()
	srv, st, err := Boot(ctx, BootConfig{
		DatabaseURL: dsn, SealType: crypto.SealTypeShamir,
	})
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
	if ir.Admin == nil || ir.Admin.Password == "" || ir.Admin.Email != "root@corp.io" {
		t.Fatalf("admin credential missing: %+v", ir.Admin)
	}
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/unseal",
		fmt.Sprintf(`{"share":%q}`, ir.Shares[0]), nil); code != 200 {
		t.Fatalf("unseal failed")
	}

	// A scope target for token tests, created through the server's own wired
	// secrets service (same package, direct field access).
	p, err := srv.service.CreateProject(ctx, "authstack", "AuthStack")
	if err != nil {
		t.Fatal(err)
	}
	e, err := srv.service.CreateEnvironment(ctx, p.ID, "prod", "Prod")
	if err != nil {
		t.Fatal(err)
	}
	c, err := srv.service.CreateConfig(ctx, e.ID, "root", nil)
	if err != nil {
		t.Fatal(err)
	}
	return ts, srv, ir.Admin.Email, ir.Admin.Password, c.ID, dsn
}

// authStack is the four-value form used by the existing e2e tests.
func authStack(t *testing.T) (*httptest.Server, string, string, string) {
	ts, _, email, password, configID := authStackFull(t)
	return ts, email, password, configID
}

// doAuthed issues a request with either a session cookie or bearer token.
func doAuthed(t *testing.T, method, url, cookie, bearer, body string, out any) int {
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
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		_ = json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

// login returns the session cookie value.
func login(t *testing.T, base, email, password string) string {
	t.Helper()
	resp, err := http.Post(base+"/v1/auth/login", "application/json",
		strings.NewReader(fmt.Sprintf(`{"email":%q,"password":%q}`, email, password)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("login: %d %s", resp.StatusCode, b)
	}
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookieName {
			if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
				t.Fatalf("cookie flags: HttpOnly=%v SameSite=%v", c.HttpOnly, c.SameSite)
			}
			return c.Value
		}
	}
	t.Fatal("no session cookie set")
	return ""
}

func TestAuthLifecycleE2E(t *testing.T) {
	ts, email, password, _ := authStack(t)

	// Wrong password vs unknown user: byte-identical bodies.
	bad1, _ := http.Post(ts.URL+"/v1/auth/login", "application/json",
		strings.NewReader(fmt.Sprintf(`{"email":%q,"password":"wrong"}`, email)))
	bad2, _ := http.Post(ts.URL+"/v1/auth/login", "application/json",
		strings.NewReader(`{"email":"ghost@nowhere.io","password":"wrong"}`))
	b1, _ := io.ReadAll(bad1.Body)
	b2, _ := io.ReadAll(bad2.Body)
	bad1.Body.Close()
	bad2.Body.Close()
	if bad1.StatusCode != 401 || bad2.StatusCode != 401 || string(b1) != string(b2) {
		t.Fatalf("enumeration oracle: %d %q vs %d %q", bad1.StatusCode, b1, bad2.StatusCode, b2)
	}

	cookie := login(t, ts.URL, email, password)

	// me.
	var me struct{ Kind, ID, Name string }
	if code := doAuthed(t, "GET", ts.URL+"/v1/auth/me", cookie, "", "", &me); code != 200 || me.Kind != "user" || me.Name != email {
		t.Fatalf("me: %d %+v", code, me)
	}

	// Mint against a missing scope → 404.
	var env errEnvelope
	if code := doAuthed(t, "POST", ts.URL+"/v1/tokens", cookie, "",
		`{"name":"ci","scope":{"kind":"config","id":"00000000-0000-0000-0000-000000000000"},"access":"read"}`, &env); code != 404 {
		t.Fatalf("missing scope: %d %+v", code, env)
	}

	// Unauthenticated requests are rejected.
	if code := doAuthed(t, "GET", ts.URL+"/v1/tokens", "", "", "", &env); code != 401 || env.Error.Code != "unauthenticated" {
		t.Fatalf("unauth list: %d %+v", code, env)
	}

	// sys/seal now requires auth.
	if code := doAuthed(t, "POST", ts.URL+"/v1/sys/seal", "", "", "", &env); code != 401 {
		t.Fatalf("unauth seal: %d", code)
	}
	var sealResp struct{ Sealed bool }
	if code := doAuthed(t, "POST", ts.URL+"/v1/sys/seal", cookie, "", "", &sealResp); code != 200 || !sealResp.Sealed {
		t.Fatalf("authed seal: %d %+v", code, sealResp)
	}
	// While sealed, the session cannot verify → 503.
	if code := doAuthed(t, "GET", ts.URL+"/v1/auth/me", cookie, "", "", &env); code != 503 || env.Error.Code != "sealed" {
		t.Fatalf("sealed me: %d %+v", code, env)
	}
}

func TestTokenLifecycleE2E(t *testing.T) {
	ts, email, password, configID := authStack(t)
	cookie := login(t, ts.URL, email, password)

	// Mint — token appears exactly once.
	var minted struct {
		Token string `json:"token"`
		ID    string `json:"id"`
	}
	body := fmt.Sprintf(`{"name":"ci","scope":{"kind":"config","id":%q},"access":"read","ttl_seconds":3600}`, configID)
	if code := doAuthed(t, "POST", ts.URL+"/v1/tokens", cookie, "", body, &minted); code != 200 {
		t.Fatalf("mint: %d", code)
	}
	if !strings.HasPrefix(minted.Token, "janus_svc_") || minted.ID == "" {
		t.Fatalf("minted = %+v", minted)
	}

	// The token authenticates.
	var me struct{ Kind, Name string }
	if code := doAuthed(t, "GET", ts.URL+"/v1/auth/me", "", minted.Token, "", &me); code != 200 || me.Kind != "service_token" || me.Name != "ci" {
		t.Fatalf("token me: %d %+v", code, me)
	}

	// List never contains the raw token.
	listResp, err := http.NewRequest("GET", ts.URL+"/v1/tokens", nil)
	if err != nil {
		t.Fatal(err)
	}
	listResp.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	resp, err := http.DefaultClient.Do(listResp)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(raw), minted.Token) {
		t.Fatal("raw token leaked into list response")
	}

	// Revoke → token stops working; double revoke 404.
	if code := doAuthed(t, "DELETE", ts.URL+"/v1/tokens/"+minted.ID, cookie, "", "", nil); code != 204 {
		t.Fatalf("revoke: %d", code)
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/auth/me", "", minted.Token, "", nil); code != 401 {
		t.Fatalf("revoked token still works: %d", code)
	}
	if code := doAuthed(t, "DELETE", ts.URL+"/v1/tokens/"+minted.ID, cookie, "", "", nil); code != 404 {
		t.Fatalf("double revoke: %d", code)
	}

	// Logout → session stops working.
	if code := doAuthed(t, "POST", ts.URL+"/v1/auth/logout", cookie, "", "", nil); code != 204 {
		t.Fatalf("logout: %d", code)
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/auth/me", cookie, "", "", nil); code != 401 {
		t.Fatalf("session after logout: %d", code)
	}
}

func TestPasswordChangeE2E(t *testing.T) {
	ts, email, password, _ := authStack(t)
	cookie := login(t, ts.URL, email, password)

	if code := doAuthed(t, "POST", ts.URL+"/v1/auth/password", cookie, "",
		fmt.Sprintf(`{"old":%q,"new":"a-much-better-password"}`, password), nil); code != 204 {
		t.Fatalf("change: %d", code)
	}
	// Old password dead, new works.
	resp, _ := http.Post(ts.URL+"/v1/auth/login", "application/json",
		strings.NewReader(fmt.Sprintf(`{"email":%q,"password":%q}`, email, password)))
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("old pw: %d", resp.StatusCode)
	}
	_ = login(t, ts.URL, email, "a-much-better-password")
}

func TestLoginRateLimitE2E(t *testing.T) {
	ts, email, _, _ := authStack(t)
	// Hammer login; the limiter (burst 5) must eventually 429.
	var got429 bool
	for i := 0; i < 12; i++ {
		resp, err := http.Post(ts.URL+"/v1/auth/login", "application/json",
			strings.NewReader(fmt.Sprintf(`{"email":%q,"password":"wrong"}`, email)))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode == 429 {
			got429 = true
			break
		}
	}
	if !got429 {
		t.Fatal("rate limiter never engaged")
	}
}
