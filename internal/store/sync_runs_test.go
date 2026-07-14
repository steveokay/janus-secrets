package store

import (
	"context"
	"testing"
	"time"
)

func TestSyncRunsInsertListPrune(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	r := NewSyncTargetRepo(s)

	projectID, _, configID := mkConfig(t, s, "prod")

	// Seed one target to hang runs off of.
	tgt, err := r.Create(ctx, newSyncTarget(t, s, projectID, configID, nil))
	if err != nil {
		t.Fatalf("Create target: %v", err)
	}

	// Insert 105 runs; config_version increases with each so newest == highest.
	const total = 105
	base := time.Now().UTC().Truncate(time.Second)
	for i := 0; i < total; i++ {
		ver := i + 1
		if err := r.InsertRun(ctx, SyncRunInput{
			TargetID:      tgt.ID,
			StartedAt:     base.Add(time.Duration(i) * time.Second),
			EndedAt:       base.Add(time.Duration(i)*time.Second + time.Second),
			Status:        "success",
			ConfigVersion: &ver,
			KeysCount:     3,
			AttemptNum:    0,
		}); err != nil {
			t.Fatalf("InsertRun #%d: %v", i, err)
		}
	}

	// Total rows for the target capped at RunHistoryCap (100).
	var count int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM sync_runs WHERE target_id = $1::uuid`, tgt.ID).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != RunHistoryCap {
		t.Fatalf("want %d rows after cap, got %d", RunHistoryCap, count)
	}

	// First page: newest-first, 50 rows, config_version descending.
	page1, err := r.ListRuns(ctx, tgt.ID, 0, 50)
	if err != nil {
		t.Fatalf("ListRuns page1: %v", err)
	}
	if len(page1) != 50 {
		t.Fatalf("want 50 runs, got %d", len(page1))
	}
	// The very newest run is config_version=total (105); pruning removed the
	// oldest, so newest survives.
	if page1[0].ConfigVersion == nil || *page1[0].ConfigVersion != total {
		t.Fatalf("want newest config_version %d first, got %v", total, page1[0].ConfigVersion)
	}
	if page1[0].KeysCount != 3 {
		t.Fatalf("want keys_count 3, got %d", page1[0].KeysCount)
	}
	for i := 1; i < len(page1); i++ {
		if page1[i].ID >= page1[i-1].ID {
			t.Fatalf("not newest-first by id: page1[%d].ID=%d >= page1[%d].ID=%d",
				i, page1[i].ID, i-1, page1[i-1].ID)
		}
		if page1[i].ConfigVersion == nil || *page1[i].ConfigVersion >= *page1[i-1].ConfigVersion {
			t.Fatalf("config_version not descending at %d", i)
		}
	}

	// Keyset paging: cursor at the last id of page1 returns strictly older rows.
	cursor := page1[49].ID
	page2, err := r.ListRuns(ctx, tgt.ID, cursor, 50)
	if err != nil {
		t.Fatalf("ListRuns page2: %v", err)
	}
	if len(page2) == 0 {
		t.Fatalf("want older rows on page2, got none")
	}
	for _, run := range page2 {
		if run.ID >= cursor {
			t.Fatalf("page2 row id %d not < cursor %d", run.ID, cursor)
		}
	}
}

// TestSyncRunsPruneTargetIsolation locks in that the per-target prune only
// deletes the target's own rows. A regression here (prune losing its target_id
// scope) is a cross-target data-loss class the suite must catch.
func TestSyncRunsPruneTargetIsolation(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	r := NewSyncTargetRepo(s)

	projectID, _, configID := mkConfig(t, s, "prod")

	// Two targets on the same config, distinct destinations (the unique
	// constraint is on (config_id, provider, addr)).
	tgtA, err := r.Create(ctx, newSyncTarget(t, s, projectID, configID, []byte(`{"owner":"o","repo":"a"}`)))
	if err != nil {
		t.Fatalf("Create target A: %v", err)
	}
	tgtB, err := r.Create(ctx, newSyncTarget(t, s, projectID, configID, []byte(`{"owner":"o","repo":"b"}`)))
	if err != nil {
		t.Fatalf("Create target B: %v", err)
	}

	base := time.Now().UTC().Truncate(time.Second)
	insert := func(targetID string, n int) {
		t.Helper()
		for i := range n {
			if err := r.InsertRun(ctx, SyncRunInput{
				TargetID:   targetID,
				StartedAt:  base.Add(time.Duration(i) * time.Second),
				EndedAt:    base.Add(time.Duration(i)*time.Second + time.Second),
				Status:     "success",
				KeysCount:  3,
				AttemptNum: 0,
			}); err != nil {
				t.Fatalf("InsertRun target=%s #%d: %v", targetID, i, err)
			}
		}
	}

	// B gets 3 runs (well under the cap); then A's 105 inserts trigger A's prune.
	insert(tgtB.ID, 3)
	insert(tgtA.ID, 105)

	countFor := func(targetID string) int {
		t.Helper()
		var c int
		if err := s.pool.QueryRow(ctx,
			`SELECT count(*) FROM sync_runs WHERE target_id = $1::uuid`, targetID).Scan(&c); err != nil {
			t.Fatalf("count target=%s: %v", targetID, err)
		}
		return c
	}

	// Target B's rows must be untouched by target A's prune.
	if got := countFor(tgtB.ID); got != 3 {
		t.Fatalf("target B: want 3 rows to survive A's prune, got %d", got)
	}
	bRuns, err := r.ListRuns(ctx, tgtB.ID, 0, 50)
	if err != nil {
		t.Fatalf("ListRuns B: %v", err)
	}
	if len(bRuns) != 3 {
		t.Fatalf("ListRuns B: want 3, got %d", len(bRuns))
	}

	// Target A is capped at RunHistoryCap.
	if got := countFor(tgtA.ID); got != RunHistoryCap {
		t.Fatalf("target A: want %d rows after cap, got %d", RunHistoryCap, got)
	}
}

// TestSyncMarkRecordsRun locks in that the mark-path methods
// (MarkSynced/MarkFailure) record a run row atomically with the target-state
// UPDATE — the durable per-run history the operations console reads.
func TestSyncMarkRecordsRun(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	r := NewSyncTargetRepo(s)

	projectID, _, configID := mkConfig(t, s, "prod")
	tgt, err := r.Create(ctx, newSyncTarget(t, s, projectID, configID, nil))
	if err != nil {
		t.Fatalf("Create target: %v", err)
	}

	// A successful mark records a success run carrying the config version and
	// managed-key count.
	managedKeys := []string{"DB_PASSWORD", "API_KEY"}
	fingerprint := []byte{0xde, 0xad, 0xbe, 0xef}
	if err := r.MarkSynced(ctx, tgt.ID, managedKeys, fingerprint, 7, time.Now().Add(time.Hour), time.Now(), 0); err != nil {
		t.Fatalf("MarkSynced: %v", err)
	}
	runs, err := r.ListRuns(ctx, tgt.ID, 0, 50)
	if err != nil {
		t.Fatalf("ListRuns after MarkSynced: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("want 1 run after MarkSynced, got %d", len(runs))
	}
	if runs[0].Status != "success" {
		t.Fatalf("want status success, got %q", runs[0].Status)
	}
	if runs[0].ConfigVersion == nil || *runs[0].ConfigVersion != 7 {
		t.Fatalf("want config_version 7, got %v", runs[0].ConfigVersion)
	}
	if runs[0].KeysCount != len(managedKeys) {
		t.Fatalf("want keys_count %d, got %d", len(managedKeys), runs[0].KeysCount)
	}
	if runs[0].Error != nil {
		t.Fatalf("want nil error on success run, got %v", *runs[0].Error)
	}

	// A failure mark records a failure run carrying the sanitized error, no
	// version, zero keys.
	if err := r.MarkFailure(ctx, tgt.ID, "apply failed", time.Now().Add(time.Hour), 3, time.Now(), 1); err != nil {
		t.Fatalf("MarkFailure: %v", err)
	}
	runs, err = r.ListRuns(ctx, tgt.ID, 0, 50)
	if err != nil {
		t.Fatalf("ListRuns after MarkFailure: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("want 2 runs after MarkFailure, got %d", len(runs))
	}
	newest := runs[0]
	if newest.Status != "failure" {
		t.Fatalf("want newest status failure, got %q", newest.Status)
	}
	if newest.Error == nil || *newest.Error != "apply failed" {
		t.Fatalf("want error \"apply failed\", got %v", newest.Error)
	}
	if newest.ConfigVersion != nil {
		t.Fatalf("want nil config_version on failure run, got %v", *newest.ConfigVersion)
	}
	if newest.KeysCount != 0 {
		t.Fatalf("want keys_count 0 on failure run, got %d", newest.KeysCount)
	}
}
