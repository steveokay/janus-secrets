package auth

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/steveokay/janus-secrets/internal/nethard"
	"github.com/steveokay/janus-secrets/internal/store"
)

const (
	// Well-known CI OIDC issuer URLs. GitHub Actions is the default; GitLab.com
	// and Buildkite have fixed issuers. CircleCI's issuer is org-specific
	// (https://oidc.circleci.com/org/<ORG_ID>) so it has no fixed constant here.
	// Kubernetes cluster issuers are cluster-specific and have no constant at
	// all — they are identified by the "kubernetes" preset instead.
	issuerGitHubActions = "https://token.actions.githubusercontent.com"
	issuerGitLabCom     = "https://gitlab.com"
	issuerBuildkite     = "https://agent.buildkite.com"
	issuerCircleCIBase  = "https://oidc.circleci.com/org/"

	defaultFederationIssuer = issuerGitHubActions
	federationMaxTTL        = time.Hour
	federationDefaultTTL    = 15 * time.Minute
)

// Federation issuer presets. A preset selects the provider-aware required-claim
// rule for bindings pinned to that issuer. It is explicit (rather than sniffed
// from the URL) because Kubernetes cluster issuers have no recognisable shape:
// EKS uses https://oidc.eks.<region>.amazonaws.com/id/<hash>, GKE uses
// https://container.googleapis.com/v1/projects/..., self-hosted clusters use
// whatever `--service-account-issuer` says.
const (
	PresetGitHub     = "github"
	PresetGitLab     = "gitlab"
	PresetBuildkite  = "buildkite"
	PresetCircleCI   = "circleci"
	PresetKubernetes = "kubernetes"
	PresetCustom     = "custom"
)

var federationPresets = map[string]bool{
	"": true, PresetGitHub: true, PresetGitLab: true, PresetBuildkite: true,
	PresetCircleCI: true, PresetKubernetes: true, PresetCustom: true,
}

// Federation sentinels owned by this file (the shared errors.go carries the
// original four: NotConfigured / Verify / NoMatch / Ambiguous).
var (
	// ErrFederationIssuerUntrusted is returned when a token's `iss` is not one
	// of the configured, enabled trusted issuers.
	ErrFederationIssuerUntrusted = errors.New("auth: federation issuer not trusted")
	// ErrFederationClaims is returned when a verified claim set cannot be
	// projected to an unambiguous flat claim map (see oidc_federation_claims.go).
	ErrFederationClaims = errors.New("auth: federation token claims are ambiguous")
	// ErrFederationIssuerConflict is returned when the legacy single-issuer
	// admin endpoint is used on a deployment that trusts several issuers, or
	// when two stored rows resolve to the same issuer.
	ErrFederationIssuerConflict = errors.New("auth: multiple federation issuers configured")
)

// presetRequiredClaims maps a preset to the alternative claim groups a binding
// may satisfy. A binding is acceptable when EVERY key of at least ONE group is
// pinned to a non-empty value. Groups are OR-ed, keys within a group AND-ed.
var presetRequiredClaims = map[string][][]string{
	PresetGitHub:    {{"repository"}},
	PresetGitLab:    {{"project_path"}},
	PresetBuildkite: {{"organization_slug"}},
	PresetCircleCI:  {{"oidc.circleci.com/project-id"}, {"aud"}},
	// Kubernetes: a binding must pin the service-account identity — either the
	// exact `sub` (system:serviceaccount:<ns>:<name>) or the namespace AND the
	// service-account name from the flattened kubernetes.io object. Pinning only
	// `aud` (which every workload in the cluster can request) is not enough.
	PresetKubernetes: {
		{"sub"},
		{"kubernetes.io.namespace", "kubernetes.io.serviceaccount.name"},
	},
}

