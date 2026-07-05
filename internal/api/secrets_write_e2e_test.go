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
