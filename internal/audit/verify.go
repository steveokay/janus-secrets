package audit

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"

	"github.com/steveokay/janus-secrets/internal/store"
)

type storeRow = store.AuditRow

func hexstr(b []byte) string { return hex.EncodeToString(b) }

// CheckpointInfo describes the latest signed checkpoint in a verify response.
type CheckpointInfo struct {
	ThroughSeq  int64  `json:"through_seq"`
	ThroughHash string `json:"through_hash"` // hex
	// EventCount is the number of audit events the chain held at the moment the
	// checkpoint was captured — i.e. COUNT(*) over audit_events at that time.
	// A LIFETIME total of events the chain has covered: the previous anchor's
	// count plus every event appended since. It is deliberately NOT the store's
	// current row count, which diverges once a prune removes an anchored prefix.
	EventCount int64  `json:"event_count"`
	CreatedAt  string `json:"created_at"` // RFC3339
	MACValid   bool   `json:"mac_valid"`
}

// VerifyResult reports chain integrity.
type VerifyResult struct {
	Valid bool `json:"valid"`
	// Count is the number of events covered by this verification: when anchored on
	// a checkpoint it is the checkpoint's EventCount plus every event walked past
	// the anchor. Since CheckpointInfo.EventCount is itself a lifetime total,
	// Count means "events the chain has covered", consistent across prunes and
	// consistent with what a checkpoint taken at the same moment records.
	Count       int64  `json:"count"`
	HeadSeq     int64  `json:"head_seq"`
	HeadHash    string `json:"head_hash,omitempty"` // hex
	BrokenAtSeq int64  `json:"broken_at_seq,omitempty"`
	Reason      string `json:"reason,omitempty"` // "hash_mismatch" | "chain_break" | "checkpoint_mac_invalid"
	// Checkpoint is the latest signed checkpoint, when one exists and checkpointing
	// is wired. Nil when there are no checkpoints (walk from genesis).
	Checkpoint *CheckpointInfo `json:"checkpoint,omitempty"`
	// FromCheckpoint reports whether this verification anchored on the checkpoint
	// rather than walking from genesis.
	FromCheckpoint bool `json:"from_checkpoint"`
}

var (
	errChainStop = errors.New("audit: chain verification stopped")
	// errWalkDone stops a bounded walk once it has consumed the event it was told
	// to stop at. Distinct from errChainStop so "reached the bound" is never
	// confused with "found a break".
	errWalkDone = errors.New("audit: chain walk complete")
)

// chainWalk carries the running state and the outcome of a forward chain walk.
// It is the single source of truth for chain validation, shared by Verify (which
// renders it as a VerifyResult) and CreateCheckpoint (which refuses to sign a
// head the walk did not validate).
type chainWalk struct {
	prev      []byte // running chain hash: the hash the next event must carry as prev_hash
	expectSeq int64  // the seq the next event must carry
	count     int64  // events covered so far (may start at a checkpoint's EventCount)
	headSeq   int64  // highest seq validated (starts at the anchor's seq, if anchored)
	headHash  []byte // chain hash at headSeq

	broken   bool   // a structural break was found
	reason   string // "chain_break" | "hash_mismatch" (only when broken)
	brokenAt int64  // the seq at which the break was found (only when broken)
}