// issuerRequiredClaims maps a known CI OIDC issuer to the strong identifying
// claim key(s) a trust binding MUST constrain when no preset is recorded (rows
// written before presets existed). Issuers not listed here fall back to
// requiring at least one non-empty match claim of any kind.
var issuerRequiredClaims = map[string]string{
	issuerGitHubActions: PresetGitHub,
	issuerGitLabCom:     PresetGitLab,
	issuerBuildkite:     PresetBuildkite,
}

// normalizeIssuer trims surrounding space and trailing slashes so issuer
// comparisons ("is this token's iss one I trust?") are stable. Discovery still
// uses the stored string verbatim, and go-oidc re-checks the token's `iss`
// against it, so normalization only ever widens *lookup*, never trust.
func normalizeIssuer(s string) string {
	return strings.TrimRight(strings.TrimSpace(s), "/")
}

// requiredClaimGroups returns the alternative claim groups acceptable for an
// issuer. The preset wins when set; otherwise the issuer URL is sniffed (legacy
// rows). nil means "no provider-specific rule" — the caller then falls back to
// "at least one non-empty match claim of any kind".
func requiredClaimGroups(issuer, preset string) [][]string {
	switch preset {
	case PresetCustom:
		return nil
	case "":
		// fall through to URL sniffing below
	default:
		return presetRequiredClaims[preset]
	}
	issuer = normalizeIssuer(issuer)
	if issuer == "" {
		issuer = normalizeIssuer(defaultFederationIssuer)
	}
	if p, ok := issuerRequiredClaims[issuer]; ok {
		return presetRequiredClaims[p]
	}
	// CircleCI: issuer is https://oidc.circleci.com/org/<ORG_ID>. Bindings must
	// constrain the org/project identity.
	if strings.HasPrefix(issuer+"/", issuerCircleCIBase) {
		return presetRequiredClaims[PresetCircleCI]
	}
	// Self-hosted GitLab / Kubernetes / arbitrary custom issuers can't be told
	// apart by URL alone: fall through to the "any non-empty claim" rule.
	return nil
}

