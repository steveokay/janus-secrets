package auth

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestMintFederatedToken(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	_, configID := mkScope(t)

	b, err := svc.CreateFederationBinding(ctx, FederationBindingInput{
		Name: "b1", MatchClaims: map[string]string{"repository": "org/app"},
		ScopeKind: "config", ScopeID: configID, Access: "read", TTLSeconds: 900, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, meta, err := svc.MintFederatedToken(ctx, b.Name, "config", configID, "read", 15*time.Minute, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(raw, "janus_svc_") {
		t.Fatalf("token prefix: %s", raw)
	}
	if meta.ExpiresAt == nil {
		t.Fatal("expected expiry")
	}
	// The federated token verifies like any service token.
	p, scope, err := svc.VerifyServiceToken(ctx, raw)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if scope.Kind != "config" || scope.ID != configID {
		t.Fatalf("verified scope wrong: %+v", scope)
	}
	if p.Kind != KindServiceToken {
		t.Fatalf("principal kind = %v, want service-token kind", p.Kind)
	}
}
