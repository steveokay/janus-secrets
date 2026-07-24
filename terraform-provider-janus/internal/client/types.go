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
