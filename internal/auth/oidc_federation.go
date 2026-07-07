package auth

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/steveokay/janus-secrets/internal/store"
)

const (
	defaultFederationIssuer = "https://token.actions.githubusercontent.com"
	federationMaxTTL        = time.Hour
	federationDefaultTTL    = 15 * time.Minute
)

// FederationConfigInput is the (single) trust-provider configuration used to
// verify federated CI identity tokens (e.g. GitHub Actions OIDC).
type FederationConfigInput struct {
	Issuer   string // empty → defaultFederationIssuer
	Audience string // required, non-empty
	Enabled  bool
}

// FederationConfigView is the non-secret view of the federation config.
type FederationConfigView struct {
	Issuer   string `json:"issuer"`
	Audience string `json:"audience"`
	Enabled  bool   `json:"enabled"`
}

// FederationBindingInput describes a claim-match binding that mints a scoped,
// time-limited service token for a federated CI identity.
type FederationBindingInput struct {
	Name        string
	MatchClaims map[string]string
	ScopeKind   string
	ScopeID     string
	Access      string
	TTLSeconds  int
	Enabled     bool
}

// FederationBindingView is the non-secret view of a federation binding.
type FederationBindingView struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	MatchClaims map[string]string `json:"match_claims"`
	ScopeKind   string            `json:"scope_kind"`
	ScopeID     string            `json:"scope_id"`
	Access      string            `json:"access"`
	TTLSeconds  int               `json:"ttl_seconds"`
	Enabled     bool              `json:"enabled"`
}

// FederationResult is the successful exchange outcome (filled by a later task).
type FederationResult struct {
	Token      string
	Meta       TokenMeta
	Binding    string
	Repository string
	Subject    string
}

// fedVerifier caches the go-oidc verifier for the configured federation issuer.
type fedVerifier struct {
	issuer   string
	audience string
	verifier *oidc.IDTokenVerifier
}
