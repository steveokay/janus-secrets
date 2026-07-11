package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/steveokay/janus-secrets/internal/crypto"
)

// newTestServerSealed builds a minimal Server (no auth/authz/store/audit) with
// a sealed Shamir keyring, wired the same way newShamirTestServer does. It
// exists alongside newShamirTestServer so this test can build both a sealed
// and an already-unsealed variant without threading keyring access through
// the existing helper's return values.
func newTestServerSealed(t *testing.T) *Server {
	t.Helper()
	seals := &memSealStore{}
	kr := crypto.NewKeyring()
	u := crypto.NewShamirUnsealer(seals, 0, 0)
	return New(Config{SealType: crypto.SealTypeShamir}, kr, u, seals, nil, nil, nil, nil, nil,
		nil, nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// newTestServerUnsealed builds the same minimal Server but unseals it
// immediately, mirroring unsealedKeyring's pattern in middleware_test.go.
func newTestServerUnsealed(t *testing.T) *Server {
	t.Helper()
	srv := newTestServerSealed(t)
	master, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.keyring.Unseal(master); err != nil {
		t.Fatal(err)
	}
	return srv
}

func stubUI() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("STUB-UI"))
	})
}

func TestMountUIFallbackAndSealGate(t *testing.T) {
	// Unsealed server: UI fallback serves non-/v1 paths; /v1/sys still works.
	s := newTestServerUnsealed(t)
	s.MountUI(stubUI())

	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/projects/x/configs/y", nil))
	if rr.Code != 200 || rr.Body.String() != "STUB-UI" {
		t.Fatalf("deep link: got %d %q, want 200 STUB-UI", rr.Code, rr.Body.String())
	}
	rr = httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/v1/sys/seal-status", nil))
	if rr.Code != 200 {
		t.Fatalf("seal-status status = %d, want 200", rr.Code)
	}

	// Sealed server: static UI still served, but a non-sys /v1 path is 503.
	sealed := newTestServerSealed(t)
	sealed.MountUI(stubUI())
	rr = httptest.NewRecorder()
	sealed.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/login", nil))
	if rr.Code != 200 || rr.Body.String() != "STUB-UI" {
		t.Fatalf("sealed UI: got %d %q, want 200 STUB-UI", rr.Code, rr.Body.String())
	}
	rr = httptest.NewRecorder()
	sealed.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/v1/configs/abc/secrets", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("sealed API: status = %d, want 503", rr.Code)
	}
}
