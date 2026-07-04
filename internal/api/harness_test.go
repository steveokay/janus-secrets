package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/steveokay/janus-secrets/internal/crypto"
)

// memSealStore is an in-memory crypto.SealConfigStore for handler tests.
type memSealStore struct {
	mu  sync.Mutex
	cfg *crypto.SealConfig
}

func (m *memSealStore) Get(context.Context) (*crypto.SealConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cfg == nil {
		return nil, crypto.ErrNoSealConfig
	}
	c := *m.cfg
	return &c, nil
}

func (m *memSealStore) Put(_ context.Context, cfg *crypto.SealConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := *cfg
	m.cfg = &c
	return nil
}

// newShamirTestServer returns a Server wired for a Shamir seal over an
// in-memory store, plus its httptest server.
func newShamirTestServer(t *testing.T) (*Server, *httptest.Server, *memSealStore) {
	t.Helper()
	seals := &memSealStore{}
	kr := crypto.NewKeyring()
	u := crypto.NewShamirUnsealer(seals, 0, 0)
	srv := New(Config{SealType: crypto.SealTypeShamir}, kr, u, seals, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts, seals
}

// doJSON issues a request and decodes the JSON response into out (if non-nil).
func doJSON(t *testing.T, method, url, body string, out any) int {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("decode %s %s: %v", method, url, err)
		}
	}
	return resp.StatusCode
}

// errCode extracts error.code from a raw envelope-decoded map.
type errEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}
