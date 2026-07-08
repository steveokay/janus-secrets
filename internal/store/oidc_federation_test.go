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
