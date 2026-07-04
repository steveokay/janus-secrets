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
