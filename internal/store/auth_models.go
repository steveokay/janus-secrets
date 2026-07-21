package store

import "time"

// User is a human identity. PasswordHash is an Argon2id PHC string; nil means
// the user has no password (reserved for Phase-2 federated identities).
type User struct {
	ID           string
	Email        string
	PasswordHash *string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	DisabledAt   *time.Time
}

// Session is a UI login session. TokenHMAC is the HMAC-SHA256 of the cookie
// value; the raw value is never stored.
type Session struct {
	ID         string
	UserID     string
	TokenHMAC  []byte
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastSeenAt time.Time
	// IP and UserAgent are non-secret client metadata captured at mint so the
	// user can recognize a session in the management surface. Nil when unknown
	// (pre-migration sessions, non-HTTP mints).
	IP        *string
	UserAgent *string
}

// ServiceToken is a long-lived machine credential. TokenHMAC is the
// HMAC-SHA256 of the raw token; the raw token is shown once at mint and never
// stored. Scope is stored now and enforced by the RBAC/API milestones.
type ServiceToken struct {
	ID        string
	Name      string
	TokenHMAC []byte
	CreatedBy string
	ScopeKind string // "config" | "environment"
	ScopeID   string
	Access    string // "read" | "readwrite"
	CreatedAt time.Time
	ExpiresAt *time.Time
	RevokedAt *time.Time
	// FederationBinding is the OIDC federation binding that minted this token
	// via CI federation, or "" for a human-minted token (CreatedBy set instead).
	FederationBinding string
}
