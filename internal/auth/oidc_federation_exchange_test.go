package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestFederateCILogin(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	idp := newMockIdP(t, "janus")
	_, configID := mkScope(t)

	if err := svc.SetFederationConfig(ctx, FederationConfigInput{
		Issuer: idp.srv.URL, Audience: "janus", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateFederationBinding(ctx, FederationBindingInput{
		Name: "prod", MatchClaims: map[string]string{"repository": "org/app", "environment": "prod"},
		ScopeKind: "config", ScopeID: configID, Access: "read", TTLSeconds: 900, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	good := idp.signClaims(t, map[string]any{
		"iss": idp.srv.URL, "aud": "janus", "sub": "repo:org/app:environment:prod",
		"exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(),
		"repository": "org/app", "environment": "prod", "ref": "refs/heads/main",
	})
	res, err := svc.FederateCILogin(ctx, good)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if res.Binding != "prod" || res.Repository != "org/app" || res.Token == "" {
		t.Fatalf("result: %+v", res)
	}
	if _, scope, err := svc.VerifyServiceToken(ctx, res.Token); err != nil || scope.ID != configID {
		t.Fatalf("verify minted: %v %+v", err, scope)
	}

	// Wrong audience → ErrFederationVerify.
	badAud := idp.signClaims(t, map[string]any{
		"iss": idp.srv.URL, "aud": "someone-else", "sub": "x",
		"exp": time.Now().Add(time.Hour).Unix(), "repository": "org/app", "environment": "prod",
	})
	if _, err := svc.FederateCILogin(ctx, badAud); !errors.Is(err, ErrFederationVerify) {
		t.Fatalf("bad aud: want ErrFederationVerify, got %v", err)
	}
	// No matching binding → ErrFederationNoMatch.
	noMatch := idp.signClaims(t, map[string]any{
		"iss": idp.srv.URL, "aud": "janus", "sub": "x",
		"exp": time.Now().Add(time.Hour).Unix(), "repository": "org/other",
	})
	if _, err := svc.FederateCILogin(ctx, noMatch); !errors.Is(err, ErrFederationNoMatch) {
		t.Fatalf("no match: want ErrFederationNoMatch, got %v", err)
	}
	// Expired token → ErrFederationVerify.
	expired := idp.signClaims(t, map[string]any{
		"iss": idp.srv.URL, "aud": "janus", "sub": "x",
		"exp": time.Now().Add(-time.Hour).Unix(), "repository": "org/app", "environment": "prod",
	})
	if _, err := svc.FederateCILogin(ctx, expired); !errors.Is(err, ErrFederationVerify) {
		t.Fatalf("expired: want ErrFederationVerify, got %v", err)
	}
}
