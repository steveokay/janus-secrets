package store

import (
	"context"
	"testing"
	"time"
)

// insertReveal writes a minimal audit_events row for tests. It bypasses the
// hash-chain (seq/hash are arbitrary but unique) since LastReadByKey only reads
// action/result/resource/occurred_at. Value-free — no secret material.
func insertReveal(t *testing.T, s *Store, seq int64, action, result, resource string, at time.Time) {
	t.Helper()
	_, err := s.pool.Exec(context.Background(),
		`INSERT INTO audit_events
		   (seq, occurred_at, actor_kind, actor_name, action, resource, result, ip, prev_hash, hash)
		 VALUES ($1,$2,'user','tester',$3,$4,$5,'127.0.0.1','\x00','\x00')`,
		seq, at, action, resource, result)
	if err != nil {
		t.Fatalf("insertReveal seq=%d: %v", seq, err)
	}
}

func TestLastReadByKey(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	_, _, cid := mkConfig(t, s, "default")
	_, _, other := mkConfigNamed(t, s, "beta", "prod", "default")
	r := NewLastReadRepo(s)

	// Empty to start.
	if got, err := r.LastReadByKey(ctx, cid); err != nil || len(got) != 0 {
		t.Fatalf("LastReadByKey empty = %v, %v", got, err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	base := "configs/" + cid + "/secrets/"

	// DATABASE_URL: two reveals; MAX wins.
	insertReveal(t, s, 1, "secret.reveal", "success", base+"DATABASE_URL", now.Add(-72*time.Hour))
	insertReveal(t, s, 2, "secret.reveal", "success", base+"DATABASE_URL", now.Add(-24*time.Hour))
	// API_KEY: one reveal.
	insertReveal(t, s, 3, "secret.reveal", "success", base+"API_KEY", now.Add(-1*time.Hour))
	// A denied reveal must NOT count.
	insertReveal(t, s, 4, "secret.reveal", "denied", base+"DENIED_KEY", now)
	// A non-reveal action on a matching resource must NOT count.
	insertReveal(t, s, 5, "secret.update", "success", base+"UPDATED_KEY", now)
	// The bulk aggregate resource (no trailing key) must NOT be attributed.
	insertReveal(t, s, 6, "secret.reveal", "success", "configs/"+cid+"/secrets", now)
	// Another config's reveal must NOT leak in.
	insertReveal(t, s, 7, "secret.reveal", "success", "configs/"+other+"/secrets/OTHER", now)

	got, err := r.LastReadByKey(ctx, cid)
	if err != nil {
		t.Fatalf("LastReadByKey: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 keys, got %d: %v", len(got), got)
	}
	if !got["DATABASE_URL"].Equal(now.Add(-24 * time.Hour)) {
		t.Errorf("DATABASE_URL last read = %v, want %v", got["DATABASE_URL"], now.Add(-24*time.Hour))
	}
	if !got["API_KEY"].Equal(now.Add(-1 * time.Hour)) {
		t.Errorf("API_KEY last read = %v, want %v", got["API_KEY"], now.Add(-1*time.Hour))
	}
	if _, ok := got["DENIED_KEY"]; ok {
		t.Error("denied reveal must not count")
	}
	if _, ok := got["UPDATED_KEY"]; ok {
		t.Error("non-reveal action must not count")
	}
	if _, ok := got["OTHER"]; ok {
		t.Error("other config's reveal must not leak in")
	}
}
