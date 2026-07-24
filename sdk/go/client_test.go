package janus

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// obviously-fake, low-entropy fixtures (not real secrets)
const (
	testToken    = "janus_svc_test-token-000"
	testConfigID = "cfg-00000000-0000-0000-0000-000000000001"
	testRoleID   = "role-0000-0000-0000-000000000002"
)

// fakeServer is a minimal stand-in for the Janus /v1 API.
type fakeServer struct {
	*httptest.Server
	revealHits int32 // count of batch-reveal GETs
	lastAuth   string
}

func newFakeServer(t *testing.T, secrets map[string]string) *fakeServer {
	t.Helper()
	fs := &fakeServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/configs/"+testConfigID+"/secrets", func(w http.ResponseWriter, r *http.Request) {
		fs.lastAuth = r.Header.Get("Authorization")
		if r.URL.Query().Get("reveal") != "true" {
			http.Error(w, "expected reveal=true", http.StatusBadRequest)
			return
		}
		atomic.AddInt32(&fs.revealHits, 1)
		writeJSON(w, 200, map[string]any{"version": 3, "secrets": secrets})
	})
	fs.Server = httptest.NewServer(mux)
	t.Cleanup(fs.Close)
	return fs
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func TestGetSecrets_SendsBearerAndParses(t *testing.T) {
	secrets := map[string]string{"DATABASE_URL": "postgres://fake", "API_KEY": "abc-123-fake"}
	fs := newFakeServer(t, secrets)
	c, err := NewClient(fs.URL, WithToken(testToken))
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.GetSecrets(context.Background(), testConfigID)
	if err != nil {
		t.Fatal(err)
	}
	if got["DATABASE_URL"] != "postgres://fake" || got["API_KEY"] != "abc-123-fake" {
		t.Fatalf("unexpected secrets: %v", got)
	}
	if fs.lastAuth != "Bearer "+testToken {
		t.Fatalf("bad auth header: %q", fs.lastAuth)
	}
}

func TestGetSecret_SingleKeyAndMissing(t *testing.T) {
	fs := newFakeServer(t, map[string]string{"KEY_A": "val-a-fake"})
	c, _ := NewClient(fs.URL, WithToken(testToken))
	v, err := c.GetSecret(context.Background(), testConfigID, "KEY_A")
	if err != nil || v != "val-a-fake" {
		t.Fatalf("get KEY_A: %q %v", v, err)
	}
	_, err = c.GetSecret(context.Background(), testConfigID, "MISSING")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing key should be ErrNotFound, got %v", err)
	}
}

func TestCache_HitAndExpiry(t *testing.T) {
	fs := newFakeServer(t, map[string]string{"K": "v-fake"})
	now := time.Unix(1000, 0)
	c, _ := NewClient(fs.URL, WithToken(testToken),
		WithCacheTTL(30*time.Second), withClock(func() time.Time { return now }))

	// first read: miss
	if _, err := c.GetSecrets(context.Background(), testConfigID); err != nil {
		t.Fatal(err)
	}
	// within TTL: hit (no new server call)
	now = now.Add(29 * time.Second)
	if _, err := c.GetSecrets(context.Background(), testConfigID); err != nil {
		t.Fatal(err)
	}
	if h := atomic.LoadInt32(&fs.revealHits); h != 1 {
		t.Fatalf("expected 1 server hit within TTL, got %d", h)
	}
	// past TTL: miss again
	now = now.Add(2 * time.Second)
	if _, err := c.GetSecrets(context.Background(), testConfigID); err != nil {
		t.Fatal(err)
	}
	if h := atomic.LoadInt32(&fs.revealHits); h != 2 {
		t.Fatalf("expected 2 server hits after TTL, got %d", h)
	}
}

func TestCache_DisabledAlwaysHits(t *testing.T) {
	fs := newFakeServer(t, map[string]string{"K": "v-fake"})
	c, _ := NewClient(fs.URL, WithToken(testToken), WithCacheTTL(0))
	for i := 0; i < 3; i++ {
		if _, err := c.GetSecrets(context.Background(), testConfigID); err != nil {
			t.Fatal(err)
		}
	}
	if h := atomic.LoadInt32(&fs.revealHits); h != 3 {
		t.Fatalf("cache disabled: expected 3 hits, got %d", h)
	}
}

func TestRefresh_Evicts(t *testing.T) {
	fs := newFakeServer(t, map[string]string{"K": "v-fake"})
	c, _ := NewClient(fs.URL, WithToken(testToken), WithCacheTTL(time.Hour))
	_, _ = c.GetSecrets(context.Background(), testConfigID)
	c.Refresh(testConfigID)
	_, _ = c.GetSecrets(context.Background(), testConfigID)
	if h := atomic.LoadInt32(&fs.revealHits); h != 2 {
		t.Fatalf("expected 2 hits after Refresh, got %d", h)
	}
	// Refresh("") clears all
	c.Refresh("")
	_, _ = c.GetSecrets(context.Background(), testConfigID)
	if h := atomic.LoadInt32(&fs.revealHits); h != 3 {
		t.Fatalf("expected 3 hits after Refresh-all, got %d", h)
	}
}

func TestCache_ReturnedMapIsCopy(t *testing.T) {
	fs := newFakeServer(t, map[string]string{"K": "v-fake"})
	c, _ := NewClient(fs.URL, WithToken(testToken), WithCacheTTL(time.Hour))
	m1, _ := c.GetSecrets(context.Background(), testConfigID)
	m1["K"] = "mutated"
	m1["EXTRA"] = "x"
	m2, _ := c.GetSecrets(context.Background(), testConfigID)
	if m2["K"] != "v-fake" || len(m2) != 1 {
		t.Fatalf("cache was mutated via returned map: %v", m2)
	}
}

func errorServer(t *testing.T, status int, code, msg string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":{"code":"` + code + `","message":"` + msg + `"}}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestErrorEnvelope_TypedErrors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		code   string
		want   error
	}{
		{"unauthorized", 401, "unauthorized", ErrUnauthorized},
		{"forbidden", 403, "forbidden", ErrForbidden},
		{"notfound", 404, "not_found", ErrNotFound},
		{"sealed", 503, "sealed", ErrSealed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			url := errorServer(t, tc.status, tc.code, "fake message")
			c, _ := NewClient(url, WithToken(testToken))
			_, err := c.GetSecrets(context.Background(), testConfigID)
			if !errors.Is(err, tc.want) {
				t.Fatalf("status %d: want %v, got %v", tc.status, tc.want, err)
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("expected *APIError, got %T", err)
			}
			if apiErr.Status != tc.status || apiErr.Code != tc.code {
				t.Fatalf("apiErr = %+v", apiErr)
			}
			if !strings.Contains(apiErr.Error(), tc.code) {
				t.Fatalf("Error() missing code: %q", apiErr.Error())
			}
		})
	}
}

func TestValidation(t *testing.T) {
	c, _ := NewClient("http://example.invalid", WithToken(testToken))
	if _, err := c.GetSecrets(context.Background(), ""); err == nil {
		t.Fatal("empty configID should error")
	}
	if _, err := c.GetSecret(context.Background(), testConfigID, ""); err == nil {
		t.Fatal("empty key should error")
	}
	if _, err := NewClient(""); err == nil {
		t.Fatal("empty baseURL should error")
	}
}

func TestContextCancellation(t *testing.T) {
	fs := newFakeServer(t, map[string]string{"K": "v-fake"})
	c, _ := NewClient(fs.URL, WithToken(testToken))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.GetSecrets(ctx, testConfigID); err == nil {
		t.Fatal("cancelled context should error")
	}
}
