package auditship

import (
	"encoding/hex"
	"time"

	"github.com/steveokay/janus-secrets/internal/store"
)

// shippedEvent is the canonical JSONL shape of one audit event as sent to a
// SIEM. It mirrors the audit_events columns and is value-free by construction —
// an audit row has no secret value field. Field names are stable (a SIEM keys
// off them), snake_case, and hashes are hex-encoded for line-safe transport.
type shippedEvent struct {
	Seq        int64     `json:"seq"`
	OccurredAt time.Time `json:"occurred_at"`
	ActorKind  string    `json:"actor_kind"`
	ActorID    string    `json:"actor_id,omitempty"`
	ActorName  string    `json:"actor_name,omitempty"`
	Action     string    `json:"action"`
	Resource   string    `json:"resource,omitempty"`
	Detail     string    `json:"detail,omitempty"`
	Result     string    `json:"result"`
	ResultCode string    `json:"result_code,omitempty"`
	IP         string    `json:"ip,omitempty"`
	PrevHash   string    `json:"prev_hash,omitempty"`
	Hash       string    `json:"hash,omitempty"`
}

// toShipped projects a store.AuditRow into the wire shape. Nil pointer fields
// (actor_id, detail, result_code) become empty strings and are omitted; the
// hash-chain fields are hex-encoded so each event is a single clean JSON line.
func toShipped(a store.AuditRow) shippedEvent {
	e := shippedEvent{
		Seq:        a.Seq,
		OccurredAt: a.OccurredAt.UTC(),
		ActorKind:  a.ActorKind,
		ActorName:  a.ActorName,
		Action:     a.Action,
		Resource:   a.Resource,
		Result:     a.Result,
		IP:         a.IP,
		PrevHash:   hex.EncodeToString(a.PrevHash),
		Hash:       hex.EncodeToString(a.Hash),
	}
	if a.ActorID != nil {
		e.ActorID = *a.ActorID
	}
	if a.Detail != nil {
		e.Detail = *a.Detail
	}
	if a.ResultCode != nil {
		e.ResultCode = *a.ResultCode
	}
	return e
}
