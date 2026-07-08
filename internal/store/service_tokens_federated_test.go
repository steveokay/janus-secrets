package store

import (
	"context"
	"testing"
	"time"
)

func TestCreateFederatedToken(t *testing.T) {
	st := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	// Isolate from any other binding rows left by other tests (resetDB does not
	// truncate the federation tables).
	if _, err := st.pool.Exec(ctx, `TRUNCATE oidc_federation_bindings RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	// A binding to attribute the token to (FK target).
	bindings := NewOIDCFederationBindingRepo(st)
	scopeID, err := st.NewID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b, err := bindings.Create(ctx, OIDCFederationBinding{
		Name: "b1", MatchClaims: map[string]string{"repository": "org/app"},
		ScopeKind: "config", ScopeID: scopeID, Access: "read", TTLSeconds: 900, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	tokScope, err := st.NewID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	exp := time.Now().Add(15 * time.Minute)
	tok, err := NewServiceTokenRepo(st).CreateFederated(ctx,
		"ci-token", []byte("hmac-bytes-32-.............0123456"), "config", tokScope, "read", &exp, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if tok.CreatedBy != "" {
		t.Fatalf("federated token should have empty CreatedBy, got %q", tok.CreatedBy)
	}
	if tok.FederationBinding != b.ID {
		t.Fatalf("federation_binding = %q, want %q", tok.FederationBinding, b.ID)
	}
	if tok.ExpiresAt == nil {
		t.Fatal("expected expiry set")
	}
}
