package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/steveokay/janus-secrets/internal/crypto"
)

func unsealedKeyring(t *testing.T) *crypto.Keyring {
	t.Helper()
	kr := crypto.NewKeyring()
	master, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := kr.Unseal(master); err != nil {
		t.Fatal(err)
	}
	return kr
}

func TestRequireUnsealed(t *testing.T) {
	kr := crypto.NewKeyring() // sealed
	probe := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := RequireUnsealed(kr)(probe)

	// Non-sys route while sealed → 503 with the sealed envelope.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/projects", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("sealed non-sys: status %d, want 503", rec.Code)
	}
	var body struct {
		Error struct{ Code string } `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error.Code != "sealed" {
		t.Fatalf("sealed body: %s (err %v)", rec.Body.String(), err)
	}

	// Sys routes are exempt even while sealed.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/sys/seal-status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("sealed sys: status %d, want 200", rec.Code)
	}

	// Unsealed keyring → non-sys route passes.
	h2 := RequireUnsealed(unsealedKeyring(t))(probe)
	rec = httptest.NewRecorder()
	h2.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/projects", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("unsealed non-sys: status %d, want 200", rec.Code)
	}
}

func TestRequestLoggerNeverLogsBodies(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	h := requestLogger(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	const canary = "deadbeefcafe0123-share-canary"
	req := httptest.NewRequest("POST", "/v1/sys/unseal", strings.NewReader(`{"share":"`+canary+`"}`))
	h.ServeHTTP(httptest.NewRecorder(), req)

	out := buf.String()
	if !strings.Contains(out, "/v1/sys/unseal") || !strings.Contains(out, "418") {
		t.Fatalf("log missing method/path/status: %q", out)
	}
	if strings.Contains(out, canary) {
		t.Fatalf("request body leaked into log: %q", out)
	}
}
