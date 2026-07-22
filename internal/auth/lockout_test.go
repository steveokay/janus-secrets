package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

// testLockoutPolicy is a fast, deterministic policy for auth tests: threshold 3,
// a long base window so the lock is comfortably active during the test, capped
// well above base so escalation is observable.
func testLockoutPolicy() LockoutPolicy {
	return LockoutPolicy{Enabled: true, Threshold: 3, Base: time.Hour, Max: 24 * time.Hour}
}

func TestLockoutWindowSchedule(t *testing.T) {
	p := LockoutPolicy{Enabled: true, Threshold: 5, Base: time.Minute, Max: time.Hour}
	cases := []struct {
		level int
		want  time.Duration
	}{
		{0, time.Minute}, // defensive: <1 treated as 1
		{1, time.Minute},
		{2, 5 * time.Minute},
		{3, 25 * time.Minute},
		{4, time.Hour}, // 125m capped to 1h
		{5, time.Hour},
		{10, time.Hour},
	}
	for _, c := range cases {
		if got := p.window(c.level); got != c.want {
			t.Errorf("window(%d)=%s want %s", c.level, got, c.want)
		}
	}
}

func TestLoginWrongPasswordLocksAtThreshold(t *testing.T) {
	svc, email, _ := newTestService(t)
	svc.SetLockoutPolicy(testLockoutPolicy())
	ctx := context.Background()

	// threshold-1 wrong passwords: still just invalid_credentials, no lock.
	for i := 0; i < svc.lockout.Threshold-1; i++ {
		if _, err := svc.Login(ctx, email, []byte("wrong"), ""); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	// The threshold-th wrong password trips the lock, but STILL returns the
	// byte-identical invalid_credentials (never reveals the lock on a wrong pw).
	if _, err := svc.Login(ctx, email, []byte("wrong"), ""); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("threshold attempt: %v", err)
	}
	if _, locked := AsAccountLocked(mustLoginErr(ctx, svc, email, "wrong")); locked {
		t.Fatal("wrong password must never surface AccountLockedError")
	}
	// The account is now locked (server-side).
	if !svc.IsEmailLocked(ctx, email) {
		t.Fatal("account should be locked after threshold failures")
	}
}

func TestLoginCorrectPasswordWhileLockedReveals(t *testing.T) {
	svc, email, password := newTestService(t)
	svc.SetLockoutPolicy(testLockoutPolicy())
	ctx := context.Background()

	for i := 0; i < svc.lockout.Threshold; i++ {
		_, _ = svc.Login(ctx, email, []byte("wrong"), "")
	}
	// Correct password while locked → AccountLockedError with a positive window.
	_, err := svc.Login(ctx, email, []byte(password), "")
	locked, ok := AsAccountLocked(err)
	if !ok {
		t.Fatalf("correct password while locked: want AccountLockedError, got %v", err)
	}
	if locked.RetryAfter <= 0 {
		t.Fatalf("RetryAfter must be positive, got %s", locked.RetryAfter)
	}
}

func TestLoginWrongPasswordWhileLockedDoesNotRevealOrExtend(t *testing.T) {
	svc, email, _ := newTestService(t)
	svc.SetLockoutPolicy(testLockoutPolicy())
	ctx := context.Background()
	uid, _ := svc.userByEmailForTest(ctx, email)

	for i := 0; i < svc.lockout.Threshold; i++ {
		_, _ = svc.Login(ctx, email, []byte("wrong"), "")
	}
	before, _ := svc.users.Get(ctx, uid)

	// Wrong password while locked → invalid_credentials (no reveal).
	if _, err := svc.Login(ctx, email, []byte("still wrong"), ""); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong pw while locked: %v", err)
	}
	after, _ := svc.users.Get(ctx, uid)
	// The lock window must NOT have been extended and the level must not grow.
	if !after.LockedUntil.Equal(*before.LockedUntil) {
		t.Fatalf("lock extended: before=%v after=%v", before.LockedUntil, after.LockedUntil)
	}
	if after.LockoutLevel != before.LockoutLevel {
		t.Fatalf("level changed while locked: %d -> %d", before.LockoutLevel, after.LockoutLevel)
	}
}

func TestLoginSuccessResetsCounter(t *testing.T) {
	svc, email, password := newTestService(t)
	svc.SetLockoutPolicy(testLockoutPolicy())
	ctx := context.Background()
	uid, _ := svc.userByEmailForTest(ctx, email)

	// Two failures (below threshold 3), then a success resets the counter.
	_, _ = svc.Login(ctx, email, []byte("wrong"), "")
	_, _ = svc.Login(ctx, email, []byte("wrong"), "")
	if _, err := svc.Login(ctx, email, []byte(password), ""); err != nil {
		t.Fatalf("success login: %v", err)
	}
	u, _ := svc.users.Get(ctx, uid)
	if u.FailedLoginCount != 0 || u.LockoutLevel != 0 || u.LockedUntil != nil {
		t.Fatalf("success did not reset: count=%d level=%d until=%v", u.FailedLoginCount, u.LockoutLevel, u.LockedUntil)
	}
}

