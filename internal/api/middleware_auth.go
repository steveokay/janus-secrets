package api

import (
	"context"
	"errors"
	"net"
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

// resolvePrincipal authenticates via Bearer service token or session cookie,
// returning the Principal (and token scope, if any). It does NOT write a
// response; callers decide how to handle errors. Shared by RequireAuth and the
// idempotency middleware.
func resolvePrincipal(v authVerifier, r *http.Request) (auth.Principal, *auth.TokenScope, error) {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return v.VerifyServiceToken(r.Context(), strings.TrimPrefix(h, "Bearer "))
	}
	if c, cErr := r.Cookie(sessionCookieName); cErr == nil {
		p, err := v.VerifySession(r.Context(), c.Value)
		return p, nil, err
	}
	return auth.Principal{}, nil, auth.ErrUnauthenticated
}

// clientIP extracts the host portion of r.RemoteAddr (stripping the port), the
// SAME source the audit log uses (see internal/api/audit.go, which records
// r.RemoteAddr verbatim). X-Forwarded-For is intentionally NOT trusted here,
// consistent with the audit IP; behind a proxy, operators terminate at a
// trusted hop. Returns the raw RemoteAddr if it has no host:port form.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ipAllowed reports whether ip is inside at least one of the CIDRs. An empty
// allowlist means "any IP" → always allowed. An unparseable client IP with a
// non-empty allowlist is denied (fail closed). CIDRs are assumed pre-validated
// (mint/update validate via net.ParseCIDR); an unparseable CIDR is skipped.
func ipAllowed(ip string, allowlist []string) bool {
	if len(allowlist) == 0 {
		return true
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	for _, c := range allowlist {
		_, ipnet, err := net.ParseCIDR(c)
		if err != nil {
			continue
		}
		if ipnet.Contains(parsed) {
			return true
		}
	}
	return false
}

// newIPRecorder is the (optional) hook RequireAuth calls after a service token
// authenticates: it records the (token, ip) sighting and, when the pair is new,
// emits a value-free audit event. Wired by the Server in production; nil in
// unit-test middleware. Implemented by *Server (see recordTokenIP).
type newIPRecorder interface {
	recordTokenIP(r *http.Request, tokenID, ip string)
}

// RequireAuth authenticates via Bearer service token or session cookie and
// injects the Principal (and, for tokens, the token scope) into the context.
// 401 on failure; a sealed keyring surfaces as 503 (credentials cannot verify
// while sealed).
//
// For SERVICE-TOKEN auth only (not session/cookie auth), it additionally:
//   - enforces the token's IP allowlist against the client IP, rejecting an
//     outside-the-allowlist request with 403; and
//   - records the client IP for new-IP anomaly surfacing (best-effort, via the
//     optional hook), emitting a value-free token.new_ip audit event on a
//     genuinely new (token, ip) pair.
//
// The client IP is captured the same way the audit log captures it (host of
// r.RemoteAddr; X-Forwarded-For untrusted).
func RequireAuth(v authVerifier, hook ...newIPRecorder) func(http.Handler) http.Handler {
	var rec newIPRecorder
	if len(hook) > 0 {
		rec = hook[0]
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, scope, err := resolvePrincipal(v, r)

			switch {
			case err == nil:
				// Service-token IP enforcement + new-IP tracking. Runs ONLY for
				// token auth (scope != nil); session/cookie auth is unaffected.
				if scope != nil && p.Kind == auth.KindServiceToken {
					ip := clientIP(r)
					if !ipAllowed(ip, scope.IPAllowlist) {
						writeError(w, http.StatusForbidden, CodeForbidden,
							"client IP not in token allowlist")
						return
					}
					if rec != nil {
						rec.recordTokenIP(r, p.ID, ip)
					}
				}
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
