package api

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"

	"github.com/steveokay/janus-secrets/internal/auth"
)

// noRedirectClient returns an http.Client that stops at the first redirect but
// keeps a cookie jar, so the OIDC state-binding cookie set at /login is carried
// to /callback (both under Path=/v1/auth/oidc).
func noRedirectClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func TestOIDCLoginEndToEnd(t *testing.T) {
	ts, srv, _, _, _ := authStackFull(t)
	ctx := t.Context()
	idp := newMockIdP(t, "test-client")

	// Pre-provision the user OIDC will match.
	if _, _, err := srv.auth.CreateUser(ctx, "seed@example.com"); err != nil {
		t.Fatal(err)
	}
	// Configure the provider (direct service call; same package).
	if err := srv.auth.SetOIDCProvider(ctx, auth.OIDCProviderInput{
		Name: "default", Issuer: idp.srv.URL, ClientID: "test-client",
		ClientSecret: "shh", Scopes: []string{"openid", "email"},
		RedirectURL: "https://app/cb", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	// status → enabled.
	var st struct {
		Enabled bool `json:"enabled"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/auth/oidc/status", "", "", "", &st); code != 200 || !st.Enabled {
		t.Fatalf("status: %d %+v", code, st)
	}

	// login → 302 to the IdP.
	client := noRedirectClient(t)
	res, err := client.Get(ts.URL + "/v1/auth/oidc/login")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusFound {
		t.Fatalf("login: want 302, got %d", res.StatusCode)
	}
	loc := res.Header.Get("Location")
	if !strings.HasPrefix(loc, idp.srv.URL+"/authorize") {
		t.Fatalf("login location: %s", loc)
	}
	// login must set the HttpOnly state-binding cookie (login-CSRF defense).
	var boundState bool
	for _, c := range res.Cookies() {
		if c.Name == oidcStateCookieName && c.Value != "" {
			boundState = true
			if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode {
				t.Fatalf("state cookie flags: HttpOnly=%v SameSite=%v", c.HttpOnly, c.SameSite)
			}
		}
	}
	if !boundState {
		t.Fatal("login did not set the OIDC state-binding cookie")
	}
	state := queryParam(t, loc, "state")
	nonce := queryParam(t, loc, "nonce")

	// Drive the IdP + hit callback → 302 + session cookie.
	idp.sub, idp.email, idp.emailVer, idp.nonce = "sub-1", "seed@example.com", true, nonce
	res, err = client.Get(ts.URL + "/v1/auth/oidc/callback?state=" + state + "&code=xyz")
	if err != nil {
		t.Fatal(err)
	}
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
		t.Fatal("callback did not set janus_session")
	}
}

// TestOIDCCallbackRequiresStateCookie is the login-CSRF regression guard: a
// callback carrying a valid (state, code) but NOT the browser's state-binding
// cookie — i.e. a victim following an attacker-supplied callback URL — must be
// rejected and must set no session cookie, even though the state row is valid.
func TestOIDCCallbackRequiresStateCookie(t *testing.T) {
	ts, srv, _, _, _ := authStackFull(t)
	ctx := t.Context()
	idp := newMockIdP(t, "test-client")
	if _, _, err := srv.auth.CreateUser(ctx, "seed@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := srv.auth.SetOIDCProvider(ctx, auth.OIDCProviderInput{
		Name: "default", Issuer: idp.srv.URL, ClientID: "test-client",
		ClientSecret: "shh", Scopes: []string{"openid", "email"},
		RedirectURL: "https://app/cb", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	// Attacker initiates a real login (their own browser holds the cookie).
	attacker := noRedirectClient(t)
	res, err := attacker.Get(ts.URL + "/v1/auth/oidc/login")
	if err != nil {
		t.Fatal(err)
	}
	loc := res.Header.Get("Location")
	state := queryParam(t, loc, "state")
	nonce := queryParam(t, loc, "nonce")
	idp.sub, idp.email, idp.emailVer, idp.nonce = "sub-1", "seed@example.com", true, nonce

	// Victim's browser (fresh jar, no binding cookie) follows the callback URL.
	victim := noRedirectClient(t)
	res, err = victim.Get(ts.URL + "/v1/auth/oidc/callback?state=" + state + "&code=xyz")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("callback without binding cookie: want 400, got %d", res.StatusCode)
	}
	for _, c := range res.Cookies() {
		if c.Name == sessionCookieName && c.Value != "" && c.MaxAge >= 0 {
			t.Fatal("callback issued a session to an unbound browser (login CSRF)")
		}
	}
}

// queryParam extracts a single query parameter from a raw URL.
func queryParam(t *testing.T, rawurl, key string) string {
	t.Helper()
	u, err := url.Parse(rawurl)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get(key)
}
