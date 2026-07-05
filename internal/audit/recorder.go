package audit

import (
	"context"
	"time"

	"github.com/steveokay/janus-secrets/internal/store"
)

// Store is the persistence subset the Recorder needs (real: *store.AuditRepo).
type Store interface {
	Append(ctx context.Context, compute func(store.AuditHead) (store.AuditRow, error)) (store.AuditRow, error)
	Iterate(ctx context.Context, fn func(store.AuditRow) error) error
	List(ctx context.Context, f store.AuditFilter, fn func(store.AuditRow) error) error
}

// Recorder appends events and verifies the chain.
type Recorder struct {
	store Store
	now   func() time.Time
}

// New returns a Recorder over the given store.
func New(s Store) *Recorder { return &Recorder{store: s, now: time.Now} }

// Record appends one event, computing its seq/prev_hash/hash from the chain
// head inside the store's serialized Append. Synchronous; returns the store's
// error so callers can fail the request.
func (rec *Recorder) Record(ctx context.Context, e Event) error {
	_, err := rec.store.Append(ctx, func(head store.AuditHead) (store.AuditRow, error) {
		seq := head.Seq + 1
		prev := head.Hash
		if prev == nil {
			prev = genesisPrevHash()
		}
		occurred := rec.now().UTC().Truncate(time.Microsecond)
		actorID := nz(e.Actor.ID)
		detail := nz(e.Detail)
		code := nz(e.ResultCode)
		hash := computeHash(prev, seq, occurred, e.Actor.Kind, actorID, e.Actor.Name,
			e.Action, e.Resource, detail, e.Result, code, e.IP)
		return store.AuditRow{
			Seq: seq, OccurredAt: occurred, ActorKind: e.Actor.Kind, ActorID: actorID,
			ActorName: e.Actor.Name, Action: e.Action, Resource: e.Resource, Detail: detail,
			Result: e.Result, ResultCode: code, IP: e.IP, PrevHash: prev, Hash: hash,
		}, nil
	})
	return err
}

// nz maps "" to a nil *string (SQL NULL).
func nz(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
