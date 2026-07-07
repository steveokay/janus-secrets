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

// SetFederationConfig upserts the single federation trust-provider config.
// Audience is required; an empty issuer defaults to the GitHub Actions OIDC
// issuer. Invalidates the cached verifier so the next exchange re-resolves it.
func (s *Service) SetFederationConfig(ctx context.Context, in FederationConfigInput) error {
	if strings.TrimSpace(in.Audience) == "" {
		return ErrValidation
	}
	issuer := strings.TrimSpace(in.Issuer)
	if issuer == "" {
		issuer = defaultFederationIssuer
	}
	if err := s.oidcFedConfig.Put(ctx, store.OIDCFederationConfig{
		Issuer: issuer, Audience: in.Audience, Enabled: in.Enabled,
	}); err != nil {
		return err
	}
	s.invalidateFederationVerifier()
	return nil
}

// GetFederationConfig returns the configured federation trust provider, or
// ErrNotFound if none has been set.
func (s *Service) GetFederationConfig(ctx context.Context) (*FederationConfigView, error) {
	c, err := s.oidcFedConfig.Get(ctx)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &FederationConfigView{Issuer: c.Issuer, Audience: c.Audience, Enabled: c.Enabled}, nil
}

// DeleteFederationConfig removes the federation trust-provider config, if any.
func (s *Service) DeleteFederationConfig(ctx context.Context) error {
	if err := s.oidcFedConfig.Delete(ctx); err != nil {
		return err
	}
	s.invalidateFederationVerifier()
	return nil
}

// CreateFederationBinding validates and creates a claim-match binding. A
// "repository" match claim is mandatory (the minimum condition for a usable
// CI-identity binding); scope must reference an existing config or
// environment; TTL defaults to federationDefaultTTL and is capped at
// federationMaxTTL.
func (s *Service) CreateFederationBinding(ctx context.Context, in FederationBindingInput) (*FederationBindingView, error) {
	if strings.TrimSpace(in.Name) == "" {
		return nil, ErrValidation
	}
	if strings.TrimSpace(in.MatchClaims["repository"]) == "" {
		return nil, ErrValidation // repository condition is mandatory
	}
	if in.Access != "read" && in.Access != "readwrite" {
		return nil, ErrValidation
	}
	ttl := in.TTLSeconds
	if ttl == 0 {
		ttl = int(federationDefaultTTL.Seconds())
	}
	if ttl < 0 || ttl > int(federationMaxTTL.Seconds()) {
		return nil, ErrValidation
	}
	switch in.ScopeKind {
	case "config":
		if _, err := s.configs.Get(ctx, in.ScopeID); err != nil {
			return nil, scopeErr(err)
		}
	case "environment":
		if _, err := s.envs.Get(ctx, in.ScopeID); err != nil {
			return nil, scopeErr(err)
		}
	default:
		return nil, ErrValidation
	}
	b, err := s.oidcFedBindings.Create(ctx, store.OIDCFederationBinding{
		Name: in.Name, MatchClaims: in.MatchClaims, ScopeKind: in.ScopeKind,
		ScopeID: in.ScopeID, Access: in.Access, TTLSeconds: ttl, Enabled: in.Enabled,
	})
	if err != nil {
		return nil, err
	}
	return fedBindingView(b), nil
}

// ListFederationBindings returns all federation bindings, oldest first.
func (s *Service) ListFederationBindings(ctx context.Context) ([]FederationBindingView, error) {
	bs, err := s.oidcFedBindings.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]FederationBindingView, 0, len(bs))
	for i := range bs {
		out = append(out, *fedBindingView(&bs[i]))
	}
	return out, nil
}

// DeleteFederationBinding removes a binding by id. ErrNotFound if absent.
func (s *Service) DeleteFederationBinding(ctx context.Context, id string) error {
	if err := s.oidcFedBindings.Delete(ctx, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

func (s *Service) invalidateFederationVerifier() {
	s.fedMu.Lock()
	s.fedCache = nil
	s.fedMu.Unlock()
}

func fedBindingView(b *store.OIDCFederationBinding) *FederationBindingView {
	return &FederationBindingView{
		ID: b.ID, Name: b.Name, MatchClaims: b.MatchClaims, ScopeKind: b.ScopeKind,
		ScopeID: b.ScopeID, Access: b.Access, TTLSeconds: b.TTLSeconds, Enabled: b.Enabled,
	}
}

// matchFederationBinding returns the single enabled binding whose every
// match_claims entry equals the token's claim. Zero matches → ErrFederationNoMatch;
// more than one → ErrFederationAmbiguous (no "most specific wins" guessing).
func matchFederationBinding(claims map[string]string, bindings []store.OIDCFederationBinding) (*store.OIDCFederationBinding, error) {
	var matched *store.OIDCFederationBinding
	for i := range bindings {
		b := &bindings[i]
		if !b.Enabled || !claimsSatisfy(claims, b.MatchClaims) {
			continue
		}
		if matched != nil {
			return nil, ErrFederationAmbiguous
		}
		matched = b
	}
	if matched == nil {
		return nil, ErrFederationNoMatch
	}
	return matched, nil
}

// claimsSatisfy is true when every wanted claim equals the token's claim. An
// empty want never matches (defense in depth against a claim-less binding).
func claimsSatisfy(tokenClaims, want map[string]string) bool {
	if len(want) == 0 {
		return false
	}
	for k, v := range want {
		if tokenClaims[k] != v {
			return false
		}
	}
	return true
}

// stringClaims projects a raw claim set to its string-valued entries (the only
// kind bindings match on). Non-string claims (iat/exp numbers) are dropped.
func stringClaims(raw map[string]any) map[string]string {
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}
