package store

import (
	"context"
	"testing"
	"time"
)

func TestReadInsightsByKey(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	_, _, cid := mkConfig(t, s, "default")
	_, _, other := mkConfigNamed(t, s, "beta", "prod", "default")
	r := NewReadInsightsRepo(s)

	// Empty to start.
	if got, err := r.ReadInsightsByKey(ctx, cid); err != nil || len(got) != 0 {
		t.Fatalf("ReadInsightsByKey empty = %v, %v", got, err)
	}

	// Anchor on the DB's notion of "today" (UTC calendar day) so bucketing lines
	// up exactly with the aggregate. now is mid-day to avoid edge flakiness.
	var dbToday time.Time
	if err := s.pool.QueryRow(ctx, `SELECT date_trunc('day', now() AT TIME ZONE 'UTC')`).Scan(&dbToday); err != nil {
		t.Fatalf("db today: %v", err)
	}
	dbToday = dbToday.UTC()
	noon := func(daysAgo int) time.Time { return dbToday.AddDate(0, 0, -daysAgo).Add(12 * time.Hour) }
	base := "configs/" + cid + "/secrets/"

	seq := int64(0)
	rev := func(resource string, at time.Time) {
		seq++
		insertReveal(t, s, seq, "secret.reveal", "success", resource, at)
	}

	// DATABASE_URL: today x2, 5 days ago x1, and once 40 days ago (outside window
	// → excluded from Daily, but IS the last-read if newer... it's the oldest, so
	// today is the last read).
	rev(base+"DATABASE_URL", noon(0))
	rev(base+"DATABASE_URL", noon(0))
	rev(base+"DATABASE_URL", noon(5))
	rev(base+"DATABASE_URL", noon(40))
	// API_KEY: exactly one reveal, 29 days ago (oldest in-window bucket, index 0).
	rev(base+"API_KEY", noon(29))
	// Noise that must NOT count:
	insertReveal(t, s, 100, "secret.reveal", "denied", base+"DENIED_KEY", noon(0))       // denied
	insertReveal(t, s, 101, "secret.update", "success", base+"UPDATED_KEY", noon(0))     // non-reveal
	rev("configs/"+cid+"/secrets", noon(0))                                              // bulk aggregate (no key)
	rev("configs/"+other+"/secrets/OTHER", noon(0))                                      // other config

	got, err := r.ReadInsightsByKey(ctx, cid)
	if err != nil {
		t.Fatalf("ReadInsightsByKey: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 keys, got %d: %v", len(got), got)
	}

	dbu, ok := got["DATABASE_URL"]
	if !ok {
		t.Fatal("DATABASE_URL missing")
	}
	if len(dbu.Daily) != ReadInsightsDays {
		t.Fatalf("Daily len = %d, want %d", len(dbu.Daily), ReadInsightsDays)
	}
	// today is the last bucket (index len-1) with 2 reveals; 5-days-ago has 1.
	if dbu.Daily[ReadInsightsDays-1] != 2 {
		t.Errorf("today bucket = %d, want 2", dbu.Daily[ReadInsightsDays-1])
	}
	if dbu.Daily[ReadInsightsDays-1-5] != 1 {
		t.Errorf("5-days-ago bucket = %d, want 1", dbu.Daily[ReadInsightsDays-1-5])
	}
	// The 40-days-ago reveal must not appear in any bucket.
	var sum int
	for _, v := range dbu.Daily {
		sum += v
	}
	if sum != 3 {
		t.Errorf("DATABASE_URL in-window total = %d, want 3 (40d reveal excluded)", sum)
	}
	// last-read spans all history → today (noon), the most recent reveal.
	if dbu.LastReadAt == nil || !dbu.LastReadAt.Equal(noon(0)) {
		t.Errorf("DATABASE_URL LastReadAt = %v, want %v", dbu.LastReadAt, noon(0))
	}

	ak := got["API_KEY"]
	if ak.Daily[0] != 1 {
		t.Errorf("API_KEY oldest bucket (index 0, 29d ago) = %d, want 1", ak.Daily[0])
	}
	if ak.LastReadAt == nil || !ak.LastReadAt.Equal(noon(29)) {
		t.Errorf("API_KEY LastReadAt = %v, want %v", ak.LastReadAt, noon(29))
	}

	if _, bad := got["DENIED_KEY"]; bad {
		t.Error("denied reveal must not count")
	}
	if _, bad := got["UPDATED_KEY"]; bad {
		t.Error("non-reveal action must not count")
	}
	if _, bad := got["OTHER"]; bad {
		t.Error("other config's reveal must not leak in")
	}
}
