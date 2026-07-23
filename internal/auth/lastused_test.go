package auth

import (
	"context"
	"testing"
	"time"
)

// TestVerifyServiceTokenStampsLastUsed verifies a successful token verification
// stamps last_used_at (visible via ListTokens), and that a second verification
// within the throttle window does NOT rewrite it.
func TestVerifyServiceTokenStampsLastUsed(t *testing.T) {
	svc, email, password := newTestService(t)
	ctx := context.Background()
	cookie, _ := svc.Login(ctx, email, []byte(password), "")
	admin, _ := svc.VerifySession(ctx, cookie)
	_, configID := mkScope(t)

	raw, meta, err := svc.MintServiceToken(ctx, admin, "ci", "config", configID, "read", nil)
	if err != nil {
		t.Fatal(err)
	}

	lastUsed := func() *time.Time {
		list, lErr := svc.ListTokens(ctx)
		if lErr != nil {
			t.Fatal(lErr)
		}
		for i := range list {
			if list[i].ID == meta.ID {
				return list[i].LastUsedAt
			}
		}
		t.Fatalf("token %s not in list", meta.ID)
		return nil
	}

	if lu := lastUsed(); lu != nil {
		t.Fatalf("pre-use token should have nil last_used_at, got %v", lu)
	}

	if _, _, err := svc.VerifyServiceToken(ctx, raw); err != nil {
		t.Fatal(err)
	}
	first := lastUsed()
	if first == nil {
		t.Fatal("last_used_at not stamped after first verification")
	}

	// Second verification inside the 60s throttle window must not move the stamp.
	if _, _, err := svc.VerifyServiceToken(ctx, raw); err != nil {
		t.Fatal(err)
	}
	second := lastUsed()
	if second == nil || !second.Equal(*first) {
		t.Fatalf("throttled verification rewrote last_used_at: %v -> %v", first, second)
	}
}

// TestVerifyServiceTokenLastUsedUpdateNonFatal proves a failure to record
// last_used_at never fails an otherwise-valid token authentication. We install a
// BEFORE UPDATE trigger that raises on service_tokens, so the throttled UPDATE in
// TouchLastUsed fails while the GetByHMAC SELECT still works; verification must
// still resolve the principal.
func TestVerifyServiceTokenLastUsedUpdateNonFatal(t *testing.T) {
	svc, email, password := newTestService(t)
	ctx := context.Background()
	cookie, _ := svc.Login(ctx, email, []byte(password), "")
	admin, _ := svc.VerifySession(ctx, cookie)
	_, configID := mkScope(t)

	raw, meta, err := svc.MintServiceToken(ctx, admin, "ci", "config", configID, "read", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Make any UPDATE on service_tokens fail (SELECTs stay fine).
	if _, err := resetPool.Exec(ctx, `
		CREATE OR REPLACE FUNCTION janus_test_block_svc_update() RETURNS trigger AS $$
		BEGIN RAISE EXCEPTION 'blocked for test'; END; $$ LANGUAGE plpgsql;
		CREATE TRIGGER janus_test_block_svc_update BEFORE UPDATE ON service_tokens
			FOR EACH ROW EXECUTE FUNCTION janus_test_block_svc_update();`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = resetPool.Exec(context.Background(),
			`DROP TRIGGER IF EXISTS janus_test_block_svc_update ON service_tokens;
			 DROP FUNCTION IF EXISTS janus_test_block_svc_update();`)
	})

	// The best-effort TouchLastUsed will error, but verification must succeed.
	p, _, err := svc.VerifyServiceToken(ctx, raw)
	if err != nil {
		t.Fatalf("verification must not fail on best-effort last-used update: %v", err)
	}
	if p.ID != meta.ID {
		t.Fatalf("principal id = %s, want %s", p.ID, meta.ID)
	}
}

// TestLoginStampsLastLogin verifies a successful password login stamps
// last_login_at (surfaced via ListUsers).
func TestLoginStampsLastLogin(t *testing.T) {
	svc, email, password := newTestService(t)
	ctx := context.Background()

	before, err := svc.ListUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range before {
		if u.Email == email && u.LastLoginAt != nil {
			t.Fatalf("pre-login user should have nil last_login_at, got %v", u.LastLoginAt)
		}
	}

	if _, err := svc.Login(ctx, email, []byte(password), ""); err != nil {
		t.Fatal(err)
	}

	after, err := svc.ListUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, u := range after {
		if u.Email == email {
			found = true
			if u.LastLoginAt == nil {
				t.Fatal("last_login_at not stamped after successful login")
			}
		}
	}
	if !found {
		t.Fatalf("user %s not in list", email)
	}
}

// TestFailedLoginDoesNotStampLastLogin verifies a rejected login leaves
// last_login_at untouched.
func TestFailedLoginDoesNotStampLastLogin(t *testing.T) {
	svc, email, _ := newTestService(t)
	ctx := context.Background()
	if _, err := svc.Login(ctx, email, []byte("wrong-password"), ""); err == nil {
		t.Fatal("expected login failure")
	}
	users, err := svc.ListUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range users {
		if u.Email == email && u.LastLoginAt != nil {
			t.Fatalf("failed login must not stamp last_login_at, got %v", u.LastLoginAt)
		}
	}
}
