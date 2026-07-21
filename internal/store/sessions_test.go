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
	sess, err := repo.Create(ctx, u.ID, mac, time.Now().Add(time.Hour), "203.0.113.7:5522", "curl/8.0")
	if err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetByHMAC(ctx, mac)
	if err != nil || got.ID != sess.ID || got.UserID != u.ID {
		t.Fatalf("GetByHMAC: %+v err=%v", got, err)
	}
	if got.IP == nil || *got.IP != "203.0.113.7:5522" || got.UserAgent == nil || *got.UserAgent != "curl/8.0" {
		t.Fatalf("session metadata not persisted: ip=%v ua=%v", got.IP, got.UserAgent)
	}
	// Empty metadata persists as NULL, not "".
	if blank, err := repo.Create(ctx, u.ID, []byte("blank-meta-000000000000000000000"), time.Now().Add(time.Hour), "", ""); err != nil {
		t.Fatal(err)
	} else if blank.IP != nil || blank.UserAgent != nil {
		t.Fatalf("empty metadata should be NULL: ip=%v ua=%v", blank.IP, blank.UserAgent)
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
	if _, err := repo.Create(ctx, u.ID, []byte("expired-mac-000000000000000000"), time.Now().Add(-time.Minute), "", ""); err != nil {
		t.Fatal(err)
	}
	live, err := repo.Create(ctx, u.ID, []byte("live-mac-0000000000000000000000"), time.Now().Add(time.Hour), "", "")
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

func TestSessionRepoManagement(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	hash := "$argon2id$v=19$m=19456,t=2,p=1$c2FsdA$aGFzaA"
	ur := NewUserRepo(s)
	alice, err := ur.Create(ctx, "alice@b.c", &hash)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := ur.Create(ctx, "bob@b.c", &hash)
	if err != nil {
		t.Fatal(err)
	}
	repo := NewSessionRepo(s)

	mk := func(owner, mac string, exp time.Duration) *Session {
		sess, err := repo.Create(ctx, owner, []byte(mac), time.Now().Add(exp), "", "")
		if err != nil {
			t.Fatal(err)
		}
		return sess
	}
	a1 := mk(alice.ID, "alice-mac-1-000000000000000000000", time.Hour)
	a2 := mk(alice.ID, "alice-mac-2-000000000000000000000", time.Hour)
	_ = mk(alice.ID, "alice-expired-00000000000000000000", -time.Minute)
	b1 := mk(bob.ID, "bob-mac-1-00000000000000000000000", time.Hour)

	// ListByUser returns only alice's non-expired sessions.
	list, err := repo.ListByUser(ctx, alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("ListByUser: want 2 live sessions, got %d", len(list))
	}

	// DeleteForUser cannot remove another user's session.
	if err := repo.DeleteForUser(ctx, b1.ID, alice.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-user delete should be ErrNotFound, got %v", err)
	}
	if _, err := repo.GetByHMAC(ctx, b1.TokenHMAC); err != nil {
		t.Fatalf("bob's session wrongly affected: %v", err)
	}
	// DeleteForUser removes an owned session.
	if err := repo.DeleteForUser(ctx, a1.ID, alice.ID); err != nil {
		t.Fatal(err)
	}

	// DeleteOthersForUser keeps only the kept id (a2), and never touches bob.
	keep := a2.ID
	n, err := repo.DeleteOthersForUser(ctx, alice.ID, &keep)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 { // only the expired one remained besides a2 (a1 already gone)
		t.Fatalf("DeleteOthersForUser count: want 1, got %d", n)
	}
	remaining, _ := repo.ListByUser(ctx, alice.ID)
	if len(remaining) != 1 || remaining[0].ID != a2.ID {
		t.Fatalf("kept session wrong: %+v", remaining)
	}
	if _, err := repo.GetByHMAC(ctx, b1.TokenHMAC); err != nil {
		t.Fatalf("bob's session collaterally revoked: %v", err)
	}
}
