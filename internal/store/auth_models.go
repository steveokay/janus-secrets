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
	// LastLoginAt is the most recent successful session mint (password or OIDC
	// login). Nil means the user has never logged in. Value-free metadata.
	LastLoginAt *time.Time
	// Account-lockout state (progressive backoff). FailedLoginCount is the
	// consecutive-failure count in the current cycle; LockoutLevel drives the
	// escalating window; LockedUntil (when set and in the future) means the
	// account is locked and auto-expires; LastFailedLoginAt records the most
	// recent counted failure. None are secret.
	FailedLoginCount  int
	LockoutLevel      int
	LockedUntil       *time.Time
	LastFailedLoginAt *time.Time
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
	// LastUsedAt is the most recent successful authentication with this token,
	// updated best-effort and throttled (never within 60s of the last stamp). Nil
	// means the token has never authenticated a request. Value-free metadata.
	LastUsedAt *time.Time
	// FederationBinding is the OIDC federation binding that minted this token
	// via CI federation, or "" for a human-minted token (CreatedBy set instead).
	FederationBinding string
	// IPAllowlist is an optional list of CIDRs (IPv4 or IPv6). When non-empty, a
	// request authenticated with this token whose client IP is outside every
	// listed CIDR is rejected. Empty/nil means "any IP". Value-free.
	IPAllowlist []string
}
