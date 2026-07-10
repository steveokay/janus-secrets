package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/steveokay/janus-secrets/internal/auth"
	"github.com/steveokay/janus-secrets/internal/crypto"
)

// sessionCookieName is the UI session cookie.
const sessionCookieName = "janus_session"

type principalCtxKey struct{}

// PrincipalFrom returns the authenticated Principal placed by RequireAuth.
func PrincipalFrom(ctx context.Context) (auth.Principal, bool) {
	p, ok := ctx.Value(principalCtxKey{}).(auth.Principal)
	return p, ok
}

// authVerifier is the subset of *auth.Service the middleware needs (tests
// substitute fakes).
type authVerifier interface {
	VerifySession(ctx context.Context, cookie string) (auth.Principal, error)
	VerifyServiceToken(ctx context.Context, raw string) (auth.Principal, *auth.TokenScope, error)
}

type tokenScopeCtxKey struct{}

// tokenScopeFrom returns the verified token scope for a service-token request.
func tokenScopeFrom(ctx context.Context) *auth.TokenScope {
	s, _ := ctx.Value(tokenScopeCtxKey{}).(*auth.TokenScope)
	return s
}

// RequireAuth authenticates via Bearer service token or session cookie and
// injects the Principal (and, for tokens, the token scope) into the context.
// 401 on failure; a sealed keyring surfaces as 503 (credentials cannot verify
// while sealed).
func RequireAuth(v authVerifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var p auth.Principal
			var scope *auth.TokenScope
			var err error

			if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
				p, scope, err = v.VerifyServiceToken(r.Context(), strings.TrimPrefix(h, "Bearer "))
			} else if c, cErr := r.Cookie(sessionCookieName); cErr == nil {
				p, err = v.VerifySession(r.Context(), c.Value)
			} else {
				err = auth.ErrUnauthenticated
			}

			switch {
			case err == nil:
				ctx := context.WithValue(r.Context(), principalCtxKey{}, p)
				if scope != nil {
					ctx = context.WithValue(ctx, tokenScopeCtxKey{}, scope)
				}
				next.ServeHTTP(w, r.WithContext(ctx))
			case errors.Is(err, crypto.ErrSealed):
				writeError(w, http.StatusServiceUnavailable, CodeSealed,
					"server is sealed; unseal via /v1/sys/unseal")
			case errors.Is(err, auth.ErrSessionExpired):
				writeError(w, http.StatusUnauthorized, CodeSessionExpired,
					"session expired due to inactivity")
			case errors.Is(err, auth.ErrUnauthenticated), errors.Is(err, auth.ErrNotFound):
				writeError(w, http.StatusUnauthorized, CodeUnauthenticated, "authentication required")
			default:
				writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			}
		})
	}
}