// bindingHasStrongClaim reports whether the binding's match_claims constrain a
// strong identifying claim appropriate to its issuer. When the issuer/preset has
// a known required-claim rule, every key of at least one alternative group must
// be present with a non-empty value. For unknown/custom issuers, any single
// non-empty match claim satisfies the rule (empty-value rejection is enforced
// separately by the caller).
func bindingHasStrongClaim(issuer, preset string, claims map[string]string) bool {
	groups := requiredClaimGroups(issuer, preset)
	if len(groups) == 0 {
		// Custom issuer: any non-empty match claim is a sufficient constraint.
		for _, v := range claims {
			if strings.TrimSpace(v) != "" {
				return true
			}
		}
		return false
	}
	for _, g := range groups {
		ok := len(g) > 0
		for _, k := range g {
			if strings.TrimSpace(claims[k]) == "" {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// maxFederationCACertBytes bounds a stored CA bundle. A real bundle is a handful
// of certificates — a few kilobytes — so 64 KiB is generous. The cap exists so a
// pathological body cannot be parked in the config table and re-parsed on every
// cold verifier build; the HTTP body cap is a blunter instrument that applies to
// the whole request, not to this one field.
const maxFederationCACertBytes = 64 * 1024

// ErrFederationCACertInvalid is returned when a supplied CA bundle is not usable
// PEM. The API maps it to 400 like ErrValidation, but as its own sentinel so the
// message can name the field: "invalid federation issuer" is useless feedback for
// a pasted certificate. Rejecting at WRITE time is the point — a bundle first
// checked when a token arrives turns an operator's typo into an
// indistinguishable federation_denied on some workload's cold start, hours later.
var ErrFederationCACertInvalid = errors.New("auth: federation ca_cert is not valid PEM")

// federationCAPool parses a PEM CA bundle into a pool.
//
// TRUST DECISION — the returned pool REPLACES the system roots for the issuer it
// belongs to; it does not add to them (x509.SystemCertPool is deliberately not
// used as the starting point). Three reasons:
//
//   - It matches the Kubernetes SYNC provider (internal/secretsync.defaultK8sClient),
//     which has accepted an explicit ca_cert since it was written. Two features
//     dialling the same API server should not disagree about what trust means.
//   - It is the STRICTER reading. A federation issuer is one specific host whose
//     certificate has one legitimate signer; pinning it means a mis-issuance by
//     any of the ~150 public roots cannot be used to impersonate that issuer.
//   - Additive trust would be silently weaker in the exact case this feature
//     exists for: a private cluster CA plus every public CA is a strictly larger
//     surface than the cluster CA alone, for no operator benefit — an operator
//     who wants the public roots simply leaves ca_cert empty.
//
// There is no InsecureSkipVerify path, and no configuration that produces one.
func federationCAPool(caPEM string) (*x509.CertPool, error) {
	caPEM = strings.TrimSpace(caPEM)
	if caPEM == "" {
		return nil, nil // system roots
	}
	if len(caPEM) > maxFederationCACertBytes {
		return nil, ErrFederationCACertInvalid
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(caPEM)) {
		// AppendCertsFromPEM is all-or-nothing per bundle only in the sense that
		// it reports whether ANY certificate was added; a bundle that yields none
		// would leave an empty pool that rejects every chain, so treat it as a
		// config error rather than a silently unusable issuer.
		return nil, ErrFederationCACertInvalid
	}
	return pool, nil
}

// validateFederationCACert checks a bundle at WRITE time and returns nil for an
// empty (system-roots) bundle. The parsed pool is discarded: the verifier builds
// its own, and holding a pool per stored row would go stale the moment the row
// changed.
func validateFederationCACert(caPEM string) error {
	_, err := federationCAPool(caPEM)
	return err
}

// federationHTTPClient returns the SSRF-hardened client to use for one issuer's
// discovery + JWKS fetches.
//
// With no CA bundle it returns the SHARED process client — one client, one
// connection pool, unchanged behaviour for every public issuer. With a bundle it
// must build a per-issuer client, because trust roots live on the transport and
// the shared client is used by every other issuer (and by human OIDC login):
// mutating its TLSClientConfig would silently re-anchor all of them.
//
// The per-issuer client is built through nethard.SafeHTTPClient with the LIVE
// process policy, so it dials through the same connect-time SSRF guard as
// everything else — a custom CA changes which certificate is acceptable, never
// which address may be dialled.
//
// A per-issuer client is created once per cold verifier build, not per exchange
// (the verifier cache holds it). A client dropped by a cache flush releases its
// idle connections on the transport's IdleConnTimeout.
func (s *Service) federationHTTPClient(caPEM string) (*http.Client, error) {
	pool, err := federationCAPool(caPEM)
	if err != nil {
		return nil, err
	}
	if pool == nil {
		return s.oidcHTTP, nil
	}
	hc := nethard.SafeHTTPClient(oidcHTTPTimeout, nethard.Process())
	tr, ok := hc.Transport.(*http.Transport)
	if !ok {
		// SafeHTTPClient documents an *http.Transport precisely so callers can
		// pin roots. Fail closed rather than fall back to a client that would
		// verify against the system roots the operator chose to replace.
		return nil, ErrFederationCACertInvalid
	}
	tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	return hc, nil
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

// FederationConfigInput is the legacy single-issuer trust-provider input. It
// replaces the whole trusted-issuer set with one entry; see
// FederationIssuerInput for the multi-issuer path.
type FederationConfigInput struct {
	Issuer   string // empty → defaultFederationIssuer
	Audience string // required, non-empty
	Preset   string // optional provider preset ("", github, …, kubernetes)
	CACert   string // optional PEM CA bundle for this issuer's TLS; empty → system roots
	Enabled  bool
}

// FederationConfigView is the non-secret view of a trusted issuer.
//
// CACert is returned, not write-only. A CA certificate is public material — the
// issuer presents it to every client that connects — and the whole federation
// config is a public-key trust relationship with nothing to hide. Returning it
// also means the UI can edit an issuer without an operator re-pasting the bundle
// from memory, which is how a wrong bundle gets saved.
type FederationConfigView struct {
	ID       string `json:"id,omitempty"`
	Issuer   string `json:"issuer"`
	Audience string `json:"audience"`
	Preset   string `json:"preset"`
	CACert   string `json:"ca_cert,omitempty"`
	Enabled  bool   `json:"enabled"`
}

// FederationIssuerInput adds or updates ONE trusted issuer, leaving the others
// untouched. Issuer is required (no implicit default: with several issuers in
// play, an implicit one is a footgun).
type FederationIssuerInput struct {
	Issuer   string
	Audience string
	Preset   string
	CACert   string
	Enabled  bool
}

// FederationBindingInput describes a claim-match binding that mints a scoped,
// time-limited service token for a federated machine identity.
type FederationBindingInput struct {
	Name string
	// Issuer pins the binding to one trusted issuer. Empty resolves to the sole
	// configured issuer (or the historical default when none is configured);
	// with several issuers configured it must be given explicitly.
	Issuer      string
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
	Issuer      string            `json:"issuer"`
	MatchClaims map[string]string `json:"match_claims"`
	ScopeKind   string            `json:"scope_kind"`
	ScopeID     string            `json:"scope_id"`
	Access      string            `json:"access"`
	TTLSeconds  int               `json:"ttl_seconds"`
	Enabled     bool              `json:"enabled"`
}

// FederationResult is the successful exchange outcome.
type FederationResult struct {
	Token      string
	Meta       TokenMeta
	Binding    string
	Issuer     string
	Repository string
	Subject    string
}

// fedVerifier caches the go-oidc verifier for ONE trusted issuer.
//
// caCert is the CA bundle the verifier's HTTP client was built with. It is part
// of the cache identity, not decoration: the JWKS RemoteKeySet behind a verifier
// holds the client it was built with for the life of the verifier, so a verifier
// created against the old trust anchor would keep using it. Every mutation path
// calls invalidateFederationVerifier(), but the comparison is what makes the
// cache correct on its own — including when a row changes underneath this
// process (a restore, a second instance, a direct DB edit).
type fedVerifier struct {
	issuer   string
	audience string
	caCert   string
	verifier *oidc.IDTokenVerifier
}

// SetFederationConfig replaces the trusted-issuer set with this single issuer —
// the historical single-provider behaviour. Audience is required; an empty
// issuer defaults to the GitHub Actions OIDC issuer. It refuses to act once
// several issuers are trusted (ErrFederationIssuerConflict) so it can never
// silently drop a trust relationship an admin added through the multi-issuer
// endpoint. Invalidates cached verifiers.
func (s *Service) SetFederationConfig(ctx context.Context, in FederationConfigInput) error {
	if strings.TrimSpace(in.Audience) == "" {
		return ErrValidation
	}
	if !federationPresets[in.Preset] {
		return ErrValidation
	}
	issuer := normalizeIssuer(in.Issuer)
	if issuer == "" {
		issuer = normalizeIssuer(defaultFederationIssuer)
	}
	if !validFederationIssuer(issuer) {
		return ErrValidation // must be an absolute http(s) URL
	}
	if err := validateFederationCACert(in.CACert); err != nil {
		return err
	}
	existing, err := s.oidcFedConfig.List(ctx)
	if err != nil {
		return err
	}
	if len(existing) > 1 {
		return ErrFederationIssuerConflict
	}
	if err := s.oidcFedConfig.Put(ctx, store.OIDCFederationConfig{
		Issuer: issuer, Audience: in.Audience, Preset: in.Preset,
		CACert: strings.TrimSpace(in.CACert), Enabled: in.Enabled,
	}); err != nil {
		return err
	}
	s.invalidateFederationVerifier()
	return nil
}

// GetFederationConfig returns the oldest trusted issuer, or ErrNotFound if none
// has been set. Legacy single-issuer read; use ListFederationIssuers to see all.
func (s *Service) GetFederationConfig(ctx context.Context) (*FederationConfigView, error) {
	c, err := s.oidcFedConfig.Get(ctx)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return fedConfigView(c), nil
}

// DeleteFederationConfig removes every trusted issuer. Bindings survive but can
// no longer match anything (fail closed) until their issuer is trusted again.
func (s *Service) DeleteFederationConfig(ctx context.Context) error {
	if err := s.oidcFedConfig.Delete(ctx); err != nil {
		return err
	}
	s.invalidateFederationVerifier()
	return nil
}

// ListFederationIssuers returns every trusted issuer, oldest first.
func (s *Service) ListFederationIssuers(ctx context.Context) ([]FederationConfigView, error) {
	cs, err := s.oidcFedConfig.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]FederationConfigView, 0, len(cs))
	for i := range cs {
		out = append(out, *fedConfigView(&cs[i]))
	}
	return out, nil
}

// PutFederationIssuer adds a trusted issuer or updates the one with the same
// issuer URL, leaving every other trusted issuer alone. Both a CI provider and a
// Kubernetes cluster can therefore be trusted at once.
func (s *Service) PutFederationIssuer(ctx context.Context, in FederationIssuerInput) (*FederationConfigView, error) {
	issuer := normalizeIssuer(in.Issuer)
	if issuer == "" || !validFederationIssuer(issuer) {
		return nil, ErrValidation
	}
	if strings.TrimSpace(in.Audience) == "" {
		return nil, ErrValidation
	}
	if !federationPresets[in.Preset] {
		return nil, ErrValidation
	}
	if err := validateFederationCACert(in.CACert); err != nil {
		return nil, err
	}
	c, err := s.oidcFedConfig.Upsert(ctx, store.OIDCFederationConfig{
		Issuer: issuer, Audience: in.Audience, Preset: in.Preset,
		CACert: strings.TrimSpace(in.CACert), Enabled: in.Enabled,
	})
	if err != nil {
		return nil, err
	}
	s.invalidateFederationVerifier()
	return fedConfigView(c), nil
}

// DeleteFederationIssuer removes one trusted issuer. Bindings pinned to it stay
// but become inert (they can never match again until the issuer is re-added).
func (s *Service) DeleteFederationIssuer(ctx context.Context, id string) error {
	if err := s.oidcFedConfig.DeleteByID(ctx, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}
	s.invalidateFederationVerifier()
	return nil
}

// CreateFederationBinding validates and creates a claim-match binding. The
// binding is pinned to exactly one trusted issuer and must constrain at least
// one strong identifying claim appropriate to that issuer (e.g. "repository"
// for GitHub Actions, "project_path" for GitLab, `sub` or
// kubernetes.io.namespace + kubernetes.io.serviceaccount.name for Kubernetes;
// any non-empty claim for an unknown/custom issuer). Scope must reference an
// existing config or environment; TTL defaults to federationDefaultTTL and is
// capped at federationMaxTTL.
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
	issuer, preset, err := s.resolveBindingIssuer(ctx, in.Issuer)
	if err != nil {
		return nil, err
	}
	if !bindingHasStrongClaim(issuer, preset, in.MatchClaims) {
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
		Name: in.Name, Issuer: issuer, MatchClaims: in.MatchClaims, ScopeKind: in.ScopeKind,
		ScopeID: in.ScopeID, Access: in.Access, TTLSeconds: ttl, Enabled: in.Enabled,
	})
	if err != nil {
		return nil, err
	}
	return fedBindingView(b), nil
}

// resolveBindingIssuer picks the issuer a new binding is pinned to and returns
// it with that issuer's preset. An explicit issuer must be one of the trusted
// issuers (when any are configured). An empty issuer resolves to the sole
// trusted issuer, or — when none is configured yet — to the historical default;
// with several trusted issuers it is a validation error, because guessing which
// one an admin meant is exactly how a binding ends up on the wrong trust anchor.
func (s *Service) resolveBindingIssuer(ctx context.Context, want string) (issuer, preset string, err error) {
	issuers, err := s.oidcFedConfig.List(ctx)
	if err != nil {
		return "", "", err
	}
	want = normalizeIssuer(want)
	if want != "" {
		if !validFederationIssuer(want) {
			return "", "", ErrValidation
		}
		for i := range issuers {
			if normalizeIssuer(issuers[i].Issuer) == want {
				return want, issuers[i].Preset, nil
			}
		}
		if len(issuers) > 0 {
			return "", "", ErrValidation // not a trusted issuer
		}
		return want, "", nil
	}
	switch len(issuers) {
	case 0:
		return normalizeIssuer(defaultFederationIssuer), "", nil
	case 1:
		return normalizeIssuer(issuers[0].Issuer), issuers[0].Preset, nil
	default:
		return "", "", ErrValidation // ambiguous: name the issuer
	}
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
// trusted issuer whose URL equals tokenIssuer. Selection is by the token's own
// `iss`: a token can never be routed to a different (possibly more permissive)
// issuer's verifier, and the verifier it does reach re-checks `iss` and the
// audience and validates the signature against that issuer's JWKS.
func (s *Service) federationVerifierFor(ctx context.Context, tokenIssuer string) (*fedVerifier, error) {
	cfgs, err := s.oidcFedConfig.List(ctx)
	if err != nil {
		return nil, err
	}
	want := normalizeIssuer(tokenIssuer)
	if want == "" {
		return nil, ErrFederationVerify // no issuer to match; never fall through
	}
	var match *store.OIDCFederationConfig
	enabled := 0
	for i := range cfgs {
		c := &cfgs[i]
		if !c.Enabled {
			continue
		}
		enabled++
		if normalizeIssuer(c.Issuer) != want {
			continue
		}
		if match != nil {
			// Two rows resolve to the same issuer (only possible for legacy
			// rows differing by a trailing slash): fail closed rather than pick.
			return nil, ErrFederationIssuerConflict
		}
		match = c
	}
	if match == nil {
		if enabled == 0 {
			return nil, ErrFederationNotConfigured
		}
		return nil, ErrFederationIssuerUntrusted
	}
	s.fedMu.Lock()
	defer s.fedMu.Unlock()
	// The CA bundle is part of the cache identity: an operator who corrects a
	// wrong bundle must see the correction on the very next exchange, and one who
	// REMOVES a bundle must stop trusting that private CA immediately.
	if v, ok := s.fedCache[want]; ok && v.issuer == match.Issuer &&
		v.audience == match.Audience && v.caCert == match.CACert {
		return v, nil
	}
	// A per-issuer client when this issuer pins a CA, the shared process client
	// otherwise. Both are SSRF-guarded.
	hc, err := s.federationHTTPClient(match.CACert)
	if err != nil {
		// A bundle that no longer parses (hand-edited row, restored dump). Fail
		// closed: never fall back to the system roots the operator replaced.
		return nil, err
	}
	// Discovery + the lazily-built JWKS RemoteKeySet both use the client carried
	// on this context, so the SSRF-hardened client covers both fetches (M-4/I-4)
	// — and, when set, the issuer's own CA bundle covers both too.
	provider, err := oidc.NewProvider(oidc.ClientContext(ctx, hc), match.Issuer)
	if err != nil {
		return nil, err
	}
	v := &fedVerifier{
		issuer:   match.Issuer,
		audience: match.Audience,
		caCert:   match.CACert,
		// oidc.Config.ClientID is the expected audience; verification fails on mismatch.
		verifier: provider.Verifier(&oidc.Config{ClientID: match.Audience}),
	}
	if s.fedCache == nil {
		s.fedCache = make(map[string]*fedVerifier, len(cfgs))
	}
	s.fedCache[want] = v
	return v, nil
}

// FederateCILogin verifies a federated machine-identity token (CI job or
// Kubernetes service account), matches it to a binding pinned to the same
// issuer, and mints a short-lived scoped service token. All failures return a
// typed sentinel; the API layer collapses them to one indistinguishable
// response and audits the reason.
func (s *Service) FederateCILogin(ctx context.Context, rawJWT string) (*FederationResult, error) {
	// Routing hint only — unverified. The verifier picked here re-checks `iss`.
	hint, err := unverifiedIssuer(rawJWT)
	if err != nil {
		return nil, err
	}
	v, err := s.federationVerifierFor(ctx, hint)
	if err != nil {
		return nil, err // not configured / untrusted issuer / infra error
	}
	idt, err := v.verifier.Verify(ctx, rawJWT)
	if err != nil {
		return nil, ErrFederationVerify
	}
	// Defence in depth: go-oidc already pins `iss` to this provider, so this can
	// only fail if that ever changes — but the whole multi-issuer model rests on
	// the signed `iss` equalling the trusted issuer whose JWKS verified it.
	if normalizeIssuer(idt.Issuer) != normalizeIssuer(v.issuer) {
		return nil, ErrFederationVerify
	}
	var raw map[string]any
	if err := idt.Claims(&raw); err != nil {
		return nil, ErrFederationVerify
	}
	claims, err := flattenClaims(raw)
	if err != nil {
		return nil, err // ErrFederationClaims
	}
	bindings, err := s.oidcFedBindings.List(ctx)
	if err != nil {
		return nil, err
	}
	b, err := matchFederationBinding(claims, bindingsForIssuer(v.issuer, bindings))
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
		Token: token, Meta: meta, Binding: b.Name, Issuer: v.issuer,
		Repository: claims["repository"], Subject: claims["sub"],
	}, nil
}

func fedConfigView(c *store.OIDCFederationConfig) *FederationConfigView {
	return &FederationConfigView{
		ID: c.ID, Issuer: c.Issuer, Audience: c.Audience, Preset: c.Preset,
		CACert: c.CACert, Enabled: c.Enabled,
	}
}

func fedBindingView(b *store.OIDCFederationBinding) *FederationBindingView {
	return &FederationBindingView{
		ID: b.ID, Name: b.Name, Issuer: b.Issuer, MatchClaims: b.MatchClaims,
		ScopeKind: b.ScopeKind, ScopeID: b.ScopeID, Access: b.Access,
		TTLSeconds: b.TTLSeconds, Enabled: b.Enabled,
	}
}

// bindingsForIssuer keeps only the bindings pinned to the issuer that actually
// signed the token. This is what stops a token from one trusted issuer
// satisfying a binding written for another (a Kubernetes SA token must not be
// able to claim a GitHub Actions binding, and vice versa).
func bindingsForIssuer(issuer string, bindings []store.OIDCFederationBinding) []store.OIDCFederationBinding {
	want := normalizeIssuer(issuer)
	out := make([]store.OIDCFederationBinding, 0, len(bindings))
	for i := range bindings {
		if normalizeIssuer(bindings[i].Issuer) == want {
			out = append(out, bindings[i])
		}
	}
	return out
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
		// Two-value lookup on purpose: a plain tokenClaims[k] yields "" for an
		// ABSENT claim, so a required value of "" would be satisfied by a token
		// that lacks the claim entirely — turning a constraint into a wildcard.
		// CreateFederationBinding already rejects empty match-claim values, but
		// this matcher must not depend on a validator running somewhere else.
		got, ok := tokenClaims[k]
		if !ok || got != v {
			return false
		}
	}
	return true
}
