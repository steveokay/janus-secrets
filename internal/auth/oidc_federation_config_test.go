package auth

import (
	"context"
	"testing"
)

func TestFederationConfigAndBindingValidation(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	_, scopeID := mkScope(t)

	// Empty audience rejected.
	if err := svc.SetFederationConfig(ctx, FederationConfigInput{Audience: "", Enabled: true}); err != ErrValidation {
		t.Fatalf("empty audience: want ErrValidation, got %v", err)
	}
	// Empty issuer defaults to GitHub Actions.
	if err := svc.SetFederationConfig(ctx, FederationConfigInput{Audience: "janus", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	cfg, err := svc.GetFederationConfig(ctx)
	if err != nil || cfg.Issuer != defaultFederationIssuer || cfg.Audience != "janus" {
		t.Fatalf("config: %v %+v", err, cfg)
	}
	// Binding without a repository claim is rejected.
	if _, err := svc.CreateFederationBinding(ctx, FederationBindingInput{
		Name: "bad", MatchClaims: map[string]string{"environment": "prod"},
		ScopeKind: "config", ScopeID: scopeID, Access: "read", TTLSeconds: 900, Enabled: true,
	}); err != ErrValidation {
		t.Fatalf("missing repository: want ErrValidation, got %v", err)
	}
	// TTL over the cap is rejected.
	if _, err := svc.CreateFederationBinding(ctx, FederationBindingInput{
		Name: "toolong", MatchClaims: map[string]string{"repository": "org/app"},
		ScopeKind: "config", ScopeID: scopeID, Access: "read", TTLSeconds: 7200, Enabled: true,
	}); err != ErrValidation {
		t.Fatalf("ttl over cap: want ErrValidation, got %v", err)
	}
	// Unknown scope rejected (well-formed uuid that isn't a config).
	badID, _ := testStore.NewID(ctx)
	if _, err := svc.CreateFederationBinding(ctx, FederationBindingInput{
		Name: "badscope", MatchClaims: map[string]string{"repository": "org/app"},
		ScopeKind: "config", ScopeID: badID, Access: "read", TTLSeconds: 900, Enabled: true,
	}); err == nil {
		t.Fatal("unknown scope: want error")
	}
	// Valid binding, default TTL when 0.
	b, err := svc.CreateFederationBinding(ctx, FederationBindingInput{
		Name: "ok", MatchClaims: map[string]string{"repository": "org/app"},
		ScopeKind: "config", ScopeID: scopeID, Access: "read", TTLSeconds: 0, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if b.TTLSeconds != int(federationDefaultTTL.Seconds()) {
		t.Fatalf("default ttl not applied: %d", b.TTLSeconds)
	}
	if list, _ := svc.ListFederationBindings(ctx); len(list) != 1 {
		t.Fatalf("list len=%d", len(list))
	}
	if err := svc.DeleteFederationBinding(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
}
