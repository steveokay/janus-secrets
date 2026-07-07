package auth

import (
	"context"
	"testing"
)

func TestOIDCProviderConfigRoundTrip(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	if _, err := svc.GetOIDCProvider(ctx); err != ErrNotFound {
		t.Fatalf("empty: want ErrNotFound, got %v", err)
	}
	err := svc.SetOIDCProvider(ctx, OIDCProviderInput{
		Name: "default", Issuer: "https://issuer.example", ClientID: "cid",
		ClientSecret: "the-secret", Scopes: []string{"openid", "email"},
		RedirectURL: "https://app/cb", Enabled: true,
	})
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	view, err := svc.GetOIDCProvider(ctx)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if view.Issuer != "https://issuer.example" || view.ClientID != "cid" || !view.SecretSet || !view.Enabled {
		t.Fatalf("view mismatch: %+v", view)
	}
	if err := svc.DeleteOIDCProvider(ctx); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := svc.GetOIDCProvider(ctx); err != ErrNotFound {
		t.Fatalf("post-delete: want ErrNotFound, got %v", err)
	}
}