func TestLoginCorrectPasswordWrongTOTPCounts(t *testing.T) {
	svc, email, password := newTestService(t)
	svc.SetLockoutPolicy(testLockoutPolicy())
	ctx := context.Background()
	uid, _ := svc.userByEmailForTest(ctx, email)
	enrollConfirm(t, svc, ctx, uid)

	// Correct password + wrong TOTP counts as a failure.
	if _, err := svc.Login(ctx, email, []byte(password), "000000"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong totp: %v", err)
	}
	u, _ := svc.users.Get(ctx, uid)
	if u.FailedLoginCount != 1 {
		t.Fatalf("wrong-totp failure not counted: count=%d", u.FailedLoginCount)
	}
}

func TestLoginTOTPRequiredDoesNotCount(t *testing.T) {
	svc, email, password := newTestService(t)
	svc.SetLockoutPolicy(testLockoutPolicy())
	ctx := context.Background()
	uid, _ := svc.userByEmailForTest(ctx, email)
	enrollConfirm(t, svc, ctx, uid)

	// Correct password, no code → totp_required challenge, NOT a failure.
	if _, err := svc.Login(ctx, email, []byte(password), ""); !errors.Is(err, ErrTOTPRequired) {
		t.Fatalf("want ErrTOTPRequired, got %v", err)
	}
	u, _ := svc.users.Get(ctx, uid)
	if u.FailedLoginCount != 0 {
		t.Fatalf("totp challenge wrongly counted: count=%d", u.FailedLoginCount)
	}
}

func TestLoginDisabledAndUnknownNeverTracked(t *testing.T) {
	svc, email, _ := newTestService(t)
	svc.SetLockoutPolicy(testLockoutPolicy())
	ctx := context.Background()
	uid, _ := svc.userByEmailForTest(ctx, email)

	// Disable the user, then hammer past threshold: no lock state should accrue.
	if err := svc.DisableUser(ctx, uid); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < svc.lockout.Threshold+2; i++ {
		if _, err := svc.Login(ctx, email, []byte("wrong"), ""); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("disabled attempt %d: %v", i, err)
		}
	}
	u, _ := svc.users.Get(ctx, uid)
	if u.FailedLoginCount != 0 || u.LockoutLevel != 0 || u.LockedUntil != nil {
		t.Fatalf("disabled user tracked: count=%d level=%d until=%v", u.FailedLoginCount, u.LockoutLevel, u.LockedUntil)
	}

	// Unknown user: no row, no panic, just invalid_credentials.
	for i := 0; i < svc.lockout.Threshold+2; i++ {
		if _, err := svc.Login(ctx, "ghost@nowhere.io", []byte("wrong"), ""); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("unknown attempt %d: %v", i, err)
		}
	}
}

func TestLoginPolicyDisabledSkipsLockout(t *testing.T) {
	svc, email, password := newTestService(t)
	// Explicitly disabled policy (also the zero value).
	svc.SetLockoutPolicy(LockoutPolicy{Enabled: false, Threshold: 3, Base: time.Hour, Max: time.Hour})
	ctx := context.Background()
	uid, _ := svc.userByEmailForTest(ctx, email)

	for i := 0; i < 10; i++ {
		_, _ = svc.Login(ctx, email, []byte("wrong"), "")
	}
	// No lock, no accrual — behaviour identical to pre-feature.
	if svc.IsEmailLocked(ctx, email) {
		t.Fatal("policy disabled but account locked")
	}
	u, _ := svc.users.Get(ctx, uid)
	if u.FailedLoginCount != 0 || u.LockoutLevel != 0 {
		t.Fatalf("policy disabled but state accrued: count=%d level=%d", u.FailedLoginCount, u.LockoutLevel)
	}
	// Correct password still works straight away.
	if _, err := svc.Login(ctx, email, []byte(password), ""); err != nil {
		t.Fatalf("login with policy disabled: %v", err)
	}
}

func TestAdminUnlockClearsLock(t *testing.T) {
	svc, email, password := newTestService(t)
	svc.SetLockoutPolicy(testLockoutPolicy())
	ctx := context.Background()
	uid, _ := svc.userByEmailForTest(ctx, email)

	for i := 0; i < svc.lockout.Threshold; i++ {
		_, _ = svc.Login(ctx, email, []byte("wrong"), "")
	}
	if !svc.IsEmailLocked(ctx, email) {
		t.Fatal("precondition: expected locked")
	}
	if err := svc.AdminUnlock(ctx, uid); err != nil {
		t.Fatal(err)
	}
	if svc.IsEmailLocked(ctx, email) {
		t.Fatal("still locked after admin unlock")
	}
	// Correct password now succeeds.
	if _, err := svc.Login(ctx, email, []byte(password), ""); err != nil {
		t.Fatalf("login after unlock: %v", err)
	}
}

func TestAdminUnlockUnknownUser(t *testing.T) {
	svc, _, _ := newTestService(t)
	svc.SetLockoutPolicy(testLockoutPolicy())
	if err := svc.AdminUnlock(context.Background(), "00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unlock missing user: %v", err)
	}
}

// mustLoginErr runs a login and returns only the error (helper for assertions).
func mustLoginErr(ctx context.Context, svc *Service, email, pw string) error {
	_, err := svc.Login(ctx, email, []byte(pw), "")
	return err
}
