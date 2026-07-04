package api

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestWriteError(t *testing.T) {
	rec := httptest.NewRecorder()
	writeError(rec, 409, CodeAlreadyInitialized, "seal is already initialized")

	if rec.Code != 409 {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q", ct)
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "already_initialized" || body.Error.Message == "" {
		t.Fatalf("body = %+v", body)
	}
}

func TestWriteJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, 200, map[string]bool{"ok": true})
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status=%d ct=%q", rec.Code, rec.Header().Get("Content-Type"))
	}
}
