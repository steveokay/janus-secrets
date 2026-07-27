package store

import "time"

// RoleBinding grants a user a role at a scope (instance / project / environment).
type RoleBinding struct {
	ID            string
	SubjectUserID string
	ScopeLevel    string // "instance" | "project" | "environment"
	ProjectID     *string
	EnvironmentID *string
	Role          string // viewer | developer | admin | owner
	CreatedBy     *string
	CreatedAt     time.Time

	// ViaGroupID is set when this binding was DERIVED from a group binding the
	// user holds through membership, and nil when the user is bound directly.
	// It exists so the API can explain why a user has access; the authz engine
	// ignores it, which is what keeps group and direct bindings a single union
	// rule. A derived binding's ID belongs to group_role_bindings, NOT
	// role_bindings — never feed it to a role_bindings mutation.
	ViaGroupID *string
}

// RoleBindingInput is the create/upsert payload.
type RoleBindingInput struct {
	SubjectUserID string
	ScopeLevel    string
	ProjectID     *string
	EnvironmentID *string
	Role          string
	CreatedBy     *string
}
