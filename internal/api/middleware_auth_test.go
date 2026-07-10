package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/steveokay/janus-secrets/internal/auth"
	"github.com/steveokay/janus-secrets/internal/crypto"
)

// fakeVerifier implements authVerifier for middleware tests.
type fakeVerifier struct {
	principal auth.Principal
	scope     *auth.TokenScope
	err       error
}

func (f *fakeVerifier) VerifySession(context.Context, string) (auth.Principal, error) {
	return f.principal, f.err
}
func (f *fakeVerifier) VerifyServiceToken(context.Context, string) (auth.Principal, *auth.TokenScope, error) {
	return f.principal, f.scope, f.err
}

func TestRequireAuth(t *testing.T) {
	okPrincipal := auth.Principal{Kind: auth.KindUser, ID: "u1", Name: "a@b.c"}
	probe := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := PrincipalFrom(r.Context())
		if !ok || p != okPrincipal {
			t.Errorf("principal missing in context: %+v ok=%v", p, ok)
		}
		w.WriteHeader(http.StatusOK)
	})

	// No credential → 401.
	h := RequireAuth(&fakeVerifier{err: auth.ErrUnauthenticated})(probe)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/tokens", nil))
	if rec.Code != 401 {
		t.Fatalf("no cred: %d", rec.Code)
	}
	var env struct {
		Error struct{ Code string } `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Error.Code != "unauthenticated" {
		t.Fatalf("code = %q", env.Error.Code)
	}

	// Bearer service token → principal in context.
	h = RequireAuth(&fakeVerifier{principal: okPrincipal})(probe)
	req := httptest.NewRequest("GET", "/v1/tokens", nil)
	req.Header.Set("Authorization", "Bearer janus_svc_sometoken")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("bearer: %d", rec.Code)
	}

	// Session cookie → principal in context.
	req = httptest.NewRequest("GET", "/v1/tokens", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "cookievalue"})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("cookie: %d", rec.Code)
	}

	// Sealed keyring during verification → 503 sealed, not 401.
	h = RequireAuth(&fakeVerifier{err: crypto.ErrSealed})(probe)
	req = httptest.NewRequest("GET", "/v1/tokens", nil)
	req.Header.Set("Authorization", "Bearer janus_svc_x")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 503 {
		t.Fatalf("sealed: %d", rec.Code)
	}
}

// idleExpiredVerifier simulates a session that failed verification because it
// exceeded the configured idle window (distinct from plain unauthenticated).
type idleExpiredVerifier struct{}

func (idleExpiredVerifier) VerifySession(context.Context, string) (auth.Principal, error) {
	return auth.Principal{}, auth.ErrSessionExpired
}

func (idleExpiredVerifier) VerifyServiceToken(context.Context, string) (auth.Principal, *auth.TokenScope, error) {
	return auth.Principal{}, nil, auth.ErrUnauthenticated
}

func TestRequireAuthIdleExpired401(t *testing.T) {
	h := RequireAuth(idleExpiredVerifier{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler must not run")
	}))
	req := httptest.NewRequest("GET", "/v1/projects", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "stale"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	var env errEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Error.Code != CodeSessionExpired {
		t.Fatalf("code = %q, want session_expired", env.Error.Code)
	}
}

func TestRateLimit(t *testing.T) {
	// 2 sustained/min with burst 2 for a fast test.
	rl := newIPRateLimiter(2.0/60.0, 2)
	h := rl.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	mk := func(ip string) *http.Request {
		r := httptest.NewRequest("POST", "/v1/auth/login", nil)
		r.RemoteAddr = ip + ":12345"
		return r
	}
	// Burst of 2 passes; third is limited.
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, mk("10.0.0.1"))
		if rec.Code != 200 {
			t.Fatalf("req %d: %d", i, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, mk("10.0.0.1"))
	if rec.Code != 429 {
		t.Fatalf("third: %d, want 429", rec.Code)
	}
	// A different IP is independent.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, mk("10.0.0.2"))
	if rec.Code != 200 {
		t.Fatalf("other ip: %d", rec.Code)
	}
}
