package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// newPolicy builds a minimal valid RotationPolicy for the given project/config,
// with a fresh id and default encrypted-blob placeholders.
func newPolicy(t *testing.T, s *Store, projectID, configID, key string) *RotationPolicy {
	t.Helper()
	ctx := context.Background()
	id, err := s.NewID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return &RotationPolicy{
		ID:                  id,
		ProjectID:           projectID,
		ConfigID:            configID,
		SecretKey:           key,
		Type:                "postgres",
		IntervalSeconds:     3600,
		NextRotationAt:      time.Now().Add(time.Hour).UTC().Truncate(time.Second),
		ConfigCT:            []byte("ct-v1"),
		ConfigNonce:         []byte("nonce-v1"),
		ConfigWrappedDEK:    []byte("wrapped-v1"),
		ConfigDEKKEKVersion: 1,
		CreatedBy:           "user:tester",
	}
}

func TestRotationRepoLifecycle(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	r := NewRotationRepo(s)

	projectID, _, configID := mkConfig(t, s, "prod")

	// Create -> Get round trip.
	in := newPolicy(t, s, projectID, configID, "DB_PASSWORD")
	got, err := r.Create(ctx, in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.ID != in.ID || got.ProjectID != projectID || got.ConfigID != configID {
		t.Fatalf("ids mismatch: %+v", got)
	}
	if got.SecretKey != "DB_PASSWORD" || got.Type != "postgres" || got.IntervalSeconds != 3600 {
		t.Fatalf("fields mismatch: %+v", got)
	}
	if got.Status != "active" {
		t.Fatalf("want default status active, got %q", got.Status)
	}
	if got.FailureCount != 0 {
		t.Fatalf("want failure_count 0, got %d", got.FailureCount)
	}
	if got.LastError != nil || got.LastRotatedAt != nil || got.LastConfigVersion != nil {
		t.Fatalf("want nil last_* fields, got err=%v rotated=%v ver=%v", got.LastError, got.LastRotatedAt, got.LastConfigVersion)
	}
	if got.PendingCT != nil || got.PendingNonce != nil || got.PendingWrappedDEK != nil || got.PendingState != nil {
		t.Fatalf("want nil pending_* fields, got %+v", got)
	}
	if string(got.ConfigCT) != "ct-v1" || string(got.ConfigNonce) != "nonce-v1" || string(got.ConfigWrappedDEK) != "wrapped-v1" {
		t.Fatalf("config blob mismatch: %+v", got)
	}
	if got.ConfigDEKKEKVersion != 1 {
		t.Fatalf("want kek version 1, got %d", got.ConfigDEKKEKVersion)
	}
	if got.CreatedBy != "user:tester" {
		t.Fatalf("want created_by preserved, got %q", got.CreatedBy)
	}

	// GetByConfigKey resolves the same row.
	byKey, err := r.GetByConfigKey(ctx, configID, "DB_PASSWORD")
	if err != nil {
		t.Fatalf("GetByConfigKey: %v", err)
	}
	if byKey.ID != got.ID {
		t.Fatalf("GetByConfigKey id mismatch: %+v", byKey)
	}

	// Duplicate (config_id, secret_key) -> ErrAlreadyExists.
	dup := newPolicy(t, s, projectID, configID, "DB_PASSWORD")
	if _, err := r.Create(ctx, dup); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("dup: want ErrAlreadyExists, got %v", err)
	}

	// A second, distinct policy for ListByProject ordering (newest first).
	second := newPolicy(t, s, projectID, configID, "API_KEY")
	if _, err := r.Create(ctx, second); err != nil {
		t.Fatalf("Create second: %v", err)
	}

	list, err := r.ListByProject(ctx, projectID)
	if err != nil {
		t.Fatalf("ListByProject: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 policies, got %d", len(list))
	}
	if list[0].SecretKey != "API_KEY" || list[1].SecretKey != "DB_PASSWORD" {
		t.Fatalf("want newest first (API_KEY, DB_PASSWORD), got (%s, %s)", list[0].SecretKey, list[1].SecretKey)
	}

	// Update: interval + status.
	newInterval := int64(7200)
	newStatus := "paused"
	if err := r.Update(ctx, got.ID, &newInterval, &newStatus, nil, nil, nil, nil); err != nil {
		t.Fatalf("Update: %v", err)
	}
	afterUpdate, err := r.Get(ctx, got.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterUpdate.IntervalSeconds != 7200 || afterUpdate.Status != "paused" {
		t.Fatalf("after update: %+v", afterUpdate)
	}
	// nil config blob args are a no-op — blob unchanged.
	if string(afterUpdate.ConfigCT) != "ct-v1" || string(afterUpdate.ConfigNonce) != "nonce-v1" ||
		string(afterUpdate.ConfigWrappedDEK) != "wrapped-v1" || afterUpdate.ConfigDEKKEKVersion != 1 {
		t.Fatalf("config blob should be unchanged by nil-config update: %+v", afterUpdate)
	}
	// Restore to active for subsequent steps.
	activeStatus := "active"
	if err := r.Update(ctx, got.ID, nil, &activeStatus, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}

	// SetPending then Get shows PendingState == "applying" and pending bytes set.
	if err := r.SetPending(ctx, got.ID, []byte("pending-ct"), []byte("pending-nonce"), []byte("pending-wrapped")); err != nil {
		t.Fatalf("SetPending: %v", err)
	}
	pending, err := r.Get(ctx, got.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pending.PendingState == nil || *pending.PendingState != "applying" {
		t.Fatalf("want pending_state applying, got %v", pending.PendingState)
	}
	if string(pending.PendingCT) != "pending-ct" || string(pending.PendingNonce) != "pending-nonce" ||
		string(pending.PendingWrappedDEK) != "pending-wrapped" {
		t.Fatalf("pending bytes mismatch: %+v", pending)
	}

	// MarkRotated clears pending, resets failure state, sets version/next, status active.
	next := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second)
	if err := r.MarkRotated(ctx, got.ID, 5, next, time.Now(), 0); err != nil {
		t.Fatalf("MarkRotated: %v", err)
	}
	rotated, err := r.Get(ctx, got.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.PendingCT != nil || rotated.PendingNonce != nil || rotated.PendingWrappedDEK != nil || rotated.PendingState != nil {
		t.Fatalf("want pending cleared after MarkRotated: %+v", rotated)
	}
	if rotated.FailureCount != 0 {
		t.Fatalf("want failure_count reset to 0, got %d", rotated.FailureCount)
	}
	if rotated.Status != "active" {
		t.Fatalf("want status active after MarkRotated, got %q", rotated.Status)
	}
	if rotated.LastConfigVersion == nil || *rotated.LastConfigVersion != 5 {
		t.Fatalf("want last_config_version 5, got %v", rotated.LastConfigVersion)
	}
	if !rotated.NextRotationAt.Equal(next) {
		t.Fatalf("want next_rotation_at %v, got %v", next, rotated.NextRotationAt)
	}
	if rotated.LastError != nil {
		t.Fatalf("want last_error nil after MarkRotated, got %v", *rotated.LastError)
	}

	// MarkFailure increments failure_count; flips to 'failed' at threshold.
	const threshold = 3
	retryAt := time.Now().Add(time.Minute).UTC().Truncate(time.Second)
	for i := 1; i < threshold; i++ {
		if err := r.MarkFailure(ctx, got.ID, "sanitized failure", retryAt, threshold, time.Now(), i); err != nil {
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
	if err := r.MarkFailure(ctx, got.ID, "sanitized failure", retryAt, threshold, time.Now(), threshold); err != nil {
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
