package store

import (
	"context"
	"testing"
)

func TestOIDCProviderRepo(t *testing.T) {
	st := requireStore(t)
	r := NewOIDCProviderRepo(st)
	ctx := context.Background()

	// Isolate from any other provider rows left by other tests.
	if _, err := st.pool.Exec(ctx, `TRUNCATE oidc_providers RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	if _, err := r.Get(ctx); err != ErrNotFound {
		t.Fatalf("empty Get: want ErrNotFound, got %v", err)
	}
	in := OIDCProvider{
		Name: "default", Issuer: "https://issuer.example",
		ClientID: "cid", WrappedClientSecret: []byte{1, 2, 3},
		Scopes: []string{"openid", "email"}, RedirectURL: "https://app/cb", Enabled: true,
	}
	if err := r.Put(ctx, in); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := r.Get(ctx)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Issuer != in.Issuer || got.ClientID != "cid" || !got.Enabled ||
		len(got.Scopes) != 2 || string(got.WrappedClientSecret) != string([]byte{1, 2, 3}) {
		t.Fatalf("mismatch: %+v", got)
	}
	in.Issuer = "https://issuer2.example"
	in.Enabled = false
	if err := r.Put(ctx, in); err != nil {
		t.Fatalf("re-put: %v", err)
	}
	got, _ = r.Get(ctx)
	if got.Issuer != "https://issuer2.example" || got.Enabled {
		t.Fatalf("upsert not applied: %+v", got)
	}
	if err := r.Delete(ctx); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := r.Get(ctx); err != ErrNotFound {
		t.Fatalf("post-delete Get: want ErrNotFound, got %v", err)
	}
}
