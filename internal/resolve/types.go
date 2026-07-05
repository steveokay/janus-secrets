package resolve

import "context"

// Coord addresses a config by human names, as written in a reference.
type Coord struct{ Project, Env, Config string }

// RawConfig is a config's raw (un-resolved) decrypted state. Values are verbatim
// stored plaintext, ${...} intact. The consumer must treat Values as owned and
// zero them when done.
type RawConfig struct {
	ProjectID, EnvID, ConfigID string
	Project, Env, Config       string  // canonical names, for provenance/errors
	InheritsFrom               *string // parent config id, if any
	Values                     map[string][]byte
}

// Path is the human path project/env/config, for provenance and error messages.
func (rc RawConfig) Path() string { return rc.Project + "/" + rc.Env + "/" + rc.Config }

// RawReader returns raw decrypted config state, by coordinate (name lookup) or by
// id. Implemented by internal/secrets.
type RawReader interface {
	ReadRaw(ctx context.Context, coord Coord) (RawConfig, error)
	ReadRawByID(ctx context.Context, configID string) (RawConfig, error)
}

// Authorizer performs the strict per-target secret:read check for a reference.
// Implemented by internal/api. A nil Authorizer means "trusted caller" (checks
// skipped) — used only at internal call sites without a principal.
type Authorizer interface {
	CanReadSecrets(ctx context.Context, target RawConfig) error
}

// Provenance records a distinct target config read via a reference (for audit).
type Provenance struct{ ProjectID, EnvID, ConfigID, Path string }
