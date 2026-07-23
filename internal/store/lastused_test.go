package store

import (
	"context"
	"testing"
	"time"
)

// TestTouchLastUsedThrottle verifies the throttled last_used_at update: the first
// touch stamps a time; a second touch inside the throttle window is a no-op (the
// stamp does not move); a touch after the window advances it.
func TestTouchLastUsedThrottle(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	hash := "$argon2id$v=19$m=19456,t=2,p=1$c2FsdA$aGFzaA"
	u, err := NewUserRepo(s).Create(ctx, "tok@b.c", &hash)
	if err != nil {
		t.Fatal(err)
	}
	_, _, configID := mkConfig(t, s, "prod")
	repo := NewServiceTokenRepo(s)
	mac := []byte("hmac-of-raw-token-0123456789abcd")
	tok, err := repo.Create(ctx, "ci", mac, u.ID, "config", configID, "read", nil)
	if err != nil {
		t.Fatal(err)
	}
	if tok.LastUsedAt != nil {
		t.Fatalf("fresh token should have nil last_used_at, got %v", tok.LastUsedAt)
	}

	// First touch stamps it.
	if err := repo.TouchLastUsed(ctx, tok.ID, 60*time.Second); err != nil {
		t.Fatalf("first touch: %v", err)
	}
	after1, err := repo.GetByHMAC(ctx, mac)
	if err != nil {
		t.Fatal(err)
	}
	if after1.LastUsedAt == nil {
		t.Fatal("last_used_at not stamped after first touch")
	}
	first := *after1.LastUsedAt

	// Second touch INSIDE the throttle window is a no-op: the stamp must not move.
	if err := repo.TouchLastUsed(ctx, tok.ID, 60*time.Second); err != nil {
		t.Fatalf("throttled touch: %v", err)
	}
	after2, err := repo.GetByHMAC(ctx, mac)
	if err != nil {
		t.Fatal(err)
	}
	if !after2.LastUsedAt.Equal(first) {
		t.Fatalf("throttled touch rewrote last_used_at: %v -> %v", first, *after2.LastUsedAt)
	}

	// A touch with a zero-length window (everything is "older than now") advances it.
	if err := repo.TouchLastUsed(ctx, tok.ID, 0); err != nil {
		t.Fatalf("unthrottled touch: %v", err)
	}
	after3, err := repo.GetByHMAC(ctx, mac)
	if err != nil {
		t.Fatal(err)
	}
	if !after3.LastUsedAt.After(first) {
		t.Fatalf("unthrottled touch should advance last_used_at: first=%v now=%v", first, *after3.LastUsedAt)
	}
}

// TestTouchLastUsedMissingTokenNoError confirms touching a non-existent token id
// is not an error (best-effort, non-fatal on the auth path).
func TestTouchLastUsedMissingTokenNoError(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	repo := NewServiceTokenRepo(s)
	if err := repo.TouchLastUsed(ctx, "00000000-0000-0000-0000-000000000000", 60*time.Second); err != nil {
		t.Fatalf("touch of missing token should be a no-op, got %v", err)
	}
}

// TestTouchLastLogin verifies last_login_at is nil for a fresh user and stamped
// after a touch.
func TestTouchLastLogin(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	repo := NewUserRepo(s)
	hash := "$argon2id$v=19$m=19456,t=2,p=1$c2FsdA$aGFzaA"
	u, err := repo.Create(ctx, "login@b.c", &hash)
	if err != nil {
		t.Fatal(err)
	}
	if u.LastLoginAt != nil {
		t.Fatalf("fresh user should have nil last_login_at, got %v", u.LastLoginAt)
	}
	if err := repo.TouchLastLogin(ctx, u.ID); err != nil {
		t.Fatalf("touch last login: %v", err)
	}
	got, err := repo.Get(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastLoginAt == nil {
		t.Fatal("last_login_at not stamped after TouchLastLogin")
	}
}
