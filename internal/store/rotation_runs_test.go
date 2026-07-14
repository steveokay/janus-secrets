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
