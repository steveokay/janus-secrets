package api

import (
	"errors"
	"net/http"

	"github.com/steveokay/janus-secrets/internal/authz"
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

// requireInstance gates a route on an instance-scoped action.
func (s *Server) requireInstance(action authz.Action) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := s.can(r, action, authz.Instance()); err != nil {
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
