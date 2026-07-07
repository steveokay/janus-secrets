package api

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/steveokay/janus-secrets/internal/auth"
)

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
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
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

// queryParam extracts a single query parameter from a raw URL.
func queryParam(t *testing.T, rawurl, key string) string {
	t.Helper()
	u, err := url.Parse(rawurl)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get(key)
}
