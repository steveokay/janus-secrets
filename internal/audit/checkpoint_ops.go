package audit

import (
	"context"
	"errors"
	"time"

	"github.com/steveokay/janus-secrets/internal/store"
)

// rfc3339 is the timestamp format used in checkpoint API responses.
const rfc3339 = time.RFC3339

var (
	// ErrCheckpointsDisabled is returned when a checkpoint operation is requested
	// but the Recorder was not wired with a checkpoint store + MAC-key provider.
	ErrCheckpointsDisabled = errors.New("audit: checkpointing not configured")
	// ErrNoEvents is returned when a checkpoint is requested over an empty chain
	// (nothing to anchor).
	ErrNoEvents = errors.New("audit: no events to checkpoint")
	// ErrNoCheckpoint is returned by prune when no checkpoint exists to anchor the
	// deletion (fail-closed: never prune an unverified prefix).
	ErrNoCheckpoint = errors.New("audit: no checkpoint to prune to")
	// ErrCheckpointMAC is returned by prune when the chosen checkpoint's MAC does
	// not verify (fail-closed: a forged anchor must never authorize a delete).
	ErrCheckpointMAC = errors.New("audit: checkpoint mac invalid")
	// ErrPrunePastShipHWM is returned when a prune would remove events not yet
	// shipped to the external audit sink (fail-closed retention guard).
	ErrPrunePastShipHWM = errors.New("audit: prune would remove un-shipped events")
)

// verifiedCheckpoint is the latest stored checkpoint plus whether its MAC checks.
type verifiedCheckpoint struct {
	store.AuditCheckpointRow
	MACValid bool
}

// withMACKey loads the checkpoint MAC key, runs fn, and zeroizes the key.
func (rec *Recorder) withMACKey(ctx context.Context, fn func(key []byte) error) error {
	key, err := rec.macKey(ctx)
	if err != nil {
		return err
	}
	defer zeroKey(key)
	return fn(key)
}

func zeroKey(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// latestVerifiedCheckpoint loads the highest-seq checkpoint and reports whether
// its MAC verifies under the current key. Returns (nil, nil) when none exist.
func (rec *Recorder) latestVerifiedCheckpoint(ctx context.Context) (*verifiedCheckpoint, error) {
	row, err := rec.ckStore.Latest(ctx)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, nil
	}
	out := &verifiedCheckpoint{AuditCheckpointRow: *row}
	if err := rec.withMACKey(ctx, func(key []byte) error {
		out.MACValid = verifyCheckpointMAC(key, Checkpoint{
			ThroughSeq:  row.ThroughSeq,
			ThroughHash: row.ThroughHash,
			EventCount:  row.EventCount,
			MAC:         row.MAC,
		})
		return nil
	}); err != nil {
		return nil, err
	}
	return out, nil
}

// CreateCheckpoint captures the current chain head, computes+stores a signed
// checkpoint over (through_seq, through_hash, event_count), and returns its
// public info. It requires at least one event. A duplicate anchor at the same
// head seq is a store conflict (ErrAlreadyExists), surfaced to the caller.
func (rec *Recorder) CreateCheckpoint(ctx context.Context) (CheckpointInfo, error) {
	if rec.ckStore == nil || rec.macKey == nil {
		return CheckpointInfo{}, ErrCheckpointsDisabled
	}
	seq, hash, count, err := rec.ckStore.Head(ctx)
	if err != nil {
		return CheckpointInfo{}, err
	}
	if seq == 0 {
		return CheckpointInfo{}, ErrNoEvents
	}
	var mac []byte
	if err := rec.withMACKey(ctx, func(key []byte) error {
		mac = computeCheckpointMAC(key, seq, hash, count)
		return nil
	}); err != nil {
		return CheckpointInfo{}, err
	}
	if err := rec.ckStore.Insert(ctx, store.AuditCheckpointRow{
		ThroughSeq:  seq,
		ThroughHash: hash,
		EventCount:  count,
		MAC:         mac,
	}); err != nil {
		return CheckpointInfo{}, err
	}
	return CheckpointInfo{
		ThroughSeq:  seq,
		ThroughHash: hexstr(hash),
		EventCount:  count,
		CreatedAt:   rec.now().UTC().Format(rfc3339),
		MACValid:    true,
	}, nil
}

// LatestCheckpoint returns the latest checkpoint's public info (with a verified
// MAC flag), or nil when none exist / checkpointing is disabled.
func (rec *Recorder) LatestCheckpoint(ctx context.Context) (*CheckpointInfo, error) {
	if rec.ckStore == nil || rec.macKey == nil {
		return nil, nil
	}
	cp, err := rec.latestVerifiedCheckpoint(ctx)
	if err != nil {
		return nil, err
	}
	if cp == nil {
		return nil, nil
	}
	return &CheckpointInfo{
		ThroughSeq:  cp.ThroughSeq,
		ThroughHash: hexstr(cp.ThroughHash),
		EventCount:  cp.EventCount,
		CreatedAt:   cp.CreatedAt.UTC().Format(rfc3339),
		MACValid:    cp.MACValid,
	}, nil
}

// PruneResult reports what a prune removed.
type PruneResult struct {
	PrunedThrough int64 `json:"pruned_through"` // the seq up to and including which events were deleted
	Deleted       int64 `json:"deleted"`        // number of events hard-deleted
}

// Prune hard-deletes audit events with seq <= the latest verified checkpoint's
// through_seq, subject to fail-closed guards:
//
//   - checkpointing must be wired (else ErrCheckpointsDisabled),
//   - a checkpoint must exist (else ErrNoCheckpoint),
//   - the checkpoint's MAC must verify (else ErrCheckpointMAC),
//   - if a ship high-water mark is supplied (shipHWM >= 0), the prune point is
//     clamped to min(checkpoint.through_seq, shipHWM) so events not yet shipped to
//     the external sink are never removed. If that clamp leaves nothing to prune,
//     it returns ErrPrunePastShipHWM.
//
// It never deletes checkpoint anchor rows (only audit_events). shipHWM < 0 means
// "no shipper configured" (no HWM guard).
func (rec *Recorder) Prune(ctx context.Context, shipHWM int64) (PruneResult, error) {
	if rec.ckStore == nil || rec.macKey == nil {
		return PruneResult{}, ErrCheckpointsDisabled
	}
	cp, err := rec.latestVerifiedCheckpoint(ctx)
	if err != nil {
		return PruneResult{}, err
	}
	if cp == nil {
		return PruneResult{}, ErrNoCheckpoint
	}
	if !cp.MACValid {
		return PruneResult{}, ErrCheckpointMAC
	}
	pruneThrough := cp.ThroughSeq
	if shipHWM >= 0 && shipHWM < pruneThrough {
		// Never prune past what has been shipped: clamp to the ship HWM.
		pruneThrough = shipHWM
	}
	if pruneThrough < 1 {
		// The ship HWM (or the checkpoint) leaves nothing safely prunable.
		return PruneResult{}, ErrPrunePastShipHWM
	}
	deleted, err := rec.ckStore.PruneThrough(ctx, pruneThrough)
	if err != nil {
		return PruneResult{}, err
	}
	return PruneResult{PrunedThrough: pruneThrough, Deleted: deleted}, nil
}
