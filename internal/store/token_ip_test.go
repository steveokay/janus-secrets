package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestServiceTokenIPAllowlist(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	hash := "$argon2id$v=19$m=19456,t=2,p=1$c2FsdA$aGFzaA"
	u, err := NewUserRepo(s).Create(ctx, "a@b.c", &hash)
	if err != nil {
		t.Fatal(err)
	}
	_, _, configID := mkConfig(t, s, "prod")
	repo := NewServiceTokenRepo(s)

	mac := []byte("hmac-of-raw-token-0123456789abcd")
	// Create with an allowlist; it round-trips.
	tok, err := repo.Create(ctx, "ci", mac, u.ID, "config", configID, "read", nil,
		[]string{"10.0.0.0/8", "2001:db8::/32"})
	if err != nil {
		t.Fatal(err)
	}
	if len(tok.IPAllowlist) != 2 || tok.IPAllowlist[0] != "10.0.0.0/8" {
		t.Fatalf("allowlist = %v", tok.IPAllowlist)
	}
	got, _ := repo.GetByHMAC(ctx, mac)
	if len(got.IPAllowlist) != 2 {
		t.Fatalf("get allowlist = %v", got.IPAllowlist)
	}

	// An empty allowlist persists as NULL → nil slice.
	mac2 := []byte("hmac-of-raw-token-abcdef98765432")
	tok2, err := repo.Create(ctx, "ci2", mac2, u.ID, "config", configID, "read", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tok2.IPAllowlist) != 0 {
		t.Fatalf("empty allowlist = %v", tok2.IPAllowlist)
	}

	// SetIPAllowlist replaces then clears.
	if err := repo.SetIPAllowlist(ctx, tok2.ID, []string{"172.16.0.0/12"}); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.GetByHMAC(ctx, mac2)
	if len(got.IPAllowlist) != 1 || got.IPAllowlist[0] != "172.16.0.0/12" {
		t.Fatalf("after set = %v", got.IPAllowlist)
	}
	if err := repo.SetIPAllowlist(ctx, tok2.ID, nil); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.GetByHMAC(ctx, mac2)
	if len(got.IPAllowlist) != 0 {
		t.Fatalf("after clear = %v", got.IPAllowlist)
	}
	// Missing token → ErrNotFound.
	if err := repo.SetIPAllowlist(ctx, "00000000-0000-0000-0000-000000000000", []string{"10.0.0.0/8"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("set missing: %v", err)
	}
}

func TestTokenSeenIPs(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	hash := "$argon2id$v=19$m=19456,t=2,p=1$c2FsdA$aGFzaA"
	u, err := NewUserRepo(s).Create(ctx, "a@b.c", &hash)
	if err != nil {
		t.Fatal(err)
	}
	_, _, configID := mkConfig(t, s, "prod")
	repo := NewServiceTokenRepo(s)
	tok, err := repo.Create(ctx, "ci", []byte("hmac-of-raw-token-0123456789abcd"), u.ID, "config", configID, "read", nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// First sighting is new; repeat is not.
	isNew, err := repo.RecordSeenIP(ctx, tok.ID, "203.0.113.9")
	if err != nil || !isNew {
		t.Fatalf("first: isNew=%v err=%v", isNew, err)
	}
	isNew, err = repo.RecordSeenIP(ctx, tok.ID, "203.0.113.9")
	if err != nil || isNew {
		t.Fatalf("repeat: isNew=%v err=%v", isNew, err)
	}
	if _, err := repo.RecordSeenIP(ctx, tok.ID, "2001:db8::5"); err != nil {
		t.Fatal(err)
	}

	n, err := repo.CountRecentNewIPs(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("count = %d, want 2", n)
	}

	// Cascade: deleting the token removes its seen-IP rows (no orphan count).
	if _, err := s.pool.Exec(ctx, `DELETE FROM service_tokens WHERE id = $1::uuid`, tok.ID); err != nil {
		t.Fatal(err)
	}
	n, err = repo.CountRecentNewIPs(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("after cascade delete count = %d, want 0", n)
	}
}
