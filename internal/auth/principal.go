// Package auth is Janus's identity layer: Argon2id passwords, Postgres-backed
// sessions, and janus_svc_ service tokens, all HMAC-hashed at rest with a
// master-key-wrapped key. Everything that authenticates resolves to a
// Principal — the seam RBAC, audit, and Phase-2 federation build on.
package auth

// PrincipalKind discriminates how a caller authenticated.
type PrincipalKind string

const (
	KindUser         PrincipalKind = "user"
	KindServiceToken PrincipalKind = "service_token"
	// Phase 2 adds federated kinds; consumers of Principal are unaffected.
)

// Principal is an authenticated caller. Name is for audit/display (an email
// or token name) and is never secret.
type Principal struct {
	Kind PrincipalKind
	ID   string
	Name string
}
