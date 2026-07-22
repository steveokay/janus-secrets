package store

import (
	"context"
	"sync"
	"testing"
	"time"
)

const lockoutTestHash = "$argon2id$v=19$m=19456,t=2,p=1$c2FsdA$aGFzaA"

// newLockoutUser creates a user and returns its id.
func newLockoutUser(t *testing.T, repo *UserRepo) string {
	t.Helper()
	h := lockoutTestHash
	u, err := repo.Create(context.Background(), "lock@example.com", &h)
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}

func TestRecordFailedLogin_IncrementThenLock(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	repo := NewUserRepo(s)
	id := newLockoutUser(t, repo)

	const threshold = 5
	window := time.Minute

	// The first threshold-1 failures accrue without locking.
	for i := 1; i < threshold; i++ {
		locked, until, err := repo.RecordFailedLogin(ctx, id, threshold, window)
		if err != nil {
			t.Fatalf("failure %d: %v", i, err)
		}
		if locked || until != nil {
			t.Fatalf("failure %d unexpectedly locked (until=%v)", i, until)
		}
		u, _ := repo.Get(ctx, id)
		if u.FailedLoginCount != i {
			t.Fatalf("failure %d: count=%d want %d", i, u.FailedLoginCount, i)
		}
		if u.LastFailedLoginAt == nil {
			t.Fatalf("failure %d: last_failed_login_at not stamped", i)
		}
	}

	// The threshold-th failure trips the lock.
	locked, until, err := repo.RecordFailedLogin(ctx, id, threshold, window)
	if err != nil {
		t.Fatal(err)
	}
	if !locked || until == nil {
		t.Fatalf("threshold failure did not lock: locked=%v until=%v", locked, until)
	}
	if time.Until(*until) <= 0 {
		t.Fatalf("locked_until is not in the future: %v", *until)
	}
	u, _ := repo.Get(ctx, id)
	if u.LockoutLevel != 1 {
		t.Fatalf("lockout_level=%d want 1", u.LockoutLevel)
	}
	if u.FailedLoginCount != 0 {
		t.Fatalf("failed_login_count=%d want 0 after lock", u.FailedLoginCount)
	}
	if u.LockedUntil == nil {
		t.Fatal("LockedUntil nil after lock")
	}
}

func TestRecordFailedLogin_Escalation(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	repo := NewUserRepo(s)
	id := newLockoutUser(t, repo)

	const threshold = 2

	// First lock: level 1, 1-minute window.
	repo.RecordFailedLogin(ctx, id, threshold, time.Minute)
	_, until1, err := repo.RecordFailedLogin(ctx, id, threshold, time.Minute)
	if err != nil || until1 == nil {
		t.Fatalf("first lock: until=%v err=%v", until1, err)
	}
	u, _ := repo.Get(ctx, id)
	if u.LockoutLevel != 1 {
		t.Fatalf("after first lock level=%d want 1", u.LockoutLevel)
	}

	// Second lock: level 2, a longer (5-minute) window per the caller's schedule.
	repo.RecordFailedLogin(ctx, id, threshold, 5*time.Minute)
	_, until2, err := repo.RecordFailedLogin(ctx, id, threshold, 5*time.Minute)
	if err != nil || until2 == nil {
		t.Fatalf("second lock: until=%v err=%v", until2, err)
	}
	u, _ = repo.Get(ctx, id)
	if u.LockoutLevel != 2 {
		t.Fatalf("after second lock level=%d want 2", u.LockoutLevel)
	}
	// The second window must be strictly larger than the first.
	if !until2.After(*until1) {
		t.Fatalf("escalation did not grow the window: until1=%v until2=%v", until1, until2)
	}
}

func TestResetLoginFailures(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	repo := NewUserRepo(s)
	id := newLockoutUser(t, repo)

	// Drive to a lock, then reset.
	repo.RecordFailedLogin(ctx, id, 2, time.Minute)
	repo.RecordFailedLogin(ctx, id, 2, time.Minute)
	if err := repo.ResetLoginFailures(ctx, id); err != nil {
		t.Fatal(err)
	}
	u, _ := repo.Get(ctx, id)
	if u.FailedLoginCount != 0 || u.LockoutLevel != 0 || u.LockedUntil != nil {
		t.Fatalf("reset left state: count=%d level=%d until=%v", u.FailedLoginCount, u.LockoutLevel, u.LockedUntil)
	}
}

func TestAdminUnlock(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	repo := NewUserRepo(s)
	id := newLockoutUser(t, repo)

	repo.RecordFailedLogin(ctx, id, 2, time.Hour)
	repo.RecordFailedLogin(ctx, id, 2, time.Hour)
	u, _ := repo.Get(ctx, id)
	if u.LockedUntil == nil {
		t.Fatal("precondition: expected locked")
	}
	if err := repo.AdminUnlock(ctx, id); err != nil {
		t.Fatal(err)
	}
	u, _ = repo.Get(ctx, id)
	if u.FailedLoginCount != 0 || u.LockoutLevel != 0 || u.LockedUntil != nil {
		t.Fatalf("admin unlock left state: count=%d level=%d until=%v", u.FailedLoginCount, u.LockoutLevel, u.LockedUntil)
	}
}

// TestLockedUntilAutoExpiry proves a past locked_until reads as unlocked from the
// perspective of the caller (locked_until <= now()). The store persists the raw
// timestamp; expiry is a read-time comparison the auth/API layer performs.
func TestLockedUntilAutoExpiry(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	repo := NewUserRepo(s)
	id := newLockoutUser(t, repo)

	// A negative window puts locked_until in the past.
	_, until, err := repo.RecordFailedLogin(ctx, id, 1, -time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	// just_locked requires locked_until > now(); a past timestamp does not lock.
	if until != nil {
		t.Fatalf("past window should not report an active lock: %v", until)
	}
	u, _ := repo.Get(ctx, id)
	if u.LockedUntil == nil {
		t.Fatal("locked_until should still be persisted")
	}
	if u.LockedUntil.After(time.Now()) {
		t.Fatalf("locked_until should be in the past: %v", u.LockedUntil)
	}
}

// TestRecordFailedLogin_Concurrent proves the atomic increment does not lose
// updates: N concurrent failures below the threshold leave count == N.
func TestRecordFailedLogin_Concurrent(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	repo := NewUserRepo(s)
	id := newLockoutUser(t, repo)

	const n = 20
	// Threshold above n so no lock trips and the counter simply accrues.
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := repo.RecordFailedLogin(ctx, id, n+1, time.Minute); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent failure: %v", err)
	}
	u, _ := repo.Get(ctx, id)
	if u.FailedLoginCount != n {
		t.Fatalf("concurrent count=%d want %d (lost/double update)", u.FailedLoginCount, n)
	}
}
