package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/steveokay/janus-secrets/internal/authz"
	"github.com/steveokay/janus-secrets/internal/store"
)

// can evaluates an authorization decision for the current request's principal.
func (s *Server) can(r *http.Request, action authz.Action, res authz.Resource) error {
	p, _ := PrincipalFrom(r.Context())
	var scope *authz.TokenScope
	if ts := tokenScopeFrom(r.Context()); ts != nil {
		scope = &authz.TokenScope{Kind: ts.Kind, ID: ts.ID, Access: ts.Access}
	}
	return s.authz.Can(r.Context(), p, scope, action, res)
}

// requireInstance gates a route on an instance-scoped action, recording a
// denied audit event (best-effort→fail-closed: if the denial's own audit write
// fails, the request 500s rather than proceed unaudited).
func (s *Server) requireInstance(action authz.Action, auditAction, auditResource string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := s.can(r, action, authz.Instance()); err != nil {
				if errors.Is(err, authz.ErrForbidden) {
					if aerr := s.record(r, auditAction, auditResource, "denied", CodeForbidden, ""); aerr != nil {
						writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
						return
					}
				}
				s.writeAuthzError(w, err)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// writeAuthzError maps an authz error to the envelope (403 forbidden or 500).
func (s *Server) writeAuthzError(w http.ResponseWriter, err error) {
	if errors.Is(err, authz.ErrForbidden) {
		writeError(w, http.StatusForbidden, CodeForbidden, "access denied")
		return
	}
	writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
}

// errBadScopeKind is returned by resolveScopeResource for an unknown scope kind.
var errBadScopeKind = errors.New("api: bad scope kind")

// resolveScopeResource builds the full scope chain (project/env/config) for a
// token scope so inheritance-based authorization works. store.ErrNotFound if
// the target does not exist; errBadScopeKind for an unknown kind.
func (s *Server) resolveScopeResource(ctx context.Context, kind, id string) (authz.Resource, error) {
	switch kind {
	case "environment":
		env, err := store.NewEnvironmentRepo(s.st).Get(ctx, id)
		if err != nil {
			return authz.Resource{}, err
		}
		return authz.Resource{ProjectID: env.ProjectID, EnvID: env.ID}, nil
	case "config":
		cfg, err := store.NewConfigRepo(s.st).Get(ctx, id)
		if err != nil {
			return authz.Resource{}, err
		}
		env, err := store.NewEnvironmentRepo(s.st).Get(ctx, cfg.EnvironmentID)
		if err != nil {
			return authz.Resource{}, err
		}
		return authz.Resource{ProjectID: env.ProjectID, EnvID: env.ID, ConfigID: cfg.ID}, nil
	case "transit":
		// Transit keys are instance-scoped; an empty id means "all keys".
		// No parent chain to resolve — authz uses the instance binding.
		return authz.Resource{TransitKey: id}, nil
	default:
		return authz.Resource{}, errBadScopeKind
	}
}
