package store

import (
	"context"
	"errors"
	"testing"
)

// mkTOTPUser creates a user and returns its id.
func mkTOTPUser(t *testing.T, s *Store, email string) string {
	t.Helper()
	hash := "$argon2id$v=19$m=19456,t=2,p=1$c2FsdA$aGFzaA"
	u, err := NewUserRepo(s).Create(context.Background(), email, &hash)
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}

func TestTOTPRepo_UpsertGetActivate(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	repo := NewTOTPRepo(s)
	uid := mkTOTPUser(t, s, "totp@example.com")

	// GetTOTP on an absent row → ErrNotFound.
	if _, err := repo.GetTOTP(ctx, uid); !errors.Is(err, ErrNotFound) {
		t.Fatalf("absent GetTOTP: want ErrNotFound, got %v", err)
	}

	// Insert a fresh (pending) enrollment.
	sec1 := []byte("wrapped-secret-one-0123456789ab")
	if err := repo.Upsert(ctx, uid, sec1); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetTOTP(ctx, uid)
	if err != nil {
		t.Fatal(err)
	}
	if got.UserID != uid || string(got.WrappedSecret) != string(sec1) {
		t.Fatalf("row mismatch: %+v", got)
	}
	if got.ActivatedAt != nil {
		t.Fatalf("fresh enrollment must be pending (activated_at NULL), got %v", got.ActivatedAt)
	}

	// Activate flips the pending row.
	if err := repo.Activate(ctx, uid); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.GetTOTP(ctx, uid)
	if got.ActivatedAt == nil {
		t.Fatal("Activate did not set activated_at")
	}
	firstActivated := *got.ActivatedAt

	// Activating an already-active row affects zero rows → ErrNotFound.
	if err := repo.Activate(ctx, uid); !errors.Is(err, ErrNotFound) {
		t.Fatalf("re-activate active row: want ErrNotFound, got %v", err)
	}

	// Upsert overwrite resets activated_at to NULL and replaces the secret.
	sec2 := []byte("wrapped-secret-two-0123456789cd")
	if err := repo.Upsert(ctx, uid, sec2); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.GetTOTP(ctx, uid)
	if got.ActivatedAt != nil {
		t.Fatalf("upsert must reset activated_at to NULL, got %v", got.ActivatedAt)
	}
	if string(got.WrappedSecret) != string(sec2) {
		t.Fatalf("upsert did not replace secret: %q", got.WrappedSecret)
	}
	_ = firstActivated

	// Activate on a missing user → ErrNotFound.
	if err := repo.Activate(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("activate missing user: %v", err)
	}
}

func TestTOTPRepo_DeleteCascadesRecoveryCodes(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	repo := NewTOTPRepo(s)
	uid := mkTOTPUser(t, s, "del@example.com")

	if err := repo.Upsert(ctx, uid, []byte("wrapped-secret-000000000000000")); err != nil {
		t.Fatal(err)
	}
	hmacs := [][]byte{[]byte("rc-hmac-1-00000000000000000000000"), []byte("rc-hmac-2-00000000000000000000000")}
	if err := repo.ReplaceRecoveryCodes(ctx, uid, hmacs); err != nil {
		t.Fatal(err)
	}
	if n, _ := repo.CountUnusedRecoveryCodes(ctx, uid); n != 2 {
		t.Fatalf("want 2 codes before delete, got %d", n)
	}

	if err := repo.DeleteTOTP(ctx, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetTOTP(ctx, uid); !errors.Is(err, ErrNotFound) {
		t.Fatalf("row still present after delete: %v", err)
	}
	// Recovery codes cascaded away.
	if n, err := repo.CountUnusedRecoveryCodes(ctx, uid); err != nil || n != 0 {
		t.Fatalf("recovery codes not cascaded: n=%d err=%v", n, err)
	}

	// Deleting a missing row → ErrNotFound.
	if err := repo.DeleteTOTP(ctx, uid); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing: %v", err)
	}
}

