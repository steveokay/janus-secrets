package audit

import (
	"context"
	"testing"
	"time"

	"github.com/steveokay/janus-secrets/internal/store"
)

// memStore is an in-memory AppendStore for the pure engine tests.
type memStore struct {
	rows        []store.AuditRow
	failNow     bool
	failIterate bool
}

func (m *memStore) Append(_ context.Context, compute func(store.AuditHead) (store.AuditRow, error)) (store.AuditRow, error) {
	if m.failNow {
		return store.AuditRow{}, errBoom
	}
	var head store.AuditHead
	if n := len(m.rows); n > 0 {
		head = store.AuditHead{Seq: m.rows[n-1].Seq, Hash: m.rows[n-1].Hash}
	}
	row, err := compute(head)
	if err != nil {
		return store.AuditRow{}, err
	}
	m.rows = append(m.rows, row)
	return row, nil
}
func (m *memStore) Iterate(_ context.Context, fn func(store.AuditRow) error) error {
	if m.failIterate {
		return errBoom
	}
	for _, r := range m.rows {
		if err := fn(r); err != nil {
			return err
		}
	}
	return nil
}
func (m *memStore) List(_ context.Context, _ store.AuditFilter, fn func(store.AuditRow) error) error {
	return m.Iterate(context.Background(), fn)
}

