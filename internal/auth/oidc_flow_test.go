package auth

import (
	"context"
	"net/url"
	"strings"
	"testing"
)

func TestOIDCStartAndVerify(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	idp := newMockIdP(t, "test-client")
	if err := svc.SetOIDCProvider(ctx, OIDCProviderInput{
		Name: "default", Issuer: idp.srv.URL, ClientID: "test-client",
		ClientSecret: "shh", Scopes: []string{"openid", "email"},
		RedirectURL: "https://app/cb", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	authURL, err := svc.StartOIDCLogin(ctx)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !strings.HasPrefix(authURL, idp.srv.URL+"/authorize") ||
		!strings.Contains(authURL, "code_challenge=") || !strings.Contains(authURL, "state=") {
		t.Fatalf("authURL missing params: %s", authURL)
	}
	state := extractQuery(t, authURL, "state")
	nonce := extractQuery(t, authURL, "nonce")
	idp.sub, idp.email, idp.emailVer, idp.nonce = "sub-1", "who@example.com", true, nonce

	claims, err := svc.verifyOIDCCallback(ctx, state, "any-code")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if claims.Subject != "sub-1" || claims.Email != "who@example.com" || !claims.EmailVerified {
		t.Fatalf("claims mismatch: %+v", claims)
	}
	if _, err := svc.verifyOIDCCallback(ctx, state, "any-code"); err == nil {
		t.Fatal("expected replayed state to be rejected")
	}
}

func extractQuery(t *testing.T, rawurl, key string) string {
	t.Helper()
	u, err := url.Parse(rawurl)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get(key)
}