func TestTOTPRepo_ReplaceRecoveryCodesAtomicReplace(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	repo := NewTOTPRepo(s)
	uid := mkTOTPUser(t, s, "rc@example.com")
	if err := repo.Upsert(ctx, uid, []byte("wrapped-secret-000000000000000")); err != nil {
		t.Fatal(err)
	}

	first := [][]byte{[]byte("first-a-000000000000000000000000"), []byte("first-b-000000000000000000000000")}
	if err := repo.ReplaceRecoveryCodes(ctx, uid, first); err != nil {
		t.Fatal(err)
	}
	if n, _ := repo.CountUnusedRecoveryCodes(ctx, uid); n != 2 {
		t.Fatalf("first set count = %d, want 2", n)
	}

	// Replacing swaps the whole set: old codes gone, new ones present.
	second := [][]byte{[]byte("second-a-00000000000000000000000"), []byte("second-b-00000000000000000000000"), []byte("second-c-00000000000000000000000")}
	if err := repo.ReplaceRecoveryCodes(ctx, uid, second); err != nil {
		t.Fatal(err)
	}
	if n, _ := repo.CountUnusedRecoveryCodes(ctx, uid); n != 3 {
		t.Fatalf("second set count = %d, want 3", n)
	}
	// An old code is no longer consumable.
	if ok, err := repo.ConsumeRecoveryCode(ctx, uid, first[0]); err != nil || ok {
		t.Fatalf("stale code consumable after replace: ok=%v err=%v", ok, err)
	}
	// A new code is consumable.
	if ok, err := repo.ConsumeRecoveryCode(ctx, uid, second[0]); err != nil || !ok {
		t.Fatalf("new code not consumable: ok=%v err=%v", ok, err)
	}

	// Replacing with an empty set clears everything.
	if err := repo.ReplaceRecoveryCodes(ctx, uid, nil); err != nil {
		t.Fatal(err)
	}
	if n, _ := repo.CountUnusedRecoveryCodes(ctx, uid); n != 0 {
		t.Fatalf("empty replace did not clear: %d", n)
	}
}

func TestTOTPRepo_ConsumeRecoveryCodeSingleUse(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	repo := NewTOTPRepo(s)
	uid := mkTOTPUser(t, s, "consume@example.com")
	if err := repo.Upsert(ctx, uid, []byte("wrapped-secret-000000000000000")); err != nil {
		t.Fatal(err)
	}
	code := []byte("code-hmac-single-0000000000000000")
	if err := repo.ReplaceRecoveryCodes(ctx, uid, [][]byte{code}); err != nil {
		t.Fatal(err)
	}

	// First consume succeeds.
	if ok, err := repo.ConsumeRecoveryCode(ctx, uid, code); err != nil || !ok {
		t.Fatalf("first consume: ok=%v err=%v", ok, err)
	}
	if n, _ := repo.CountUnusedRecoveryCodes(ctx, uid); n != 0 {
		t.Fatalf("count after consume = %d, want 0", n)
	}
	// Second consume of the same (now spent) code returns false, no error.
	if ok, err := repo.ConsumeRecoveryCode(ctx, uid, code); err != nil || ok {
		t.Fatalf("double consume: ok=%v err=%v (want false,nil)", ok, err)
	}
	// Unknown code returns false, no error.
	if ok, err := repo.ConsumeRecoveryCode(ctx, uid, []byte("unknown-hmac-000000000000000000")); err != nil || ok {
		t.Fatalf("unknown consume: ok=%v err=%v (want false,nil)", ok, err)
	}

	// A code belonging to another user is not consumable across users.
	other := mkTOTPUser(t, s, "other@example.com")
	if err := repo.Upsert(ctx, other, []byte("wrapped-secret-other-00000000000")); err != nil {
		t.Fatal(err)
	}
	shared := []byte("cross-user-hmac-00000000000000000")
	if err := repo.ReplaceRecoveryCodes(ctx, other, [][]byte{shared}); err != nil {
		t.Fatal(err)
	}
	// uid does not own `shared`, so consuming it under uid fails and leaves
	// other's copy intact.
	if ok, err := repo.ConsumeRecoveryCode(ctx, uid, shared); err != nil || ok {
		t.Fatalf("cross-user consume under wrong user: ok=%v err=%v", ok, err)
	}
	if n, _ := repo.CountUnusedRecoveryCodes(ctx, other); n != 1 {
		t.Fatalf("other user's code was affected: %d", n)
	}
}