// walkChain walks events with seq >= w.expectSeq in ascending order, validating
// each event's seq and prev_hash linkage and recomputing its hash, updating w in
// place. When throughSeq > 0 the walk stops after consuming the event at that
// seq (used by CreateCheckpoint to bound the walk at the head it is about to
// anchor); throughSeq <= 0 walks to the end of the log.
//
// A structural break sets w.broken/w.reason/w.brokenAt and returns a nil error —
// only a store failure returns non-nil.
func (rec *Recorder) walkChain(ctx context.Context, w *chainWalk, throughSeq int64) error {
	walkErr := rec.iterate(ctx, w.expectSeq, func(row storeRow) error {
		want := computeHash(w.prev, row.Seq, row.OccurredAt, row.ActorKind, row.ActorID,
			row.ActorName, row.Action, row.Resource, row.Detail, row.Result, row.ResultCode, row.IP)
		if row.Seq != w.expectSeq || !bytes.Equal(row.PrevHash, w.prev) {
			w.broken, w.reason, w.brokenAt = true, "chain_break", row.Seq
			return errChainStop
		}
		if !bytes.Equal(row.Hash, want) {
			w.broken, w.reason, w.brokenAt = true, "hash_mismatch", row.Seq
			return errChainStop
		}
		w.prev = row.Hash
		w.count++
		w.headSeq = row.Seq
		w.headHash = row.Hash
		w.expectSeq++
		if throughSeq > 0 && row.Seq >= throughSeq {
			return errWalkDone
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, errChainStop) && !errors.Is(walkErr, errWalkDone) {
		return walkErr
	}
	return nil
}

// Verify walks the chain and reports integrity. When a signed checkpoint exists
// (and checkpointing is wired), it first validates the checkpoint's MAC, then
// walks the chain FORWARD from the anchor (through_seq+1), linking the first
// post-anchor event to the checkpoint's through_hash. This remains correct
// whether or not the pre-anchor prefix has been pruned. With no checkpoints (or
// checkpointing not wired) it walks from genesis — the original behavior. A
// structural break or an invalid checkpoint MAC returns Valid=false with a nil
// error; only a store error returns non-nil.
func (rec *Recorder) Verify(ctx context.Context) (VerifyResult, error) {
	var res VerifyResult
	w := chainWalk{prev: genesisPrevHash(), expectSeq: 1}

	if rec.ckStore != nil && rec.macKey != nil {
		cp, err := rec.latestVerifiedCheckpoint(ctx)
		if err != nil {
			return VerifyResult{}, err
		}
		if cp != nil {
			res.Checkpoint = &CheckpointInfo{
				ThroughSeq:  cp.ThroughSeq,
				ThroughHash: hexstr(cp.ThroughHash),
				EventCount:  cp.EventCount,
				CreatedAt:   cp.CreatedAt.UTC().Format(rfc3339),
				MACValid:    cp.MACValid,
			}
			if !cp.MACValid {
				// A present-but-forged checkpoint is an integrity failure: we cannot
				// trust the anchor, and refusing to fall back to a genesis walk stops an
				// attacker who tampered a checkpoint from hiding it behind that walk.
				res.Valid = false
				res.Reason = "checkpoint_mac_invalid"
				res.BrokenAtSeq = cp.ThroughSeq
				return res, nil
			}
			// Anchor the forward walk on the checkpoint.
			w.prev = cp.ThroughHash
			w.expectSeq = cp.ThroughSeq + 1
			w.count = cp.EventCount
			w.headSeq = cp.ThroughSeq
			w.headHash = cp.ThroughHash
			res.FromCheckpoint = true
		}
	}

	if err := rec.walkChain(ctx, &w, 0); err != nil {
		return VerifyResult{}, err
	}
	res.Count = w.count
	res.HeadSeq = w.headSeq
	if len(w.headHash) > 0 {
		res.HeadHash = hexstr(w.headHash)
	}
	if w.broken {
		res.Valid = false
		res.Reason = w.reason
		res.BrokenAtSeq = w.brokenAt
		return res, nil
	}
	res.Valid = true
	return res, nil
}

// iterate walks events with seq >= fromSeq. It uses the checkpoint-aware
// IterateFrom (so a pruned prefix isn't required) when anchored past genesis;
// otherwise it uses the full Iterate to preserve the genesis-walk path exactly.
func (rec *Recorder) iterate(ctx context.Context, fromSeq int64, fn func(storeRow) error) error {
	if fromSeq > 1 {
		return rec.store.IterateFrom(ctx, fromSeq, fn)
	}
	return rec.store.Iterate(ctx, fn)
}
