package store

import (
	"context"
	"testing"
)

func TestFederationConfigRoundTrip(t *testing.T) {
	st := requireStore(t)
	ctx := context.Background()
	r := NewOIDCFederationConfigRepo(st)

	// Isolate from any other config rows left by other tests.
	if _, err := st.pool.Exec(ctx, `TRUNCATE oidc_federation_config RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	if _, err := r.Get(ctx); err != ErrNotFound {
		t.Fatalf("empty Get: want ErrNotFound, got %v", err)
	}
	if err := r.Put(ctx, OIDCFederationConfig{
		Issuer: "https://iss.example", Audience: "janus", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	// Put is a single-row upsert: a second Put replaces, not appends.
	if err := r.Put(ctx, OIDCFederationConfig{
		Issuer: "https://iss.example", Audience: "janus2", Enabled: false,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := r.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Audience != "janus2" || got.Enabled {
		t.Fatalf("upsert not applied: %+v", got)
	}
	if err := r.Delete(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Get(ctx); err != ErrNotFound {
		t.Fatalf("after delete: want ErrNotFound, got %v", err)
	}
}

// TestFederationIssuerSet covers the multi-issuer trust set: Upsert adds or
// updates one issuer without disturbing the others, List returns them all, and
// DeleteByID removes exactly one.
func TestFederationIssuerSet(t *testing.T) {
	st := requireStore(t)
	ctx := context.Background()
	r := NewOIDCFederationConfigRepo(st)

	if _, err := st.pool.Exec(ctx, `TRUNCATE oidc_federation_config RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	ci, err := r.Upsert(ctx, OIDCFederationConfig{
		Issuer: "https://token.actions.githubusercontent.com", Audience: "janus",
		Preset: "github", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Upsert(ctx, OIDCFederationConfig{
		Issuer: "https://oidc.eks.eu-west-1.amazonaws.com/id/EXAMPLE", Audience: "janus",
		Preset: "kubernetes", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	list, err := r.List(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("list: %v len=%d", err, len(list))
	}

	// Re-upserting the same issuer updates in place rather than appending.
	if _, err := r.Upsert(ctx, OIDCFederationConfig{
		Issuer: ci.Issuer, Audience: "janus-2", Preset: "github", Enabled: false}); err != nil {
		t.Fatal(err)
	}
	list, err = r.List(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("list after update: %v len=%d", err, len(list))
	}
	for _, c := range list {
		if c.Issuer == ci.Issuer && (c.Audience != "janus-2" || c.Enabled) {
			t.Fatalf("update not applied: %+v", c)
		}
		if c.Preset == "" {
			t.Fatalf("preset not stored: %+v", c)
		}
	}

	if err := r.DeleteByID(ctx, ci.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := r.List(ctx); len(list) != 1 {
		t.Fatalf("after delete len=%d", len(list))
	}
	if err := r.DeleteByID(ctx, ci.ID); err != ErrNotFound {
		t.Fatalf("second delete: want ErrNotFound, got %v", err)
	}
	if err := r.Delete(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Get(ctx); err != ErrNotFound {
		t.Fatalf("after delete-all: want ErrNotFound, got %v", err)
	}
}

func TestFederationBindingRepo(t *testing.T) {
	st := requireStore(t)
	ctx := context.Background()

	// Isolate from any other binding rows left by other tests.
	if _, err := st.pool.Exec(ctx, `TRUNCATE oidc_federation_bindings RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	scopeID, err := st.NewID(ctx)
	if err != nil {
		t.Fatalf("new id: %v", err)
	}
	r := NewOIDCFederationBindingRepo(st)

	b := OIDCFederationBinding{
		Name:        "prod-deploy",
		Issuer:      "https://token.actions.githubusercontent.com",
		MatchClaims: map[string]string{"repository": "org/app", "environment": "prod"},
		ScopeKind:   "config", ScopeID: scopeID, Access: "read", TTLSeconds: 900, Enabled: true,
	}
	created, err := r.Create(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.MatchClaims["repository"] != "org/app" {
		t.Fatalf("create returned %+v", created)
	}
	if created.Issuer != b.Issuer {
		t.Fatalf("issuer round-trip: %q", created.Issuer)
	}
	list, err := r.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v len=%d", err, len(list))
	}
	if list[0].MatchClaims["environment"] != "prod" || list[0].TTLSeconds != 900 {
		t.Fatalf("round-trip mismatch: %+v", list[0])
	}
	if err := r.Delete(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := r.List(ctx); len(list) != 0 {
		t.Fatalf("after delete len=%d", len(list))
	}
}
