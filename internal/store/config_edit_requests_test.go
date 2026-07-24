package store

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// mkPendingEditRequest inserts a pending config_edit_request for a fresh config
// and returns its id plus the config id.
func mkPendingEditRequest(t *testing.T, s *Store, cfgName, requesterEmail string) (reqID, configID string) {
	t.Helper()
	ctx := context.Background()
	_, _, configID = mkConfig(t, s, cfgName)
	requester := mkUser(t, requesterEmail)
	created, err := NewConfigEditRequestRepo(s).Create(ctx, &ConfigEditRequest{
		ConfigID:           configID,
		RequestedBy:        requester,
		ProposedCiphertext: []byte("ct"),
		WrappedDEK:         []byte("wd"),
		Nonce:              []byte("nn"),
		DEKKeyVersion:      1,
		ChangedKeys:        []string{"K"},
	})
	if err != nil {
		t.Fatalf("create edit request: %v", err)
	}
	return created.ID, configID
}

// TestConfigEditRequestClaimSingleWinner asserts the claim-before-commit CAS:
// exactly one of many concurrent ClaimForApply calls wins the pending ->
// applying transition; the losers get ErrNotFound. This is the store-level
// guarantee behind editreq.Approve refusing to double-commit.
func TestConfigEditRequestClaimSingleWinner(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	repo := NewConfigEditRequestRepo(s)

	id, _ := mkPendingEditRequest(t, s, "prod", "claimant@example.com")

	const n = 8
	var wg sync.WaitGroup
	wg.Add(n)
	wins := make([]bool, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			err := repo.ClaimForApply(ctx, id)
			if err == nil {
				wins[i] = true
			} else if !errors.Is(err, ErrNotFound) {
				t.Errorf("claim %d: unexpected err %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	won := 0
	for _, w := range wins {
		if w {
			won++
		}
	}
	if won != 1 {
		t.Fatalf("want exactly 1 claim winner, got %d", won)
	}

	got, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != "applying" {
		t.Fatalf("want status applying after claim, got %q", got.Status)
	}
}

// TestConfigEditRequestApplyingLifecycle exercises the applying state machine:
// claim (pending -> applying), MarkApplied requires 'applying' (not 'pending'),
// and RevertApplying returns a claimed request to pending for retry.
func TestConfigEditRequestApplyingLifecycle(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	repo := NewConfigEditRequestRepo(s)

	id, _ := mkPendingEditRequest(t, s, "prod", "lifecycle@example.com")
	approver := mkUser(t, "approver-lc@example.com")

	// MarkApplied before claiming must fail: the row is still 'pending'.
	if err := repo.MarkApplied(ctx, id, approver, 2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("MarkApplied before claim: want ErrNotFound, got %v", err)
	}

	// Claim, then revert -> back to pending, retriable.
	if err := repo.ClaimForApply(ctx, id); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := repo.RevertApplying(ctx, id); err != nil {
		t.Fatalf("revert: %v", err)
	}
	if got, err := repo.Get(ctx, id); err != nil || got.Status != "pending" {
		t.Fatalf("after revert: status=%q err=%v, want pending", got.Status, err)
	}

	// Re-claim and mark applied for real.
	if err := repo.ClaimForApply(ctx, id); err != nil {
		t.Fatalf("re-claim: %v", err)
	}
	if err := repo.MarkApplied(ctx, id, approver, 2); err != nil {
		t.Fatalf("MarkApplied after claim: %v", err)
	}
	final, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("final Get: %v", err)
	}
	if final.Status != "applied" {
		t.Fatalf("want applied, got %q", final.Status)
	}
	if final.AppliedVersion == nil || *final.AppliedVersion != 2 {
		t.Fatalf("want applied_version 2, got %v", final.AppliedVersion)
	}
	if final.ResolvedBy == nil || *final.ResolvedBy != approver {
		t.Fatalf("want resolved_by %s, got %v", approver, final.ResolvedBy)
	}

	// A second claim on the now-applied row must fail.
	if err := repo.ClaimForApply(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("claim on applied: want ErrNotFound, got %v", err)
	}
	// RevertApplying on a non-applying row is a no-op failure (ErrNotFound).
	if err := repo.RevertApplying(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revert on applied: want ErrNotFound, got %v", err)
	}
}
