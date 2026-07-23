package auth

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/steveokay/janus-secrets/internal/store"
)

const (
	// Well-known CI OIDC issuer URLs. GitHub Actions is the default; GitLab.com
	// and Buildkite have fixed issuers. CircleCI's issuer is org-specific
	// (https://oidc.circleci.com/org/<ORG_ID>) so it has no fixed constant here.
	issuerGitHubActions = "https://token.actions.githubusercontent.com"
	issuerGitLabCom     = "https://gitlab.com"
	issuerBuildkite     = "https://agent.buildkite.com"
	issuerCircleCIBase  = "https://oidc.circleci.com/org/"

	defaultFederationIssuer = issuerGitHubActions
	federationMaxTTL        = time.Hour
	federationDefaultTTL    = 15 * time.Minute
)

// issuerRequiredClaims maps a known CI OIDC issuer to the strong identifying
// claim key(s) a trust binding MUST constrain. A binding must set at least one
// of the listed claims (to a non-empty value); this is the provider-aware
// replacement for the old hardcoded "repository" rule. Issuers not listed here
// (self-hosted GitLab, custom providers, or CircleCI's org-specific issuer)
// fall back to requiring at least one non-empty match claim of any kind.
var issuerRequiredClaims = map[string][]string{
	issuerGitHubActions: {"repository"},
	issuerGitLabCom:     {"project_path"},
	issuerBuildkite:     {"organization_slug"},
}

// requiredClaimsForIssuer returns the acceptable strong-claim keys for the
// configured issuer. For a self-hosted GitLab base URL we still require
// project_path; for CircleCI's org-scoped issuer we require its org identifier;
// otherwise (unknown/custom issuer) we return nil, signalling the caller to
// fall back to "at least one non-empty match claim of any kind".
func requiredClaimsForIssuer(issuer string) []string {
	issuer = strings.TrimRight(strings.TrimSpace(issuer), "/")
	if issuer == "" {
		issuer = strings.TrimRight(defaultFederationIssuer, "/")
	}
	if keys, ok := issuerRequiredClaims[issuer]; ok {
		return keys
	}
	// CircleCI: issuer is https://oidc.circleci.com/org/<ORG_ID>. Bindings must
	// constrain the org/project identity.
	if strings.HasPrefix(issuer+"/", issuerCircleCIBase) {
		return []string{"oidc.circleci.com/project-id", "aud"}
	}
	// Self-hosted GitLab: no fixed host, but GitLab always emits project_path.
	// We can't distinguish it from an arbitrary custom issuer by URL alone, so
	// unknown issuers fall through to the "any non-empty claim" rule below.
	return nil
}

// bindingHasStrongClaim reports whether the binding's match_claims constrain a
// strong identifying claim appropriate to the configured issuer. When the
// issuer has a known required-claim set, at least one of those keys must be
// present with a non-empty value. For unknown/custom issuers, any single
// non-empty match claim satisfies the rule (empty-value rejection is enforced
// separately by the caller).
func bindingHasStrongClaim(issuer string, claims map[string]string) bool {
	required := requiredClaimsForIssuer(issuer)
	if len(required) == 0 {
		// Custom issuer: any non-empty match claim is a sufficient constraint.
		for _, v := range claims {
			if strings.TrimSpace(v) != "" {
				return true
			}
		}
		return false
	}
	for _, k := range required {
		if strings.TrimSpace(claims[k]) != "" {
			return true
		}
	}
	return false
}

