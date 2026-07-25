package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
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

// TestUnmatchedAPIPathReturnsJSON404 pins QA finding D-1: an unmatched /v1/
// route must produce the JSON error envelope, never the SPA's 200 + index.html
// (which made SDKs parse HTML as JSON and hid typo'd endpoints).
func TestUnmatchedAPIPathReturnsJSON404(t *testing.T) {
	s := newTestServerUnsealed(t)
	s.MountUI(stubUI())

	for _, path := range []string{"/v1/definitely-not-a-route", "/v1/sys/nope", "/v1"} {
		rr := httptest.NewRecorder()
		s.Handler().ServeHTTP(rr, httptest.NewRequest("GET", path, nil))
		if rr.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", path, rr.Code)
		}
		if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
			t.Errorf("%s: content-type = %q, want JSON", path, ct)
		}
		if body := rr.Body.String(); strings.Contains(body, "STUB-UI") {
			t.Errorf("%s: served the SPA instead of a JSON error: %q", path, body)
		}
		var env errorBody
		if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
			t.Errorf("%s: body is not the error envelope: %v", path, err)
		} else if env.Error.Code != CodeNotFound {
			t.Errorf("%s: code = %q, want %q", path, env.Error.Code, CodeNotFound)
		}
	}
}

// TestSPADeepLinkStillServed guards the other half of D-1: non-API paths must
// keep falling through to the SPA so client-side routing works.
func TestSPADeepLinkStillServed(t *testing.T) {
	s := newTestServerUnsealed(t)
	s.MountUI(stubUI())
	for _, path := range []string{"/projects/x", "/v1x-not-api", "/"} {
		rr := httptest.NewRecorder()
		s.Handler().ServeHTTP(rr, httptest.NewRequest("GET", path, nil))
		if rr.Code != http.StatusOK || rr.Body.String() != "STUB-UI" {
			t.Errorf("%s: got %d %q, want 200 STUB-UI", path, rr.Code, rr.Body.String())
		}
	}
}

// TestAPIMethodNotAllowedReturnsJSON pins the 405 half of D-1.
func TestAPIMethodNotAllowedReturnsJSON(t *testing.T) {
	s := newTestServerUnsealed(t)
	s.MountUI(stubUI())
	rr := httptest.NewRecorder()
	// /v1/sys/seal-status is registered GET-only.
	s.Handler().ServeHTTP(rr, httptest.NewRequest("DELETE", "/v1/sys/seal-status", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rr.Code)
	}
	if strings.Contains(rr.Body.String(), "STUB-UI") {
		t.Fatalf("served the SPA for a 405: %q", rr.Body.String())
	}
}
