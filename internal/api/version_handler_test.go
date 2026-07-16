package api

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func decodeBody(t *testing.T, b []byte, out any) {
	t.Helper()
	if err := json.Unmarshal(b, out); err != nil {
		t.Fatalf("decode %q: %v", string(b), err)
	}
}

func TestVersionHandlerRendersBuildInfo(t *testing.T) {
	srv, ts, _ := newShamirTestServer(t)
	_ = ts
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/sys/version", nil)
	srv.handleVersion(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d", rec.Code)
	}
	var body struct{ Version, Commit, Date string }
	decodeBody(t, rec.Body.Bytes(), &body)
	if body.Version == "" {
		t.Fatalf("empty version in %s", rec.Body.String())
	}
}
