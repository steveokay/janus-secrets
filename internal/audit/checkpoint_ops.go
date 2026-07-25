package audit

import (
	"bytes"
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
	// ErrChainInvalid is returned when the hash chain itself does not verify at the
	// moment a checkpoint or prune was requested: signing a tampered head, or
	// deleting across an already-broken boundary, would launder a detectable
	// compromise into an undetectable one. Fail-closed — the operator must
	// investigate. Deliberately carries no detail beyond "does not verify" so the
	// message stays value-free.
	ErrChainInvalid = errors.New("audit: chain does not verify")
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
//
// The chain is VERIFIED before it is signed (see verifyBeforeAnchor): a
// checkpoint must attest "the chain up to here is intact", not merely "this is
// what the head row said". Without that, an operator performing ordinary
// retention over a tampered log would sign the tampered head and then prune the
// evidence, after which verify reports valid forever.
//
// event_count is a LIFETIME total of events the chain has covered, carried
// forward from the previous anchor plus everything appended since — not the
// store's current row count. Those differ once a prune has removed an anchored
// prefix, and using the row count made a post-prune checkpoint record a smaller
// number than the verify immediately before it reported (verify computes
// anchor-count + walked). Both now use the same rule, so the two agree.
func (rec *Recorder) CreateCheckpoint(ctx context.Context) (CheckpointInfo, error) {
	if rec.ckStore == nil || rec.macKey == nil {
		return CheckpointInfo{}, ErrCheckpointsDisabled
	}
	seq, hash, _, err := rec.ckStore.Head(ctx)
	if err != nil {
		return CheckpointInfo{}, err
	}
	if seq == 0 {
		return CheckpointInfo{}, ErrNoEvents
	}
	count, err := rec.verifyBeforeAnchor(ctx, seq, hash)
	if err != nil {
		return CheckpointInfo{}, err
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

// verifyBeforeAnchor validates the hash chain up to (headSeq, headHash) and
// returns ErrChainInvalid if anything about it fails to reconstruct. It is the
// guard that makes a checkpoint an attestation of integrity rather than a
// signature over whatever the head row happened to contain.
//
// The walk is anchored on the previous checkpoint when one exists — its MAC is
// validated first, so a forged anchor can never be silently re-anchored over —
// and otherwise starts at genesis. Because the walk is bounded below by the
// previous anchor and above by the head being captured, checkpointing on a
// regular schedule keeps this cheap: each run re-reads only the events appended
// since the last checkpoint. The first checkpoint after a long gap (or the very
// first one ever) is the only full-log read.
// It also returns the number of events the chain covers through headSeq,
// carried forward from the previous anchor's count (see CreateCheckpoint) so the
// value stays a lifetime total across prunes rather than a retained-row count.
func (rec *Recorder) verifyBeforeAnchor(ctx context.Context, headSeq int64, headHash []byte) (int64, error) {
	w := chainWalk{prev: genesisPrevHash(), expectSeq: 1}

	cp, err := rec.latestVerifiedCheckpoint(ctx)
	if err != nil {
		return 0, err
	}
	if cp != nil {
		if !cp.MACValid {
			// Never re-anchor over a forged checkpoint: doing so would bless it.
			return 0, ErrChainInvalid
		}
		if cp.ThroughSeq > headSeq {
			// The head is BELOW an existing anchor: the log was truncated under us.
			return 0, ErrChainInvalid
		}
		if cp.ThroughSeq == headSeq {
			// Nothing new since the last anchor. The head must still match what that
			// anchor attests; the store's UNIQUE(through_seq) then rejects the
			// duplicate insert as a conflict.
			if !bytes.Equal(cp.ThroughHash, headHash) {
				return 0, ErrChainInvalid
			}
			return cp.EventCount, nil
		}
		w.prev = cp.ThroughHash
		w.expectSeq = cp.ThroughSeq + 1
		w.headSeq = cp.ThroughSeq
		w.headHash = cp.ThroughHash
		// Carry the anchor's lifetime total forward; the walk adds only the
		// events appended since. This is exactly how Verify computes its count,
		// which is what keeps the two consistent.
		w.count = cp.EventCount
	}

	if err := rec.walkChain(ctx, &w, headSeq); err != nil {
		return 0, err
	}
	if w.broken {
		return 0, ErrChainInvalid
	}
	// The walk must have actually reached the head we are about to sign; a short
	// walk means events between the anchor and the head are missing.
	if w.headSeq != headSeq || !bytes.Equal(w.headHash, headHash) {
		return 0, ErrChainInvalid
	}
	return w.count, nil
}

// checkAnchorBoundary confirms the surviving chain still links to the anchor:
// the first event with seq > cp.ThroughSeq must be exactly seq+1 and carry the
// anchor's through_hash as its prev_hash. If the boundary is already broken,
// deleting the prefix would destroy the only remaining evidence, so prune
// refuses with ErrChainInvalid. No events past the anchor is fine (nothing to
// link) and is allowed.
func (rec *Recorder) checkAnchorBoundary(ctx context.Context, cp *verifiedCheckpoint) error {
	wantSeq := cp.ThroughSeq + 1
	var found, linked bool
	err := rec.store.IterateFrom(ctx, wantSeq, func(row storeRow) error {
		found = true
		linked = row.Seq == wantSeq && bytes.Equal(row.PrevHash, cp.ThroughHash)
		return errWalkDone // only the first survivor matters
	})
	if err != nil && !errors.Is(err, errWalkDone) {
		return err
	}
	if found && !linked {
		return ErrChainInvalid
	}
	return nil
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
//   - the surviving chain must still link to the anchor (else ErrChainInvalid):
//     if the anchor→survivor boundary is already broken, the prefix about to be
//     deleted is evidence, not noise,
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
	if err := rec.checkAnchorBoundary(ctx, cp); err != nil {
		return PruneResult{}, err
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
