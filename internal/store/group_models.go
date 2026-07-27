package store

import "time"

// Group kinds. A group is IdP-fed or admin-curated, never both — see
// migrations/000045_groups.up.sql for why the distinction is load-bearing.
const (
	GroupKindOIDC  = "oidc"
	GroupKindLocal = "local"
)

// Group is a named subject that role bindings can target instead of a user.
// ClaimValue is set exactly when Kind is GroupKindOIDC; it is the opaque value
// matched against the configured OIDC group claim (Entra emits GUIDs here,
// which is why it is kept separate from the human-facing Name).
type Group struct {
	ID          string
	Name        string
	Kind        string
	ClaimValue  *string
	Description string
	// CanCreateProjects lets members create projects owned by this group,
	// WITHOUT the instance-wide read that instance admin would carry. It is a
	// narrow capability rather than a role: roles are cumulative bundles, so any
	// role granting project:create at instance scope also grants project:read
	// there — which is the exact leak this avoids.
	CanCreateProjects bool
	CreatedBy         *string
	CreatedAt         time.Time

	// Populated by List/Get for display only; never used in a decision.
	MemberCount  int
	BindingCount int
}

// GroupInput is the create payload.
type GroupInput struct {
	Name              string
	Kind              string
	ClaimValue        *string
	Description       string
	CanCreateProjects bool
	CreatedBy         *string
}

// GroupMember is one user's membership of one group. There is no source column:
// under the two-kind model provenance is a property of the GROUP, and the
// schema's composite FK makes a hand-added member of an OIDC group
// unrepresentable. CreatedBy is nil for rows written by the login sync.
type GroupMember struct {
	GroupID   string
	UserID    string
	CreatedBy *string
	CreatedAt time.Time
}

// GroupRoleBinding grants a group a role at a scope. Role is never "owner" —
// enforced by a CHECK constraint as well as at the API boundary.
type GroupRoleBinding struct {
	ID            string
	GroupID       string
	ScopeLevel    string // "instance" | "project" | "environment"
	ProjectID     *string
	EnvironmentID *string
	Role          string // viewer | developer | admin
	CreatedBy     *string
	CreatedAt     time.Time

	// Populated by the scope listing for display (join on groups).
	GroupName string
	GroupKind string
}

// GroupRoleBindingInput is the create/upsert payload.
type GroupRoleBindingInput struct {
	GroupID       string
	ScopeLevel    string
	ProjectID     *string
	EnvironmentID *string
	Role          string
	CreatedBy     *string
}

// DerivedMember is one user's access to a scope held THROUGH a group, and the
// group it came from. One row per (user, group) pair that grants at the scope —
// a user in two granting groups produces two rows, and the caller takes the
// highest role. It is display material for "who can act here, and why"; the
// authorization decision is always made by the engine, never from this.
type DerivedMember struct {
	UserID    string
	Role      string
	GroupID   string
	GroupName string
}

// GroupSyncResult reports what one OIDC login changed, for a value-free audit
// event. Names, not ids, because the audit ledger is read by humans.
type GroupSyncResult struct {
	Added   []string
	Removed []string
}

// Changed reports whether the sync altered the user's membership (the audit
// event is emitted only on change, so a login never floods the ledger).
func (r GroupSyncResult) Changed() bool { return len(r.Added) > 0 || len(r.Removed) > 0 }
