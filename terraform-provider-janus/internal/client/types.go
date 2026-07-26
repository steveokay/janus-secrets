package client

// Wire shapes mirroring docs/openapi.yaml. Only the fields the provider needs
// are modelled. Secret Value fields are write-mostly; the client never logs
// them.

// Project mirrors components.schemas.Project.
type Project struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// Environment mirrors components.schemas.Environment.
type Environment struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
}

// Config mirrors components.schemas.Config.
type Config struct {
	ID            string  `json:"id"`
	EnvironmentID string  `json:"environment_id"`
	Name          string  `json:"name"`
	InheritsFrom  *string `json:"inherits_from"`
}

// SecretMeta is one entry of the MASKED secret list
// (`GET /v1/configs/{cid}/secrets`). It is value-free by design: the list
// endpoint never returns plaintext, so the provider uses ValueVersion — the
// monotonic per-key value counter Janus bumps on every write — as its
// out-of-band-change signal.
//
// Origin is "own" (defined in this config), "inherited" (visible only via a
// base config) or "overridden" (defined here AND in a base). Only "own" and
// "overridden" keys are actually stored in this config; an "inherited" key is
// not something this config owns.
type SecretMeta struct {
	ValueVersion int    `json:"value_version"`
	Origin       string `json:"origin"`
	Type         string `json:"type"`
}

// Owned reports whether the key is materialised in this config (as opposed to
// merely visible through config inheritance).
func (m SecretMeta) Owned() bool { return m.Origin != "inherited" }

// SecretChange is one entry of a batch write. Delete tombstones the key; Value
// is omitted from the wire when deleting so no empty plaintext is shipped.
type SecretChange struct {
	Key    string `json:"key"`
	Value  string `json:"value,omitempty"`
	Delete bool   `json:"delete,omitempty"`
}

// ConfigVersion is the immutable config version created by a write.
type ConfigVersion struct {
	Version   int    `json:"version"`
	ID        string `json:"id"`
	CreatedAt string `json:"created_at"`
}

// TokenScope is the scope object on a minted service token.
type TokenScope struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// MintedToken is the once-only response from POST /v1/tokens. Token carries the
// raw secret and is never logged.
type MintedToken struct {
	Token     string     `json:"token"`
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Scope     TokenScope `json:"scope"`
	Access    string     `json:"access"`
	ExpiresAt *string    `json:"expires_at"`
}

// TokenMeta mirrors components.schemas.TokenMeta (no raw token).
type TokenMeta struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	ScopeKind string  `json:"scope_kind"`
	ScopeID   string  `json:"scope_id"`
	Access    string  `json:"access"`
	ExpiresAt *string `json:"expires_at"`
}
