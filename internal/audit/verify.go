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

// VerifyResult reports chain integrity.
type VerifyResult struct {
	Valid       bool   `json:"valid"`
	Count       int64  `json:"count"`
	HeadSeq     int64  `json:"head_seq"`
	HeadHash    string `json:"head_hash,omitempty"` // hex
	BrokenAtSeq int64  `json:"broken_at_seq,omitempty"`
	Reason      string `json:"reason,omitempty"` // "hash_mismatch" | "chain_break"
}

var errChainStop = errors.New("audit: chain verification stopped")

// Verify walks the chain in seq order, recomputing each hash and checking
// linkage. It reports the first break (if any). A structural break returns
// Valid=false with a nil error; only a store error returns non-nil.
func (rec *Recorder) Verify(ctx context.Context) (VerifyResult, error) {
	var res VerifyResult
	prev := genesisPrevHash()
	var expectSeq int64 = 1
	walkErr := rec.store.Iterate(ctx, func(row storeRow) error {
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
