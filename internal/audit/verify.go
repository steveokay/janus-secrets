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
	EventCount  int64  `json:"event_count"`
	CreatedAt   string `json:"created_at"` // RFC3339
	MACValid    bool   `json:"mac_valid"`
}

// VerifyResult reports chain integrity.
type VerifyResult struct {
	Valid       bool   `json:"valid"`
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

var errChainStop = errors.New("audit: chain verification stopped")

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
	prev := genesisPrevHash()
	var expectSeq int64 = 1

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
			prev = cp.ThroughHash
			expectSeq = cp.ThroughSeq + 1
			res.Count = cp.EventCount
			res.HeadSeq = cp.ThroughSeq
			res.HeadHash = res.Checkpoint.ThroughHash
			res.FromCheckpoint = true
		}
	}

	walkErr := rec.iterate(ctx, expectSeq, func(row storeRow) error {
		want := computeHash(prev, row.Seq, row.OccurredAt, row.ActorKind, row.ActorID,
			row.ActorName, row.Action, row.Resource, row.Detail, row.Result, row.ResultCode, row.IP)
		if row.Seq != expectSeq || !bytes.Equal(row.PrevHash, prev) {
			res.Reason = "chain_break"
			res.BrokenAtSeq = row.Seq
			return errChainStop
		}
		if !bytes.Equal(row.Hash, want) {
			res.Reason = "hash_mismatch"
			res.BrokenAtSeq = row.Seq
			return errChainStop
		}
		prev = row.Hash
		res.Count++
		res.HeadSeq = row.Seq
		res.HeadHash = hexstr(row.Hash)
		expectSeq++
		return nil
	})
	if errors.Is(walkErr, errChainStop) {
		res.Valid = false
		return res, nil
	}
	if walkErr != nil {
		return VerifyResult{}, walkErr
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
