package api

import (
	"strings"
	"testing"
	"time"

	"github.com/steveokay/janus-secrets/internal/auth"
)

func TestOIDCFederateExchange(t *testing.T) {
	ts, srv, _, _, configID := authStackFull(t)
	ctx := t.Context()
	idp := newMockIdP(t, "janus")

	if err := srv.auth.SetFederationConfig(ctx, auth.FederationConfigInput{
		Issuer: idp.srv.URL, Audience: "janus", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.auth.CreateFederationBinding(ctx, auth.FederationBindingInput{
		Name: "prod", MatchClaims: map[string]string{"repository": "org/app"},
		ScopeKind: "config", ScopeID: configID, Access: "read", TTLSeconds: 900, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	sign := func(claims map[string]any) string { return idp.signClaims(t, claims) }
	base := map[string]any{"iss": idp.srv.URL, "aud": "janus", "sub": "s",
		"exp": time.Now().Add(time.Hour).Unix(), "repository": "org/app"}

	// Happy path → 200 with a janus_svc_ token.
	var ok struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expires_at"`
	}
	if code := doJSON(t, "POST", ts.URL+"/v1/auth/oidc/federate", `{"token":"`+sign(base)+`"}`, &ok); code != 200 {
		t.Fatalf("exchange: %d", code)
	}
	if !strings.HasPrefix(ok.Token, "janus_svc_") || ok.ExpiresAt == "" {
		t.Fatalf("token response: %+v", ok)
	}

	// Wrong audience and no-match must be the SAME indistinguishable 401 denial.
	badAud := map[string]any{"iss": idp.srv.URL, "aud": "x", "sub": "s",
		"exp": time.Now().Add(time.Hour).Unix(), "repository": "org/app"}
	noRepo := map[string]any{"iss": idp.srv.URL, "aud": "janus", "sub": "s",
		"exp": time.Now().Add(time.Hour).Unix(), "repository": "org/nope"}
	var e1, e2 errEnvelope
	c1 := doJSON(t, "POST", ts.URL+"/v1/auth/oidc/federate", `{"token":"`+sign(badAud)+`"}`, &e1)
	c2 := doJSON(t, "POST", ts.URL+"/v1/auth/oidc/federate", `{"token":"`+sign(noRepo)+`"}`, &e2)
	if c1 != 401 || c2 != 401 || e1.Error.Code != "federation_denied" || e2.Error.Code != e1.Error.Code {
		t.Fatalf("denials not indistinguishable: %d/%s %d/%s", c1, e1.Error.Code, c2, e2.Error.Code)
	}
}
