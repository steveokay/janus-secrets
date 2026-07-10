package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

// newSyncTarget builds a minimal valid SyncTarget for the given
// project/config, with a fresh id and default encrypted-blob placeholders.
func newSyncTarget(t *testing.T, s *Store, projectID, configID string, addr []byte) *SyncTarget {
	t.Helper()
	ctx := context.Background()
	id, err := s.NewID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if addr == nil {
		addr = []byte(`{"owner":"o","repo":"r"}`)
	}
	return &SyncTarget{
		ID:                 id,
		ProjectID:          projectID,
		ConfigID:           configID,
		Provider:           "github",
		Prune:              true,
		IntervalSeconds:    3600,
		NextSyncAt:         time.Now().Add(time.Hour).UTC().Truncate(time.Second),
		CredsCT:            []byte("ct-v1"),
		CredsNonce:         []byte("nonce-v1"),
		CredsWrappedDEK:    []byte("wrapped-v1"),
		CredsDEKKEKVersion: 1,
		Addr:               addr,
		CreatedBy:          "user:tester",
	}
}

func TestSyncTargetRepoLifecycle(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	r := NewSyncTargetRepo(s)

	projectID, _, configID := mkConfig(t, s, "prod")

	// Create -> Get round trip.
	in := newSyncTarget(t, s, projectID, configID, []byte(`{"owner":"o","repo":"r"}`))
	got, err := r.Create(ctx, in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.ID != in.ID || got.ProjectID != projectID || got.ConfigID != configID {
		t.Fatalf("ids mismatch: %+v", got)
	}
	if got.Provider != "github" || !got.Prune || got.IntervalSeconds != 3600 {
		t.Fatalf("fields mismatch: %+v", got)
	}
	if got.Status != "active" {
		t.Fatalf("want default status active, got %q", got.Status)
	}
	if got.FailureCount != 0 {
		t.Fatalf("want failure_count 0, got %d", got.FailureCount)
	}
	if got.LastError != nil || got.LastSyncedAt != nil || got.SyncedConfigVersion != nil {
		t.Fatalf("want nil last_* fields, got err=%v synced=%v ver=%v", got.LastError, got.LastSyncedAt, got.SyncedConfigVersion)
	}
	if got.SyncedFingerprint != nil {
		t.Fatalf("want nil synced_fingerprint, got %+v", got.SyncedFingerprint)
	}
	if string(got.CredsCT) != "ct-v1" || string(got.CredsNonce) != "nonce-v1" || string(got.CredsWrappedDEK) != "wrapped-v1" {
		t.Fatalf("creds blob mismatch: %+v", got)
	}
	if got.CredsDEKKEKVersion != 1 {
		t.Fatalf("want kek version 1, got %d", got.CredsDEKKEKVersion)
	}
	if got.CreatedBy != "user:tester" {
		t.Fatalf("want created_by preserved, got %q", got.CreatedBy)
	}
	// ManagedKeys comes back empty non-nil (column default '{}').
	if got.ManagedKeys == nil || len(got.ManagedKeys) != 0 {
		t.Fatalf("want empty non-nil managed_keys, got %#v", got.ManagedKeys)
	}
	// Addr round-trips (be robust to jsonb whitespace normalization: compare
	// by unmarshaling to a map rather than raw bytes).
	var gotAddr, wantAddr map[string]any
	if err := json.Unmarshal(got.Addr, &gotAddr); err != nil {
		t.Fatalf("unmarshal got.Addr: %v", err)
	}
	if err := json.Unmarshal([]byte(`{"owner":"o","repo":"r"}`), &wantAddr); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotAddr, wantAddr) {
		t.Fatalf("addr mismatch: got %v want %v", gotAddr, wantAddr)
	}

	// Duplicate (config_id, provider, addr) -> ErrAlreadyExists.
	dup := newSyncTarget(t, s, projectID, configID, []byte(`{"owner":"o","repo":"r"}`))
	if _, err := r.Create(ctx, dup); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("dup: want ErrAlreadyExists, got %v", err)
	}

	// A second, distinct target (different addr) for ListByProject ordering (newest first).
	second := newSyncTarget(t, s, projectID, configID, []byte(`{"owner":"o","repo":"other"}`))
	if _, err := r.Create(ctx, second); err != nil {
		t.Fatalf("Create second: %v", err)
	}

	list, err := r.ListByProject(ctx, projectID)
	if err != nil {
		t.Fatalf("ListByProject: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 targets, got %d", len(list))
	}
	if list[0].ID != second.ID || list[1].ID != got.ID {
		t.Fatalf("want newest first (second, got), got ids (%s, %s)", list[0].ID, list[1].ID)
	}

	// Update: interval + prune + status.
	newInterval := int64(7200)
	newPrune := false
	newStatus := "paused"
	if err := r.Update(ctx, got.ID, &newInterval, &newPrune, &newStatus, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("Update: %v", err)
	}
	afterUpdate, err := r.Get(ctx, got.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterUpdate.IntervalSeconds != 7200 || afterUpdate.Prune || afterUpdate.Status != "paused" {
		t.Fatalf("after update: %+v", afterUpdate)
	}
	// nil creds/addr args are a no-op — unchanged.
	if string(afterUpdate.CredsCT) != "ct-v1" || string(afterUpdate.CredsNonce) != "nonce-v1" ||
		string(afterUpdate.CredsWrappedDEK) != "wrapped-v1" || afterUpdate.CredsDEKKEKVersion != 1 {
		t.Fatalf("creds blob should be unchanged by nil-creds update: %+v", afterUpdate)
	}
	var afterAddr map[string]any
	if err := json.Unmarshal(afterUpdate.Addr, &afterAddr); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterAddr, gotAddr) {
		t.Fatalf("addr should be unchanged by nil-addr update: got %v want %v", afterAddr, gotAddr)
	}

	// Creds re-seal: pass new bytes, verify they changed.
	newCT, newNonce, newWrapped := []byte("ct-v2"), []byte("nonce-v2"), []byte("wrapped-v2")
	newKEKVer := 2
	if err := r.Update(ctx, got.ID, nil, nil, nil, newCT, newNonce, newWrapped, &newKEKVer, nil); err != nil {
		t.Fatalf("Update creds: %v", err)
	}
	afterCreds, err := r.Get(ctx, got.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterCreds.CredsCT) != "ct-v2" || string(afterCreds.CredsNonce) != "nonce-v2" ||
		string(afterCreds.CredsWrappedDEK) != "wrapped-v2" || afterCreds.CredsDEKKEKVersion != 2 {
		t.Fatalf("creds blob should have been re-sealed: %+v", afterCreds)
	}
	// interval/prune/status unaffected by the creds-only update.
	if afterCreds.IntervalSeconds != 7200 || afterCreds.Prune || afterCreds.Status != "paused" {
		t.Fatalf("non-creds fields should be unaffected: %+v", afterCreds)
	}

	// Restore to active for subsequent steps.
	activeStatus := "active"
	if err := r.Update(ctx, got.ID, nil, nil, &activeStatus, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}

	// ClaimDue: a future next_sync_at row is NOT selected.
	due, err := r.ClaimDue(ctx, time.Now(), 10)
	if err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	for _, d := range due {
		if d.ID == got.ID {
			t.Fatalf("future next_sync_at target should not be due: %+v", d)
		}
	}

	// Make it past-due and re-check it IS selected.
	pastNext := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)
	if err := r.PrepareSyncNow(ctx, got.ID, pastNext); err != nil {
		t.Fatalf("PrepareSyncNow (seed past-due): %v", err)
	}
	due, err = r.ClaimDue(ctx, time.Now(), 10)
	if err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	foundDue := false
	for _, d := range due {
		if d.ID == got.ID {
			foundDue = true
		}
	}
	if !foundDue {
		t.Fatalf("past-due active target should be selected by ClaimDue")
	}

	// A status='paused' past-due row is NOT selected.
	if err := r.Update(ctx, second.ID, nil, nil, &newStatus /* "paused" */, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.PrepareSyncNow(ctx, second.ID, pastNext); err != nil {
		t.Fatal(err)
	}
	// PrepareSyncNow does not reactivate a 'paused' target (only 'failed').
	pausedCheck, err := r.Get(ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pausedCheck.Status != "paused" {
		t.Fatalf("want paused target to remain paused, got %q", pausedCheck.Status)
	}
	due, err = r.ClaimDue(ctx, time.Now(), 10)
	if err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	for _, d := range due {
		if d.ID == second.ID {
			t.Fatalf("paused past-due target should not be selected by ClaimDue")
		}
	}

	// MarkSynced: managed_keys+fingerprint+synced_config_version set, failure_count reset, status active.
	next := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second)
	managedKeys := []string{"DB_PASSWORD", "API_KEY"}
	fingerprint := []byte{0xde, 0xad, 0xbe, 0xef}
	if err := r.MarkSynced(ctx, got.ID, managedKeys, fingerprint, 5, next); err != nil {
		t.Fatalf("MarkSynced: %v", err)
	}
	synced, err := r.Get(ctx, got.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(synced.ManagedKeys, managedKeys) {
		t.Fatalf("want managed_keys %v, got %v", managedKeys, synced.ManagedKeys)
	}
	if synced.SyncedFingerprint == nil || !reflect.DeepEqual(synced.SyncedFingerprint, fingerprint) {
		t.Fatalf("want synced_fingerprint %v, got %v", fingerprint, synced.SyncedFingerprint)
	}
	if synced.SyncedConfigVersion == nil || *synced.SyncedConfigVersion != 5 {
		t.Fatalf("want synced_config_version 5, got %v", synced.SyncedConfigVersion)
	}
	if synced.FailureCount != 0 {
		t.Fatalf("want failure_count reset to 0, got %d", synced.FailureCount)
	}
	if synced.Status != "active" {
		t.Fatalf("want status active after MarkSynced, got %q", synced.Status)
	}
	if synced.LastError != nil {
		t.Fatalf("want last_error nil after MarkSynced, got %v", *synced.LastError)
	}
	if synced.LastSyncedAt == nil {
		t.Fatalf("want last_synced_at set after MarkSynced")
	}
	if !synced.NextSyncAt.Equal(next) {
		t.Fatalf("want next_sync_at %v, got %v", next, synced.NextSyncAt)
	}

	// MarkFailure increments failure_count; flips to 'failed' at threshold.
	const threshold = 3
	retryAt := time.Now().Add(time.Minute).UTC().Truncate(time.Second)
	for i := 1; i < threshold; i++ {
		if err := r.MarkFailure(ctx, got.ID, "sanitized failure", retryAt, threshold); err != nil {
			t.Fatalf("MarkFailure #%d: %v", i, err)
		}
		p, err := r.Get(ctx, got.ID)
		if err != nil {
			t.Fatal(err)
		}
		if p.FailureCount != i {
			t.Fatalf("after failure #%d: want failure_count %d, got %d", i, i, p.FailureCount)
		}
		if p.Status != "active" {
			t.Fatalf("after failure #%d (below threshold): want status active, got %q", i, p.Status)
		}
	}
	// One more failure crosses the threshold.
	if err := r.MarkFailure(ctx, got.ID, "sanitized failure", retryAt, threshold); err != nil {
		t.Fatalf("MarkFailure (threshold-crossing): %v", err)
	}
	failed, err := r.Get(ctx, got.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.FailureCount != threshold {
		t.Fatalf("want failure_count %d, got %d", threshold, failed.FailureCount)
	}
	if failed.Status != "failed" {
		t.Fatalf("want status failed at threshold, got %q", failed.Status)
	}
	if failed.LastError == nil || *failed.LastError != "sanitized failure" {
		t.Fatalf("want last_error set, got %v", failed.LastError)
	}

	// PrepareSyncNow on a 'failed' target reactivates it and resets counters.
	reactivateAt := time.Now().Add(-time.Second).UTC().Truncate(time.Second)
	if err := r.PrepareSyncNow(ctx, got.ID, reactivateAt); err != nil {
		t.Fatalf("PrepareSyncNow (reactivate): %v", err)
	}
	reactivated, err := r.Get(ctx, got.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reactivated.Status != "active" {
		t.Fatalf("want status active after PrepareSyncNow on failed target, got %q", reactivated.Status)
	}
	if reactivated.FailureCount != 0 {
		t.Fatalf("want failure_count reset to 0, got %d", reactivated.FailureCount)
	}
	if reactivated.LastError != nil {
		t.Fatalf("want last_error cleared, got %v", *reactivated.LastError)
	}
	if !reactivated.NextSyncAt.Equal(reactivateAt) {
		t.Fatalf("want next_sync_at %v, got %v", reactivateAt, reactivated.NextSyncAt)
	}

	// Delete then Get -> ErrNotFound.
	if err := r.Delete(ctx, got.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := r.Get(ctx, got.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: want ErrNotFound, got %v", err)
	}
	if err := r.Delete(ctx, got.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("double delete: want ErrNotFound, got %v", err)
	}
}
