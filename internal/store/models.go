package store

import "time"

// EncryptedValue is an opaque encrypted secret value. The store persists and
// returns these bytes verbatim; it never inspects or decrypts them.
type EncryptedValue struct {
	// WrappedDEK is the data-encryption key wrapped by the project KEK.
	WrappedDEK []byte
	// Ciphertext is the secret value encrypted under the DEK (includes the
	// AEAD tag).
	Ciphertext []byte
	// Nonce is the AEAD nonce used to produce Ciphertext.
	Nonce []byte
	// DEKKeyVersion is the project KEK version that wrapped WrappedDEK. It is
	// the version of the wrapping key, not of the DEK (DEKs are not versioned);
	// it lets a KEK-rotation sweep find rows whose DEK needs re-wrapping.
	DEKKeyVersion int
}

// Project is the top of the hierarchy and owns a wrapped project KEK.
type Project struct {
	ID         string
	Slug       string
	Name       string
	WrappedKEK []byte
	KEKVersion int
	CreatedAt  time.Time
	UpdatedAt  time.Time
	DeletedAt  *time.Time
}

// Environment is a user-definable environment within a project (dev/staging/prod).
type Environment struct {
	ID        string
	ProjectID string
	Slug      string
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time
}

// Config holds secrets within an environment. InheritsFrom is reserved for the
// not-yet-implemented inheritance feature.
type Config struct {
	ID            string
	EnvironmentID string
	Name          string
	InheritsFrom  *string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	DeletedAt     *time.Time
}

// ConfigVersion is one immutable save — the unit of diff and rollback.
type ConfigVersion struct {
	ID       string
	ConfigID string
	Version  int
	Message  string
	// CreatedBy is a free-form actor identifier supplied by the caller (a user
	// or token id once auth lands). The store does not interpret it.
	CreatedBy string
	CreatedAt time.Time
}

// SecretValue is one immutable, append-only value in a key's history.
type SecretValue struct {
	ID           string
	ConfigID     string
	Key          string
	ValueVersion int
	EncryptedValue
	CreatedAt time.Time
}

// Change is one edit within a batched save. Encrypt == nil means delete the key
// (a tombstone). Otherwise the store calls Encrypt with the value_version it
// assigns to this key, and Encrypt returns the opaque encrypted value bound to
// that exact version. Returning an error from Encrypt aborts the whole save.
type Change struct {
	Key     string
	Encrypt func(valueVersion int) (*EncryptedValue, error)
}

// Diff is the set difference between two config versions.
type Diff struct {
	Added   []string
	Changed []string
	Removed []string
}

// OIDCProvider is a configured OIDC identity provider. WrappedClientSecret is
// the master-key-wrapped client secret (never plaintext at rest).
type OIDCProvider struct {
	ID                  string
	Name                string
	Issuer              string
	ClientID            string
	WrappedClientSecret []byte
	Scopes              []string
	RedirectURL         string
	Enabled             bool
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// OIDCIdentity links a provider subject to a Janus user. (Issuer, Subject) is
// the durable federated identity; email is only used to match on first login.
type OIDCIdentity struct {
	ID          string
	UserID      string
	Issuer      string
	Subject     string
	CreatedAt   time.Time
	LastLoginAt time.Time
}

// OIDCAuthRequest is a short-lived, single-use login state row created at the
// start of an Authorization-Code flow and consumed at the callback.
type OIDCAuthRequest struct {
	State        string
	Nonce        string
	PKCEVerifier string
	ProviderID   string
	CreatedAt    time.Time
	ExpiresAt    time.Time
}

// OIDCFederationConfig is the single trust-provider row for CI federation
// (e.g. GitHub Actions OIDC): the issuer and audience that federated JWTs
// must present before any binding is matched.
type OIDCFederationConfig struct {
	ID        string
	Issuer    string
	Audience  string
	Enabled   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// OIDCFederationBinding maps claim matches from a federated CI identity token
// to a scoped, time-limited service token. MatchClaims is compared against
// the verified JWT claims; ScopeKind/ScopeID/Access mirror ServiceToken's
// scoping model.
type OIDCFederationBinding struct {
	ID          string
	Name        string
	MatchClaims map[string]string
	ScopeKind   string
	ScopeID     string
	Access      string
	TTLSeconds  int
	Enabled     bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
