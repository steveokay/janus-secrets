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
	// ProjectID scopes the event for authorization-filtered reads. It is
	// deliberately OUTSIDE the chain hash (see migration 000048), so it is an
	// index, not evidence: it can narrow who may READ an event, and can never
	// affect whether the chain verifies. NULL = not attributable to a project,
	// visible only to instance-wide readers.
	ProjectID *string
}

// AuditFilter narrows an export. A zero field means "no constraint". Actor
// matches actor_id OR actor_name.
type AuditFilter struct {
	From   *time.Time
	To     *time.Time
	Actor  string
	Action string
	Result string
	// Projects, when non-nil, restricts the result to events scoped to one of
	// these projects. This is an AUTHORIZATION filter, not a user-supplied one:
	// the API sets it from the caller's readable scopes and it is applied in
	// SQL, never after paging — a post-filter would let the keyset cursor skip
	// rows and silently truncate a team's trail.
	//
	// nil = unrestricted (an instance-wide reader). A non-nil EMPTY slice
	// matches nothing, which is the correct fail-closed reading of "this caller
	// can read no project".
	Projects []string
}

// AuditBucketCount is one (time-bucket, result) group with its event count.
type AuditBucketCount struct {
	Start  time.Time
	Result string
	Count  int
}
