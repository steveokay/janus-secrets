package store

import (
	"context"
	"testing"
	"time"
)

func TestRotationRuns(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	r := NewRotationRepo(s)

	projectID, _, configID := mkConfig(t, s, "prod")

	// Seed one policy to hang runs off of.
	pol, err := r.Create(ctx, newPolicy(t, s, projectID, configID, "DB_PASSWORD"))
	if err != nil {
		t.Fatalf("Create policy: %v", err)
	}

	// Insert 105 runs; config_version increases with each so newest == highest.
	const total = 105
	base := time.Now().UTC().Truncate(time.Second)
	for i := 0; i < total; i++ {
		ver := i + 1
		if err := r.InsertRun(ctx, RotationRunInput{
			PolicyID:      pol.ID,
			StartedAt:     base.Add(time.Duration(i) * time.Second),
			EndedAt:       base.Add(time.Duration(i)*time.Second + time.Second),
			Status:        "success",
			ConfigVersion: &ver,
			AttemptNum:    1,
		}); err != nil {
			t.Fatalf("InsertRun #%d: %v", i, err)
		}
	}

	// Total rows for the policy capped at RunHistoryCap (100).
	var count int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM rotation_runs WHERE policy_id = $1::uuid`, pol.ID).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != RunHistoryCap {
		t.Fatalf("want %d rows after cap, got %d", RunHistoryCap, count)
	}

	// First page: newest-first, 50 rows, config_version descending.
	page1, err := r.ListRuns(ctx, pol.ID, 0, 50)
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
	page2, err := r.ListRuns(ctx, pol.ID, cursor, 50)
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

// TestRotationRunsPrunePolicyIsolation locks in that the per-policy prune only
// deletes the target policy's rows. A regression here (prune losing its
// policy_id scope) is a cross-policy data-loss class the suite must catch.
func TestRotationRunsPrunePolicyIsolation(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	r := NewRotationRepo(s)

	projectID, _, configID := mkConfig(t, s, "prod")

	// Two policies on the same config, distinct secret keys (the unique
	// constraint is on (config_id, secret_key)).
	polA, err := r.Create(ctx, newPolicy(t, s, projectID, configID, "A_PASSWORD"))
	if err != nil {
		t.Fatalf("Create policy A: %v", err)
	}
	polB, err := r.Create(ctx, newPolicy(t, s, projectID, configID, "B_PASSWORD"))
	if err != nil {
		t.Fatalf("Create policy B: %v", err)
	}

	base := time.Now().UTC().Truncate(time.Second)
	insert := func(policyID string, n int) {
		t.Helper()
		for i := range n {
			if err := r.InsertRun(ctx, RotationRunInput{
				PolicyID:   policyID,
				StartedAt:  base.Add(time.Duration(i) * time.Second),
				EndedAt:    base.Add(time.Duration(i)*time.Second + time.Second),
				Status:     "success",
				AttemptNum: 1,
			}); err != nil {
				t.Fatalf("InsertRun policy=%s #%d: %v", policyID, i, err)
			}
		}
	}

	// B gets 3 runs (well under the cap); then A's 105 inserts trigger A's prune.
	insert(polB.ID, 3)
	insert(polA.ID, 105)

	countFor := func(policyID string) int {
		t.Helper()
		var c int
		if err := s.pool.QueryRow(ctx,
			`SELECT count(*) FROM rotation_runs WHERE policy_id = $1::uuid`, policyID).Scan(&c); err != nil {
			t.Fatalf("count policy=%s: %v", policyID, err)
		}
		return c
	}

	// Policy B's rows must be untouched by policy A's prune.
	if got := countFor(polB.ID); got != 3 {
		t.Fatalf("policy B: want 3 rows to survive A's prune, got %d", got)
	}
	bRuns, err := r.ListRuns(ctx, polB.ID, 0, 50)
	if err != nil {
		t.Fatalf("ListRuns B: %v", err)
	}
	if len(bRuns) != 3 {
		t.Fatalf("ListRuns B: want 3, got %d", len(bRuns))
	}

	// Policy A is capped at RunHistoryCap.
	if got := countFor(polA.ID); got != RunHistoryCap {
		t.Fatalf("policy A: want %d rows after cap, got %d", RunHistoryCap, got)
	}
}

// TestRotationMarkRecordsRun locks in that the mark-path methods
// (MarkRotated/MarkFailure) record a run row atomically with the policy-state
// UPDATE — the durable per-run history the operations console reads.
func TestRotationMarkRecordsRun(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	r := NewRotationRepo(s)

	projectID, _, configID := mkConfig(t, s, "prod")
	pol, err := r.Create(ctx, newPolicy(t, s, projectID, configID, "DB_PASSWORD"))
	if err != nil {
		t.Fatalf("Create policy: %v", err)
	}

	// A successful mark records a success run carrying the config version.
	if err := r.MarkRotated(ctx, pol.ID, 7, time.Now().Add(time.Hour), time.Now(), 0); err != nil {
		t.Fatalf("MarkRotated: %v", err)
	}
	runs, err := r.ListRuns(ctx, pol.ID, 0, 50)
	if err != nil {
		t.Fatalf("ListRuns after MarkRotated: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("want 1 run after MarkRotated, got %d", len(runs))
	}
	if runs[0].Status != "success" {
		t.Fatalf("want status success, got %q", runs[0].Status)
	}
	if runs[0].ConfigVersion == nil || *runs[0].ConfigVersion != 7 {
		t.Fatalf("want config_version 7, got %v", runs[0].ConfigVersion)
	}
	if runs[0].Error != nil {
		t.Fatalf("want nil error on success run, got %v", *runs[0].Error)
	}

	// A failure mark records a failure run carrying the sanitized error, no version.
	if err := r.MarkFailure(ctx, pol.ID, "apply failed", time.Now().Add(time.Hour), 3, time.Now(), 1); err != nil {
		t.Fatalf("MarkFailure: %v", err)
	}
	runs, err = r.ListRuns(ctx, pol.ID, 0, 50)
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
}
