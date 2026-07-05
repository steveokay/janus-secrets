package auth

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/steveokay/janus-secrets/internal/store"
)

// svcTokenPrefix namespaces service tokens; Phase-2 federated credentials get
// their own janus_<type>_ prefixes.
// #nosec G101 -- public namespace prefix shown at the start of every service
// token, not a hardcoded credential; gosec's entropy heuristic false-positives.
const svcTokenPrefix = "janus_svc_"

// TokenScope is a verified service token's scope, for authorization.
type TokenScope struct {
	Kind   string // scope_kind: "config" | "environment" | "transit"
	ID     string // scope_id ("" for a transit token targeting all keys)
	Access string // "read" | "readwrite" (config/env); "use" | "manage" (transit)
}

// TokenMeta is service-token metadata. It has no raw-token field by design —
// list/inspect paths structurally cannot leak the credential.
type TokenMeta struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	ScopeKind string     `json:"scope_kind"`
	ScopeID   string     `json:"scope_id"`
	Access    string     `json:"access"`
	CreatedBy string     `json:"created_by"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

func metaOf(t *store.ServiceToken) TokenMeta {
	return TokenMeta{
		ID: t.ID, Name: t.Name, ScopeKind: t.ScopeKind, ScopeID: t.ScopeID,
		Access: t.Access, CreatedBy: t.CreatedBy, CreatedAt: t.CreatedAt,
		ExpiresAt: t.ExpiresAt, RevokedAt: t.RevokedAt,
	}
}

// MintServiceToken creates a scoped token. The raw value is returned exactly
// once; only its HMAC is stored. Scope existence is validated here;
// enforcement is the RBAC/API milestones' job. ttl nil means long-lived.
func (s *Service) MintServiceToken(ctx context.Context, by Principal, name,
	scopeKind, scopeID, access string, ttl *time.Duration) (string, TokenMeta, error) {
	if strings.TrimSpace(name) == "" {
		return "", TokenMeta{}, ErrValidation
	}
	// Access validation depends on scope kind: config/env use read/readwrite;
	// transit uses use/manage.
	validAccess := access == "read" || access == "readwrite"
	if scopeKind == "transit" {
		validAccess = access == "use" || access == "manage"
	}
	if !validAccess {
		return "", TokenMeta{}, ErrValidation
	}
	switch scopeKind {
	case "config":
		if _, err := s.configs.Get(ctx, scopeID); err != nil {
			return "", TokenMeta{}, scopeErr(err)
		}
	case "environment":
		if _, err := s.envs.Get(ctx, scopeID); err != nil {
			return "", TokenMeta{}, scopeErr(err)
		}
	case "transit":
		if scopeID != "" { // "" = all transit keys (persisted as NULL scope_id)
			// Validate and store the restriction by key NAME so it matches the
			// name-based enforcement in authz.tokenAllows (scope.ID vs the
			// /{name} route's Resource.TransitKey).
			if _, err := s.transit.GetByName(ctx, scopeID); err != nil {
				return "", TokenMeta{}, scopeErr(err)
			}
		}
	default:
		return "", TokenMeta{}, ErrValidation
	}
	if by.Kind != KindUser {
		// created_by references users; token-minted-by-token arrives with RBAC.
		return "", TokenMeta{}, ErrValidation
	}

	random, err := randToken(32)
	if err != nil {
		return "", TokenMeta{}, err
	}
	raw := svcTokenPrefix + random
	key, err := s.hmacKey(ctx)
	if err != nil {
		return "", TokenMeta{}, err
	}
	defer zeroize(key)
	var expiresAt *time.Time
	if ttl != nil {
		t := time.Now().Add(*ttl)
		expiresAt = &t
	}
	tok, err := s.tokens.Create(ctx, name, mac(key, raw), by.ID, scopeKind, scopeID, access, expiresAt)
	if err != nil {
		return "", TokenMeta{}, err
	}
	return raw, metaOf(tok), nil
}

func scopeErr(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	}
	return err
}

// VerifyServiceToken resolves a raw bearer token to a Principal and its scope.
func (s *Service) VerifyServiceToken(ctx context.Context, raw string) (Principal, *TokenScope, error) {
	if !strings.HasPrefix(raw, svcTokenPrefix) || len(raw) == len(svcTokenPrefix) {
		return Principal{}, nil, ErrUnauthenticated
	}
	key, err := s.hmacKey(ctx)
	if err != nil {
		return Principal{}, nil, err // crypto.ErrSealed passes through
	}
	defer zeroize(key)
	tok, err := s.tokens.GetByHMAC(ctx, mac(key, raw))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return Principal{}, nil, ErrUnauthenticated
		}
		return Principal{}, nil, err
	}
	if tok.RevokedAt != nil {
		return Principal{}, nil, ErrUnauthenticated
	}
	if tok.ExpiresAt != nil && time.Now().After(*tok.ExpiresAt) {
		return Principal{}, nil, ErrUnauthenticated
	}
	scope := &TokenScope{Kind: tok.ScopeKind, ID: tok.ScopeID, Access: tok.Access}
	return Principal{Kind: KindServiceToken, ID: tok.ID, Name: tok.Name}, scope, nil
}

// ListTokens returns metadata for all tokens (raw values are unrecoverable).
func (s *Service) ListTokens(ctx context.Context) ([]TokenMeta, error) {
	list, err := s.tokens.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]TokenMeta, 0, len(list))
	for _, t := range list {
		out = append(out, metaOf(t))
	}
	return out, nil
}

// RevokeToken revokes by id. ErrNotFound if absent or already revoked.
func (s *Service) RevokeToken(ctx context.Context, id string) error {
	if err := s.tokens.Revoke(ctx, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}
	return nil
}
