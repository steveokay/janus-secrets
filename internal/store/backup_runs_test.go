package store

import (
	"context"
	"testing"
	"time"
)

func TestBackupRuns(t *testing.T) {
	s := requireStore(t)
	ctx := context.Background()
	if _, err := s.pool.Exec(ctx, `TRUNCATE backup_runs RESTART IDENTITY`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	r := NewBackupRunRepo(s)

	// No runs yet: LatestRun is (nil, nil).
	latest, err := r.LatestRun(ctx)
	if err != nil {
		t.Fatalf("LatestRun empty: %v", err)
	}
	if latest != nil {
		t.Fatalf("want nil latest run, got %+v", latest)
	}

	// Insert BackupRunHistoryCap+5 runs; newest size_bytes is largest.
	const total = BackupRunHistoryCap + 5
	base := time.Now().UTC().Truncate(time.Second)
	for i := 0; i < total; i++ {
		size := int64(i + 1)
		key := "backups/janus-backup-" + base.Add(time.Duration(i)*time.Second).Format("20060102T150405Z") + ".jsonl"
		if err := r.InsertRun(ctx, BackupRunInput{
			StartedAt:  base.Add(time.Duration(i) * time.Second),
			FinishedAt: base.Add(time.Duration(i)*time.Second + time.Second),
			Status:     "success",
			ObjectKey:  &key,
			SizeBytes:  &size,
		}); err != nil {
			t.Fatalf("InsertRun #%d: %v", i, err)
		}
	}

	// Prune keeps only BackupRunHistoryCap rows.
	var count int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM backup_runs`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != BackupRunHistoryCap {
		t.Fatalf("want %d rows after cap, got %d", BackupRunHistoryCap, count)
	}

	// LatestRun is the newest (largest size_bytes == total).
	latest, err = r.LatestRun(ctx)
	if err != nil {
		t.Fatalf("LatestRun: %v", err)
	}
	if latest == nil || latest.SizeBytes == nil || *latest.SizeBytes != int64(total) {
		t.Fatalf("want latest size=%d, got %+v", total, latest)
	}
	if latest.Status != "success" {
		t.Fatalf("want success, got %q", latest.Status)
	}

	// First page: newest-first, ≤50 rows.
	page1, err := r.ListRuns(ctx, 0, 50)
	if err != nil {
		t.Fatalf("ListRuns page1: %v", err)
	}
	if len(page1) != 50 {
		t.Fatalf("want 50 runs, got %d", len(page1))
	}
	if page1[0].SizeBytes == nil || *page1[0].SizeBytes != int64(total) {
		t.Fatalf("page1 newest size want %d, got %+v", total, page1[0].SizeBytes)
	}

	// Keyset page older than the last of page1.
	page2, err := r.ListRuns(ctx, page1[len(page1)-1].ID, 50)
	if err != nil {
		t.Fatalf("ListRuns page2: %v", err)
	}
	if len(page2) != 50 {
		t.Fatalf("want 50 runs on page2, got %d", len(page2))
	}
	if page2[0].ID >= page1[len(page1)-1].ID {
		t.Fatalf("page2 must be strictly older than page1 tail")
	}

	// A failure records the sanitized error category, no object key.
	cat := "upload_failed"
	if err := r.InsertRun(ctx, BackupRunInput{
		StartedAt:  base,
		FinishedAt: base.Add(time.Second),
		Status:     "failure",
		Error:      &cat,
	}); err != nil {
		t.Fatalf("InsertRun failure: %v", err)
	}
	latest, err = r.LatestRun(ctx)
	if err != nil {
		t.Fatalf("LatestRun after failure: %v", err)
	}
	if latest.Status != "failure" || latest.Error == nil || *latest.Error != cat {
		t.Fatalf("want failure/%q, got %+v", cat, latest)
	}
	if latest.ObjectKey != nil {
		t.Fatalf("failure run must have no object key, got %v", *latest.ObjectKey)
	}
}
