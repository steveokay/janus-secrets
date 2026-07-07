package auth

import (
	"context"
	"testing"
)

func TestOIDCResolveMatrix(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	uid, _, err := svc.CreateUser(ctx, "match@example.com")
	if err != nil {
		t.Fatal(err)
	}

	// First login: matched by verified email → link created → session issued.
	cookie, err := svc.resolveOIDCLogin(ctx, &OIDCClaims{
		Issuer: "https://iss", Subject: "sub-1", Email: "match@example.com", EmailVerified: true,
	})
	if err != nil {
		t.Fatalf("first login: %v", err)
	}
	p, err := svc.VerifySession(ctx, cookie)
	if err != nil || p.ID != uid {
		t.Fatalf("session: p=%+v err=%v", p, err)
	}

	// Second login: resolved by (iss, sub) link (email now irrelevant).
	if _, err := svc.resolveOIDCLogin(ctx, &OIDCClaims{
		Issuer: "https://iss", Subject: "sub-1", Email: "changed@example.com", EmailVerified: true,
	}); err != nil {
		t.Fatalf("second login: %v", err)
	}

	// Unknown email → deny (no auto-provision).
	if _, err := svc.resolveOIDCLogin(ctx, &OIDCClaims{
		Issuer: "https://iss", Subject: "sub-2", Email: "nobody@example.com", EmailVerified: true,
	}); err != ErrOIDCDenied {
		t.Fatalf("unknown email: want ErrOIDCDenied, got %v", err)
	}

	// Unverified email → deny even if a user exists.
	if _, err := svc.resolveOIDCLogin(ctx, &OIDCClaims{
		Issuer: "https://iss", Subject: "sub-3", Email: "match@example.com", EmailVerified: false,
	}); err != ErrOIDCDenied {
		t.Fatalf("unverified: want ErrOIDCDenied, got %v", err)
	}

	// Disabled user → deny.
	if err := svc.DisableUser(ctx, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.resolveOIDCLogin(ctx, &OIDCClaims{
		Issuer: "https://iss", Subject: "sub-1", Email: "x", EmailVerified: true,
	}); err != ErrOIDCDenied {
		t.Fatalf("disabled: want ErrOIDCDenied, got %v", err)
	}
}
