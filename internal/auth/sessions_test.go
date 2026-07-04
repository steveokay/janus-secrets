package auth

import (
	"context"
	"errors"
	"testing"
)

func TestLoginVerifyLogout(t *testing.T) {
	svc, email, password := newTestService(t)
	ctx := context.Background()

	cookie, err := svc.Login(ctx, email, []byte(password))
	if err != nil {
		t.Fatal(err)
	}
	if cookie == "" {
		t.Fatal("empty cookie")
	}

	p, err := svc.VerifySession(ctx, cookie)
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind != KindUser || p.Name != email || p.ID == "" {
		t.Fatalf("principal = %+v", p)
	}

	if err := svc.Logout(ctx, cookie); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.VerifySession(ctx, cookie); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("after logout: %v", err)
	}
	// Logout is idempotent.
	if err := svc.Logout(ctx, cookie); err != nil {
		t.Fatalf("double logout: %v", err)
	}
}

func TestLoginIndistinguishableFailures(t *testing.T) {
	svc, email, _ := newTestService(t)
	ctx := context.Background()

	_, errWrongPW := svc.Login(ctx, email, []byte("wrong password"))
	_, errNoUser := svc.Login(ctx, "nobody@example.com", []byte("whatever"))
	if !errors.Is(errWrongPW, ErrInvalidCredentials) || !errors.Is(errNoUser, ErrInvalidCredentials) {
		t.Fatalf("want ErrInvalidCredentials for both: %v / %v", errWrongPW, errNoUser)
	}
	if errWrongPW.Error() != errNoUser.Error() {
		t.Fatalf("distinguishable errors: %q vs %q", errWrongPW, errNoUser)
	}
}

func TestVerifySessionGarbage(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	for _, c := range []string{"", "not-a-cookie", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"} {
		if _, err := svc.VerifySession(ctx, c); !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("cookie %q: %v", c, err)
		}
	}
}

func TestChangePassword(t *testing.T) {
	svc, email, password := newTestService(t)
	ctx := context.Background()
	cookie, err := svc.Login(ctx, email, []byte(password))
	if err != nil {
		t.Fatal(err)
	}
	p, _ := svc.VerifySession(ctx, cookie)

	if err := svc.ChangePassword(ctx, p.ID, []byte("wrong old"), []byte("newpassword1")); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong old pw: %v", err)
	}
	if err := svc.ChangePassword(ctx, p.ID, []byte(password), []byte("newpassword1")); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Login(ctx, email, []byte(password)); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old password still valid: %v", err)
	}
	if _, err := svc.Login(ctx, email, []byte("newpassword1")); err != nil {
		t.Fatalf("new password rejected: %v", err)
	}
}

func TestEnsureHMACKeyIdempotentAndSealed(t *testing.T) {
	svc, email, password := newTestService(t)
	ctx := context.Background()

	// Idempotent: calling again must not replace the key (logins keep working).
	if err := svc.EnsureHMACKey(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Login(ctx, email, []byte(password)); err != nil {
		t.Fatalf("login after re-ensure: %v", err)
	}

	// Sealed keyring: verification surfaces crypto.ErrSealed.
	sealedSvc := NewService(testStore, cryptoNewSealedKeyring())
	if _, err := sealedSvc.VerifySession(ctx, "whatever"); err == nil {
		t.Fatal("sealed verify should fail")
	}
}
