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

func strptr(s string) *string { return &s }

// ts is a fixed, microsecond-truncated timestamp for hash-determinism tests.
func ts() time.Time { return time.Unix(1_700_000_000, 0).UTC() }
