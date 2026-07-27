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
	ListPage(ctx context.Context, f store.AuditFilter, beforeSeq int64, limit int) ([]store.AuditRow, error)
	Histogram(ctx context.Context, f store.AuditFilter, bucket string) ([]store.AuditBucketCount, error)
	// IterateFrom calls fn for every event with seq >= fromSeq, ascending. Used by
	// verify-from-checkpoint so a pruned prefix isn't required to still exist.
	IterateFrom(ctx context.Context, fromSeq int64, fn func(store.AuditRow) error) error
}

// CheckpointStore is the persistence subset the checkpoint/prune paths need
// (real: *store.AuditCheckpointRepo). It is optional: a Recorder built without
// one still verifies from genesis and rejects checkpoint/prune operations.
type CheckpointStore interface {
	Head(ctx context.Context) (seq int64, hash []byte, count int64, err error)
	Insert(ctx context.Context, cp store.AuditCheckpointRow) error
	Latest(ctx context.Context) (*store.AuditCheckpointRow, error)
	PruneThrough(ctx context.Context, throughSeq int64) (int64, error)
}

// MACKeyFunc lazily supplies the domain-separated checkpoint MAC key (derived
// from the server's token-HMAC key, only available post-unseal). The Recorder
// zeroizes the returned key after each use. Nil → checkpoint ops are disabled.
type MACKeyFunc func(ctx context.Context) ([]byte, error)

// Recorder appends events and verifies the chain.
type Recorder struct {
	store   Store
	now     func() time.Time
	ckStore CheckpointStore
	macKey  MACKeyFunc
}

// New returns a Recorder over the given store. Checkpointing is disabled until
// WithCheckpoints wires a checkpoint store + MAC-key provider.
func New(s Store) *Recorder { return &Recorder{store: s, now: time.Now} }

// WithCheckpoints wires the checkpoint store and MAC-key provider, enabling the
// signed-checkpoint create/verify/prune paths. Returns the same Recorder for
// chaining. Both must be non-nil for checkpoint operations to be available.
func (rec *Recorder) WithCheckpoints(cs CheckpointStore, mk MACKeyFunc) *Recorder {
	rec.ckStore = cs
	rec.macKey = mk
	return rec
}

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
			// NOT an input to computeHash above — by design.
			ProjectID: e.ProjectID,
		}, nil
	})
	return err
}

// List streams events matching f to fn (export). It is a thin passthrough to
// the store so the API layer never imports the store repo directly for reads.
func (rec *Recorder) List(ctx context.Context, f store.AuditFilter, fn func(store.AuditRow) error) error {
	return rec.store.List(ctx, f, fn)
}

// ListPage exposes paginated reads for the API's events endpoint.
func (rec *Recorder) ListPage(ctx context.Context, f store.AuditFilter, beforeSeq int64, limit int) ([]store.AuditRow, error) {
	return rec.store.ListPage(ctx, f, beforeSeq, limit)
}

// Histogram exposes bucketed event counts for the API's histogram endpoint.
func (rec *Recorder) Histogram(ctx context.Context, f store.AuditFilter, bucket string) ([]store.AuditBucketCount, error) {
	return rec.store.Histogram(ctx, f, bucket)
}

// nz maps "" to a nil *string (SQL NULL).
func nz(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
