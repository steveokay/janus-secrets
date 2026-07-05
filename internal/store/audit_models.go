package store

import "time"

// AuditHead is the current chain head returned to an Append closure. At genesis
// (empty table) Seq is 0 and Hash is nil.
type AuditHead struct {
	Seq  int64
	Hash []byte
}

// AuditRow is one persisted audit event. The engine fills Seq/PrevHash/Hash/
// OccurredAt inside the Append closure; the store persists the row verbatim.
type AuditRow struct {
	Seq        int64
	OccurredAt time.Time
	ActorKind  string
	ActorID    *string
	ActorName  string
	Action     string
	Resource   string
	Detail     *string
	Result     string
	ResultCode *string
	IP         string
	PrevHash   []byte
	Hash       []byte
}

// AuditFilter narrows an export. A zero field means "no constraint". Actor
// matches actor_id OR actor_name.
type AuditFilter struct {
	From   *time.Time
	To     *time.Time
	Actor  string
	Action string
	Result string
}
