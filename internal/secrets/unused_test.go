package secrets

import (
	"context"
	"testing"
	"time"
)

// insertRevealEvent writes a minimal audit_events row via the test pool. It
// bypasses the hash chain (seq/hash arbitrary) since unused detection reads only
// action/result/resource/occurred_at. Value-free — no secret material.
func insertRevealEvent(t *testing.T, seq int64, action, result, resource string, at time.Time) {
	t.Helper()
	_, err := testPool.Exec(context.Background(),
		`INSERT INTO audit_events
		   (seq, occurred_at, actor_kind, actor_name, action, resource, result, ip, prev_hash, hash)
		 VALUES ($1,$2,'user','tester',$3,$4,$5,'127.0.0.1','\x00','\x00')`,
		seq, at, action, resource, result)
	if err != nil {
		t.Fatalf("insertRevealEvent seq=%d: %v", seq, err)
	}
}

func TestUnused_NeverStaleFresh(t *testing.T) {
	s := newService(t)
	ctx, cid := seedMaxAgeConfig(t, s) // seeds DATABASE_URL + API_KEY

	// No reveals on record → every key is "never read" → unused, last_read nil.
	metas, err := s.ListSecretsMerged(ctx, cid)
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 2 {
		t.Fatalf("want 2 keys, got %d", len(metas))
	}
	for _, m := range metas {
		if m.LastReadAt != nil {
			t.Errorf("%s: LastReadAt = %v, want nil (never read)", m.Key, m.LastReadAt)
		}
		if !m.Unused {
			t.Errorf("%s: Unused = false, want true (never read)", m.Key)
		}
	}
	if n, err := s.CountUnusedKeys(ctx, cid); err != nil || n != 2 {
		t.Fatalf("CountUnusedKeys = %d, %v; want 2", n, err)
	}
}

func TestUnused_RecentVsStaleRead(t *testing.T) {
	s := newService(t)
	ctx, cid := seedMaxAgeConfig(t, s)
	// Default threshold is 90 days; override to a small window for a deterministic
	// test without a 90-day-old fixture.
	s.SetUnusedSecretDays(1) // 1 day window

	now := time.Now().UTC()
	base := "configs/" + cid + "/secrets/"

	// DATABASE_URL read 2 hours ago → within window → NOT unused.
	insertRevealEvent(t, 101, "secret.reveal", "success", base+"DATABASE_URL", now.Add(-2*time.Hour))
	// API_KEY read 3 days ago → outside window → unused (stale read).
	insertRevealEvent(t, 102, "secret.reveal", "success", base+"API_KEY", now.Add(-72*time.Hour))

	metas, err := s.ListSecretsMerged(ctx, cid)
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[string]MergedMeta{}
	for _, m := range metas {
		byKey[m.Key] = m
	}

	db := byKey["DATABASE_URL"]
	if db.LastReadAt == nil {
		t.Fatal("DATABASE_URL LastReadAt = nil, want a timestamp")
	}
	if db.Unused {
		t.Error("DATABASE_URL Unused = true, want false (read within window)")
	}

	api := byKey["API_KEY"]
	if api.LastReadAt == nil {
		t.Fatal("API_KEY LastReadAt = nil, want a timestamp (stale read)")
	}
	if !api.Unused {
		t.Error("API_KEY Unused = false, want true (last read outside window)")
	}

	if n, err := s.CountUnusedKeys(ctx, cid); err != nil || n != 1 {
		t.Fatalf("CountUnusedKeys = %d, %v; want 1", n, err)
	}
}

func TestUnused_BulkAggregateNotAttributed(t *testing.T) {
	s := newService(t)
	ctx, cid := seedMaxAgeConfig(t, s)
	s.SetUnusedSecretDays(1)
	now := time.Now().UTC()

	// A bulk raw read records the aggregate resource with no trailing key. It must
	// NOT count as a per-key read for either seeded key.
	insertRevealEvent(t, 201, "secret.reveal", "success", "configs/"+cid+"/secrets", now)

	metas, err := s.ListSecretsMerged(ctx, cid)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range metas {
		if m.LastReadAt != nil || !m.Unused {
			t.Errorf("%s: bulk aggregate leaked into per-key last-read (last=%v unused=%v)", m.Key, m.LastReadAt, m.Unused)
		}
	}
}

func TestUnused_DaysThresholdDefaultAndClamp(t *testing.T) {
	s := newService(t)
	if got := s.UnusedSecretDays(); got != DefaultUnusedSecretDays {
		t.Fatalf("default = %d, want %d", got, DefaultUnusedSecretDays)
	}
	s.SetUnusedSecretDays(30)
	if got := s.UnusedSecretDays(); got != 30 {
		t.Fatalf("after set 30 = %d", got)
	}
	s.SetUnusedSecretDays(0) // non-positive → reset to default
	if got := s.UnusedSecretDays(); got != DefaultUnusedSecretDays {
		t.Fatalf("after set 0 = %d, want default %d", got, DefaultUnusedSecretDays)
	}
	s.SetUnusedSecretDays(-5)
	if got := s.UnusedSecretDays(); got != DefaultUnusedSecretDays {
		t.Fatalf("after set -5 = %d, want default %d", got, DefaultUnusedSecretDays)
	}
}
