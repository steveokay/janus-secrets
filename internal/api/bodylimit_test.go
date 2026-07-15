package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBodyLimit(t *testing.T) {
	readEcho := func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
	mw := bodyLimit(10) // 10-byte cap
	h := mw(http.HandlerFunc(readEcho))

	// under cap → OK
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/projects", strings.NewReader("12345")))
	if rec.Code != http.StatusOK {
		t.Fatalf("under cap: got %d", rec.Code)
	}
	// over cap → 413
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/projects", strings.NewReader("this body is way over ten bytes")))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over cap: got %d", rec.Code)
	}
	// restore endpoint exempt even over cap
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/sys/restore", strings.NewReader("this body is way over ten bytes")))
	if rec.Code != http.StatusOK {
		t.Fatalf("restore exempt: got %d", rec.Code)
	}
	// 0 cap disables the limit
	off := bodyLimit(0)(http.HandlerFunc(readEcho))
	rec = httptest.NewRecorder()
	off.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/projects", strings.NewReader("this body is way over ten bytes")))
	if rec.Code != http.StatusOK {
		t.Fatalf("disabled cap: got %d", rec.Code)
	}
}