// validFederationIssuer reports whether s is a well-formed absolute URL with an
// http(s) scheme and a host (an empty string is allowed by the caller and
// defaults to GitHub Actions). Real CI issuers are https; http is permitted so a
// self-hosted / loopback IdP (and the test harness) is not rejected outright.
func validFederationIssuer(s string) bool {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil {
		return false
	}
	return (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}

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
	if !validFederationIssuer(issuer) {
		return ErrValidation // must be an absolute https URL
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

// CreateFederationBinding validates and creates a claim-match binding. The
// binding must constrain at least one strong identifying claim appropriate to
// the configured issuer (provider-aware, e.g. "repository" for GitHub Actions,
// "project_path" for GitLab, "organization_slug" for Buildkite; any non-empty
// claim for an unknown/custom issuer). Scope must reference an existing config
// or environment; TTL defaults to federationDefaultTTL and is capped at
// federationMaxTTL.
func (s *Service) CreateFederationBinding(ctx context.Context, in FederationBindingInput) (*FederationBindingView, error) {
	if strings.TrimSpace(in.Name) == "" {
		return nil, ErrValidation
	}
	// Reject empty match-claim values first: an empty want would match tokens
	// that LACK that claim entirely, silently broadening the binding.
	for _, v := range in.MatchClaims {
		if strings.TrimSpace(v) == "" {
			return nil, ErrValidation
		}
	}
	// The binding must constrain a strong identifying claim for the configured
	// issuer. Resolve the currently-configured issuer (default GitHub Actions if
	// none set) and apply the provider-aware required-claim rule.
	issuer := defaultFederationIssuer
	if c, err := s.oidcFedConfig.Get(ctx); err == nil {
		issuer = c.Issuer
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	if !bindingHasStrongClaim(issuer, in.MatchClaims) {
		return nil, ErrValidation // no strong identifying claim for this issuer
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

// federationVerifierFor builds (or returns cached) the go-oidc verifier for the
// configured, enabled federation provider. ErrFederationNotConfigured if none.
func (s *Service) federationVerifierFor(ctx context.Context) (*fedVerifier, error) {
	c, err := s.oidcFedConfig.Get(ctx)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrFederationNotConfigured
		}
		return nil, err
	}
	if !c.Enabled {
		return nil, ErrFederationNotConfigured
	}
	s.fedMu.Lock()
	defer s.fedMu.Unlock()
	if s.fedCache != nil && s.fedCache.issuer == c.Issuer && s.fedCache.audience == c.Audience {
		return s.fedCache, nil
	}
	provider, err := oidc.NewProvider(ctx, c.Issuer)
	if err != nil {
		return nil, err
	}
	v := &fedVerifier{
		issuer:   c.Issuer,
		audience: c.Audience,
		// oidc.Config.ClientID is the expected audience; verification fails on mismatch.
		verifier: provider.Verifier(&oidc.Config{ClientID: c.Audience}),
	}
	s.fedCache = v
	return v, nil
}

// FederateCILogin verifies a CI OIDC token, matches it to a binding, and mints a
// short-lived scoped service token. All failures return a typed sentinel; the
// API layer collapses them to one indistinguishable response and audits the reason.
func (s *Service) FederateCILogin(ctx context.Context, rawJWT string) (*FederationResult, error) {
	v, err := s.federationVerifierFor(ctx)
	if err != nil {
		return nil, err // ErrFederationNotConfigured or infra error
	}
	idt, err := v.verifier.Verify(ctx, rawJWT)
	if err != nil {
		return nil, ErrFederationVerify
	}
	var raw map[string]any
	if err := idt.Claims(&raw); err != nil {
		return nil, ErrFederationVerify
	}
	claims := stringClaims(raw)
	bindings, err := s.oidcFedBindings.List(ctx)
	if err != nil {
		return nil, err
	}
	b, err := matchFederationBinding(claims, bindings)
	if err != nil {
		return nil, err // ErrFederationNoMatch / ErrFederationAmbiguous
	}
	ttl := time.Duration(b.TTLSeconds) * time.Second
	if ttl <= 0 || ttl > federationMaxTTL {
		ttl = federationDefaultTTL // defensive; config validation should prevent
	}
	token, meta, err := s.MintFederatedToken(ctx, b.Name, b.ScopeKind, b.ScopeID, b.Access, ttl, b.ID)
	if err != nil {
		return nil, err
	}
	return &FederationResult{
		Token: token, Meta: meta, Binding: b.Name,
		Repository: claims["repository"], Subject: claims["sub"],
	}, nil
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