// ListPage is a minimal in-memory mirror of the store's keyset pagination:
// newest-first, seq < beforeSeq (0 = from head), filtered, limited.
func (m *memStore) ListPage(_ context.Context, f store.AuditFilter, beforeSeq int64, limit int) ([]store.AuditRow, error) {
	var out []store.AuditRow
	for i := len(m.rows) - 1; i >= 0; i-- {
		r := m.rows[i]
		if beforeSeq != 0 && r.Seq >= beforeSeq {
			continue
		}
		if f.Action != "" && r.Action != f.Action {
			continue
		}
		if f.Result != "" && r.Result != f.Result {
			continue
		}
		if f.Actor != "" {
			actorID := ""
			if r.ActorID != nil {
				actorID = *r.ActorID
			}
			if actorID != f.Actor && r.ActorName != f.Actor {
				continue
			}
		}
		if f.From != nil && r.OccurredAt.Before(*f.From) {
			continue
		}
		if f.To != nil && r.OccurredAt.After(*f.To) {
			continue
		}
		out = append(out, r)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

// Histogram is a minimal in-memory mirror of the store's grouped counts: all
// rows collapse into a single bucket (Start left zero) since the pure engine
// tests don't need real time-bucketing, only the pivot-by-result behavior.
func (m *memStore) Histogram(_ context.Context, f store.AuditFilter, _ string) ([]store.AuditBucketCount, error) {
	counts := map[string]int{}
	for _, r := range m.rows {
		if f.Action != "" && r.Action != f.Action {
			continue
		}
		if f.Result != "" && r.Result != f.Result {
			continue
		}
		counts[r.Result]++
	}
	var out []store.AuditBucketCount
	for res, n := range counts {
		out = append(out, store.AuditBucketCount{Result: res, Count: n})
	}
	return out, nil
}

var errBoom = errStub("boom")

type errStub string

func (e errStub) Error() string { return string(e) }

func rec(t *testing.T) (*Recorder, *memStore) {
	t.Helper()
	m := &memStore{}
	return New(m), m
}

func TestRecordChainsAndVerifies(t *testing.T) {
	r, m := rec(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := r.Record(ctx, Event{
			Actor: Actor{Kind: "user", ID: "u1", Name: "a@b.c"},
			Action: "token.mint", Resource: "tokens/x", Result: "success", IP: "1.1.1.1",
		}); err != nil {
			t.Fatal(err)
		}
	}
	if len(m.rows) != 3 || m.rows[0].Seq != 1 || m.rows[2].Seq != 3 {
		t.Fatalf("rows = %+v", m.rows)
	}
	res, err := r.Verify(ctx)
	if err != nil || !res.Valid || res.Count != 3 || res.HeadSeq != 3 {
		t.Fatalf("verify = %+v (err %v)", res, err)
	}
}

func TestVerifyDetectsTamper(t *testing.T) {
	r, m := rec(t)
	ctx := context.Background()
	_ = r.Record(ctx, Event{Actor: Actor{Kind: "user", Name: "a"}, Action: "user.create", Result: "success", IP: "ip"})
	_ = r.Record(ctx, Event{Actor: Actor{Kind: "user", Name: "a"}, Action: "user.disable", Result: "success", IP: "ip"})
	m.rows[0].Action = "user.disable" // tamper a stored field
	res, err := r.Verify(ctx)
	if err != nil || res.Valid || res.BrokenAtSeq != 1 || res.Reason != "hash_mismatch" {
		t.Fatalf("verify = %+v (err %v)", res, err)
	}
}

func TestVerifyDetectsChainBreak(t *testing.T) {
	r, m := rec(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		_ = r.Record(ctx, Event{Actor: Actor{Kind: "user", Name: "a"}, Action: "x", Result: "success", IP: "ip"})
	}
	m.rows = append(m.rows[:1], m.rows[2]) // delete seq 2
	res, _ := r.Verify(ctx)
	if res.Valid || res.Reason != "chain_break" || res.BrokenAtSeq != 3 {
		t.Fatalf("verify = %+v", res)
	}
}

func TestNullableFieldsDistinctFromEmpty(t *testing.T) {
	// A nil detail and an empty-string detail must hash differently.
	a := computeHash(make([]byte, 32), 1, ts(), "user", nil, "n", "act", "res", nil, "success", nil, "ip")
	b := computeHash(make([]byte, 32), 1, ts(), "user", nil, "n", "act", "res", strptr(""), "success", nil, "ip")
	if string(a) == string(b) {
		t.Fatal("nil and empty detail must not collide")
	}
}

func TestRecordPropagatesStoreError(t *testing.T) {
	r, m := rec(t)
	m.failNow = true
	if err := r.Record(context.Background(), Event{Actor: Actor{Kind: "user"}, Action: "x", Result: "success", IP: "ip"}); err != errBoom {
		t.Fatalf("want errBoom, got %v", err)
	}
}

func TestVerifyPropagatesStoreError(t *testing.T) {
	r, m := rec(t)
	m.failIterate = true
	res, err := r.Verify(context.Background())
	if err != errBoom || res.Valid {
		t.Fatalf("want errBoom + invalid, got res=%+v err=%v", res, err)
	}
}

func TestListPassthrough(t *testing.T) {
	r, _ := rec(t)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := r.Record(ctx, Event{Actor: Actor{Kind: "user", Name: "a"}, Action: "token.mint", Result: "success", IP: "ip"}); err != nil {
			t.Fatal(err)
		}
	}
	var seen []int64
	if err := r.List(ctx, store.AuditFilter{Action: "token.mint"}, func(row store.AuditRow) error {
		seen = append(seen, row.Seq)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 || seen[0] != 1 || seen[1] != 2 {
		t.Fatalf("list = %v", seen)
	}
}

func TestListPagePassthrough(t *testing.T) {
	r, _ := rec(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := r.Record(ctx, Event{Actor: Actor{Kind: "user", Name: "a"}, Action: "token.mint", Result: "success", IP: "ip"}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := r.ListPage(ctx, store.AuditFilter{}, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Seq != 3 || rows[1].Seq != 2 {
		t.Fatalf("listpage = %+v", rows)
	}
}

func TestHistogramPassthrough(t *testing.T) {
	r, _ := rec(t)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := r.Record(ctx, Event{Actor: Actor{Kind: "user", Name: "a"}, Action: "token.mint", Result: "success", IP: "ip"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Record(ctx, Event{Actor: Actor{Kind: "user", Name: "a"}, Action: "token.mint", Result: "denied", IP: "ip"}); err != nil {
		t.Fatal(err)
	}
	buckets, err := r.Histogram(ctx, store.AuditFilter{}, "day")
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, b := range buckets {
		counts[b.Result] += b.Count
	}
	if counts["success"] != 2 || counts["denied"] != 1 {
		t.Fatalf("histogram = %+v", counts)
	}
}

func strptr(s string) *string { return &s }

// ts is a fixed, microsecond-truncated timestamp for hash-determinism tests.
func ts() time.Time { return time.Unix(1_700_000_000, 0).UTC() }
