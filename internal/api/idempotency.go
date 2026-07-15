package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"

	"github.com/steveokay/janus-secrets/internal/store"
)

// idemExisting is the current record returned when a claim loses.
type idemExisting struct {
	Endpoint    string
	RequestHash string
	StatusCode  int // 0 = pending
}

// idemStore is the subset of *store.IdempotencyRepo the middleware needs (tests
// substitute a stub).
type idemStore interface {
	Claim(ctx context.Context, key, actor, endpoint, hash string) (bool, *idemExisting, error)
	Complete(ctx context.Context, key, actor string, status int) error
	Release(ctx context.Context, key, actor string) error
}

func hashBody(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func isMutating(m string) bool {
	return m == http.MethodPost || m == http.MethodPut || m == http.MethodDelete || m == http.MethodPatch
}

// idempotencyMiddleware honors a client-supplied Idempotency-Key on mutating
// /v1 requests. It stores only the final status (never the body), so once-shown
// secrets in a response never persist. Non-mutating methods, missing keys, and
// unauthenticated requests pass through untouched.
func idempotencyMiddleware(st idemStore, v authVerifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get("Idempotency-Key")
			if key == "" || !isMutating(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			p, _, err := resolvePrincipal(v, r)
			if err != nil || p.ID == "" {
				next.ServeHTTP(w, r) // unauthenticated: let downstream RequireAuth 401
				return
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				writeError(w, http.StatusRequestEntityTooLarge, CodeValidation, "request body too large")
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			endpoint := r.Method + " " + r.URL.Path
			hash := hashBody(body)

			claimed, existing, cerr := st.Claim(r.Context(), key, p.ID, endpoint, hash)
			if cerr != nil {
				writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
				return
			}
			if !claimed {
				switch {
				case existing.Endpoint != endpoint || existing.RequestHash != hash:
					writeError(w, http.StatusConflict, "idempotency_key_conflict",
						"Idempotency-Key reused with a different request")
				case existing.StatusCode == 0:
					writeError(w, http.StatusConflict, "idempotency_in_progress",
						"a request with this Idempotency-Key is still in progress")
				default:
					w.Header().Set("Idempotency-Replayed", "true")
					writeJSON(w, existing.StatusCode, map[string]any{"idempotent_replay": true})
				}
				return
			}
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sw, r)
			if sw.status >= 200 && sw.status < 300 {
				_ = st.Complete(r.Context(), key, p.ID, sw.status)
			} else {
				_ = st.Release(r.Context(), key, p.ID)
			}
		})
	}
}

// idemRepoAdapter adapts *store.IdempotencyRepo to the middleware's idemStore.
type idemRepoAdapter struct{ repo *store.IdempotencyRepo }

func (a idemRepoAdapter) Claim(ctx context.Context, key, actor, endpoint, hash string) (bool, *idemExisting, error) {
	claimed, rec, err := a.repo.Claim(ctx, key, actor, endpoint, hash)
	if err != nil || rec == nil {
		return claimed, nil, err
	}
	return claimed, &idemExisting{Endpoint: rec.Endpoint, RequestHash: rec.RequestHash, StatusCode: rec.StatusCode}, nil
}
func (a idemRepoAdapter) Complete(ctx context.Context, key, actor string, status int) error {
	return a.repo.Complete(ctx, key, actor, status)
}
func (a idemRepoAdapter) Release(ctx context.Context, key, actor string) error {
	return a.repo.Release(ctx, key, actor)
}
