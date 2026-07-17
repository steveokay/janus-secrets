package api

import (
	"net/http"
	"testing"
)

func TestSecretsWriteE2E(t *testing.T) {
	ts, _, email, password, cid := authStackFull(t)
	cookie := login(t, ts.URL, email, password)
	base := ts.URL + "/v1/configs/" + cid + "/secrets"

	// Batch save -> new version.
	var v1 versionResponse
	if code := doAuthed(t, "PUT", base, cookie, "",
		`{"message":"init","changes":[{"key":"A","value":"1"},{"key":"B","value":"2"}]}`, &v1); code != 200 {
		t.Fatalf("batch write: %d", code)
	}
	if v1.Version < 1 {
		t.Fatalf("bad version: %+v", v1)
	}

	// Duplicate key in a batch -> 400, no version.
	if code := doAuthed(t, "PUT", base, cookie, "",
		`{"changes":[{"key":"A","value":"x"},{"key":"A","value":"y"}]}`, nil); code != http.StatusBadRequest {
		t.Fatalf("dup-key batch: want 400, got %d", code)
	}

	// Per-key put -> new version; then reveal reflects it.
	var v2 versionResponse
	if code := doAuthed(t, "PUT", base+"/A", cookie, "", `{"value":"updated"}`, &v2); code != 200 {
		t.Fatalf("per-key put: %d", code)
	}
	if v2.Version <= v1.Version {
		t.Fatalf("version did not advance: %d <= %d", v2.Version, v1.Version)
	}
	var one struct{ Value string }
	doAuthed(t, "GET", base+"/A", cookie, "", "", &one)
	if one.Value != "updated" {
		t.Fatalf("value after put: %q", one.Value)
	}

	// Delete key -> new version; masked list drops it.
	if code := doAuthed(t, "DELETE", base+"/B", cookie, "", "", nil); code != 200 {
		t.Fatalf("delete key: %d", code)
	}
	var masked struct {
		Secrets map[string]map[string]any `json:"secrets"`
	}
	doAuthed(t, "GET", base, cookie, "", "", &masked)
	if _, present := masked.Secrets["B"]; present {
		t.Fatal("deleted key B still present")
	}
}

// TestSecretsWriteType covers the type field end-to-end: a batch write can set
// a key's type, the masked list and reveal responses both emit it, and an
// unrecognized type is rejected with 400 (not 500).
func TestSecretsWriteType(t *testing.T) {
	ts, _, email, password, cid := authStackFull(t)
	cookie := login(t, ts.URL, email, password)
	base := ts.URL + "/v1/configs/" + cid + "/secrets"

	// Batch write with a type -> masked list and reveal both carry it.
	if code := doAuthed(t, "PUT", base, cookie, "", `{"changes":[{"key":"CFG","value":"{}","type":"json"}]}`, nil); code != 200 {
		t.Fatalf("batch write with type: %d", code)
	}

	var masked struct {
		Secrets map[string]map[string]any `json:"secrets"`
	}
	if code := doAuthed(t, "GET", base, cookie, "", "", &masked); code != 200 {
		t.Fatalf("masked list: %d", code)
	}
	if got := masked.Secrets["CFG"]["type"]; got != "json" {
		t.Fatalf("masked list type: got %v, want json (entry=%+v)", got, masked.Secrets["CFG"])
	}

	var revealed struct {
		Key, Value, Type string
	}
	if code := doAuthed(t, "GET", base+"/CFG", cookie, "", "", &revealed); code != 200 {
		t.Fatalf("reveal: %d", code)
	}
	if revealed.Type != "json" {
		t.Fatalf("reveal type: got %q, want json", revealed.Type)
	}

	// Bogus type -> 400, not 500.
	if code := doAuthed(t, "PUT", base, cookie, "", `{"changes":[{"key":"BAD","value":"x","type":"bogus"}]}`, nil); code != http.StatusBadRequest {
		t.Fatalf("bogus type: want 400, got %d", code)
	}
}
