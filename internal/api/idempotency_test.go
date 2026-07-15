package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/steveokay/janus-secrets/internal/auth"
)

type stubIdemStore struct {
	claimed   bool
	existing  *idemExisting
	claims    int
	completed int
	released  int
}

func (s *stubIdemStore) Claim(ctx context.Context, key, actor, endpoint, hash string) (bool, *idemExisting, error) {
	s.claims++
	return s.claimed, s.existing, nil
}
func (s *stubIdemStore) Complete(ctx context.Context, key, actor string, status int) error {
	s.completed++
	return nil
}
func (s *stubIdemStore) Release(ctx context.Context, key, actor string) error {
	s.released++
	return nil
}

type stubVerifier struct{}

func (stubVerifier) VerifySession(ctx context.Context, c string) (auth.Principal, error) {
	return auth.Principal{}, auth.ErrUnauthenticated
}
func (stubVerifier) VerifyServiceToken(ctx context.Context, raw string) (auth.Principal, *auth.TokenScope, error) {
	return auth.Principal{Kind: auth.KindServiceToken, ID: "tok-1"}, nil, nil
}

func idemReq(method, key string) *http.Request {
	req := httptest.NewRequest(method, "/v1/projects", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer x")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	return req
}

func TestIdempotency_PassthroughWithoutKey(t *testing.T) {
	st := &stubIdemStore{claimed: true}
	var ran bool
	h := idempotencyMiddleware(st, stubVerifier{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { ran = true; w.WriteHeader(201) }))
	h.ServeHTTP(httptest.NewRecorder(), idemReq("POST", ""))
	if !ran || st.claims != 0 {
		t.Fatalf("no key → passthrough, no claim: ran=%v claims=%d", ran, st.claims)
	}
}

func TestIdempotency_ClaimThenComplete(t *testing.T) {
	st := &stubIdemStore{claimed: true}
	rec := httptest.NewRecorder()
	h := idempotencyMiddleware(st, stubVerifier{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(201) }))
	h.ServeHTTP(rec, idemReq("POST", "k1"))
	if st.claims != 1 || st.completed != 1 || rec.Code != 201 {
		t.Fatalf("claim+complete on 2xx: claims=%d completed=%d code=%d", st.claims, st.completed, rec.Code)
	}
}

func TestIdempotency_ReleaseOnError(t *testing.T) {
	st := &stubIdemStore{claimed: true}
	h := idempotencyMiddleware(st, stubVerifier{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	h.ServeHTTP(httptest.NewRecorder(), idemReq("POST", "k1"))
	if st.released != 1 || st.completed != 0 {
		t.Fatalf("non-2xx → release: released=%d completed=%d", st.released, st.completed)
	}
}

func TestIdempotency_ReplayCompleted(t *testing.T) {
	st := &stubIdemStore{claimed: false, existing: &idemExisting{Endpoint: "POST /v1/projects", RequestHash: hashBody([]byte("{}")), StatusCode: 201}}
	var ran bool
	rec := httptest.NewRecorder()
	h := idempotencyMiddleware(st, stubVerifier{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { ran = true }))
	h.ServeHTTP(rec, idemReq("POST", "k1"))
	if ran || rec.Code != 201 || rec.Header().Get("Idempotency-Replayed") != "true" || !strings.Contains(rec.Body.String(), "idempotent_replay") {
		t.Fatalf("replay: ran=%v code=%d hdr=%q body=%s", ran, rec.Code, rec.Header().Get("Idempotency-Replayed"), rec.Body.String())
	}
}

func TestIdempotency_ConflictDifferentHash(t *testing.T) {
	st := &stubIdemStore{claimed: false, existing: &idemExisting{Endpoint: "POST /v1/projects", RequestHash: "different", StatusCode: 201}}
	rec := httptest.NewRecorder()
	h := idempotencyMiddleware(st, stubVerifier{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	h.ServeHTTP(rec, idemReq("POST", "k1"))
	if rec.Code != http.StatusConflict {
		t.Fatalf("hash mismatch → 409, got %d", rec.Code)
	}
}

func TestIdempotency_InProgress(t *testing.T) {
	st := &stubIdemStore{claimed: false, existing: &idemExisting{Endpoint: "POST /v1/projects", RequestHash: hashBody([]byte("{}")), StatusCode: 0}}
	rec := httptest.NewRecorder()
	h := idempotencyMiddleware(st, stubVerifier{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	h.ServeHTTP(rec, idemReq("POST", "k1"))
	if rec.Code != http.StatusConflict {
		t.Fatalf("pending → 409, got %d", rec.Code)
	}
}
