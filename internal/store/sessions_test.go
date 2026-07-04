package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSessionRepo(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	hash := "$argon2id$v=19$m=19456,t=2,p=1$c2FsdA$aGFzaA"
	u, err := NewUserRepo(s).Create(ctx, "a@b.c", &hash)
	if err != nil {
		t.Fatal(err)
	}
	repo := NewSessionRepo(s)

	mac := []byte("hmac-of-cookie-value-0123456789ab")
	sess, err := repo.Create(ctx, u.ID, mac, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetByHMAC(ctx, mac)
	if err != nil || got.ID != sess.ID || got.UserID != u.ID {
		t.Fatalf("GetByHMAC: %+v err=%v", got, err)
	}

	// TouchLastSeen advances last_seen_at.
	before := got.LastSeenAt
	time.Sleep(20 * time.Millisecond)
	if err := repo.TouchLastSeen(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.GetByHMAC(ctx, mac)
	if !got.LastSeenAt.After(before) {
		t.Fatalf("last_seen not advanced: %v !> %v", got.LastSeenAt, before)
	}

	// Delete (logout).
	if err := repo.DeleteByHMAC(ctx, mac); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetByHMAC(ctx, mac); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
	// Deleting again is ErrNotFound.
	if err := repo.DeleteByHMAC(ctx, mac); !errors.Is(err, ErrNotFound) {
		t.Fatalf("double delete: %v", err)
	}

	// DeleteExpired removes only expired rows.
	if _, err := repo.Create(ctx, u.ID, []byte("expired-mac-000000000000000000"), time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	live, err := repo.Create(ctx, u.ID, []byte("live-mac-0000000000000000000000"), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteExpired(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetByHMAC(ctx, []byte("expired-mac-000000000000000000")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired survived sweep: %v", err)
	}
	if _, err := repo.GetByHMAC(ctx, live.TokenHMAC); err != nil {
		t.Fatalf("live swept: %v", err)
	}
}
