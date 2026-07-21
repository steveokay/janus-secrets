package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLoginVerifyLogout(t *testing.T) {
	svc, email, password := newTestService(t)
	ctx := context.Background()

	cookie, err := svc.Login(ctx, email, []byte(password), "")
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

	_, errWrongPW := svc.Login(ctx, email, []byte("wrong password"), "")
	_, errNoUser := svc.Login(ctx, "nobody@example.com", []byte("whatever"), "")
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
	cookie, err := svc.Login(ctx, email, []byte(password), "")
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
	if _, err := svc.Login(ctx, email, []byte(password), ""); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old password still valid: %v", err)
	}
	if _, err := svc.Login(ctx, email, []byte("newpassword1"), ""); err != nil {
		t.Fatalf("new password rejected: %v", err)
	}
}

func TestCreateSessionForUser(t *testing.T) {
	svc, email, password := newTestService(t)
	ctx := context.Background()

	cookie, err := svc.Login(ctx, email, []byte(password), "")
	if err != nil {
		t.Fatal(err)
	}
	p, err := svc.VerifySession(ctx, cookie)
	if err != nil {
		t.Fatal(err)
	}

	cookie2, err := svc.createSession(ctx, p.ID)
	if err != nil {
		t.Fatalf("createSession: %v", err)
	}
	p2, err := svc.VerifySession(ctx, cookie2)
	if err != nil || p2.Kind != KindUser || p2.ID != p.ID {
		t.Fatalf("verify minted session: p=%+v err=%v", p2, err)
	}
}

func TestEnsureHMACKeyIdempotentAndSealed(t *testing.T) {
	svc, email, password := newTestService(t)
	ctx := context.Background()

	// Idempotent: calling again must not replace the key (logins keep working).
	if err := svc.EnsureHMACKey(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Login(ctx, email, []byte(password), ""); err != nil {
		t.Fatalf("login after re-ensure: %v", err)
	}

	// Sealed keyring: verification surfaces crypto.ErrSealed.
	sealedSvc := NewService(testStore, cryptoNewSealedKeyring())
	if _, err := sealedSvc.VerifySession(ctx, "whatever"); err == nil {
		t.Fatal("sealed verify should fail")
	}
}

func TestVerifySessionIdleTimeout(t *testing.T) {
	svc, email, password := newTestService(t)
	svc.SetSessionIdleTimeout(30 * time.Minute)
	ctx := context.Background()
	cookie, err := svc.Login(ctx, email, []byte(password), "")
	if err != nil {
		t.Fatal(err)
	}
	// Fresh session verifies.
	if _, err := svc.VerifySession(ctx, cookie); err != nil {
		t.Fatalf("fresh session: %v", err)
	}
	// Backdate last_seen_at beyond the idle window.
	if _, err := resetPool.Exec(ctx,
		`UPDATE sessions SET last_seen_at = now() - interval '31 minutes'`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.VerifySession(ctx, cookie); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("want ErrSessionExpired, got %v", err)
	}
	// The idle-expired session row was deleted.
	var n int
	if err := resetPool.QueryRow(ctx, `SELECT count(*) FROM sessions`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("idle-expired session not deleted (%d rows)", n)
	}
}

func TestVerifySessionIdleDisabled(t *testing.T) {
	svc, email, password := newTestService(t)
	// Zero (the default) disables idle enforcement entirely.
	ctx := context.Background()
	cookie, err := svc.Login(ctx, email, []byte(password), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resetPool.Exec(ctx,
		`UPDATE sessions SET last_seen_at = now() - interval '23 hours'`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.VerifySession(ctx, cookie); err != nil {
		t.Fatalf("idle-disabled session should verify: %v", err)
	}
}

func TestVerifySessionAbsoluteTTLStillEnforced(t *testing.T) {
	svc, email, password := newTestService(t)
	svc.SetSessionIdleTimeout(30 * time.Minute)
	ctx := context.Background()
	cookie, err := svc.Login(ctx, email, []byte(password), "")
	if err != nil {
		t.Fatal(err)
	}
	// Recently active but past the 24h absolute expiry → plain unauthenticated.
	if _, err := resetPool.Exec(ctx,
		`UPDATE sessions SET expires_at = now() - interval '1 minute', last_seen_at = now()`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.VerifySession(ctx, cookie); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("want ErrUnauthenticated, got %v", err)
	}
}
