package api

import (
	"net/http"
	"testing"
)

func TestVersionsE2E(t *testing.T) {
	ts, _, email, password, cid := authStackFull(t)
	cookie := login(t, ts.URL, email, password)
	base := ts.URL + "/v1/configs/" + cid

	// v1: A=1,B=2 ; v2: A=9 (change), C=3 (add), B deleted.
	doAuthed(t, "PUT", base+"/secrets", cookie, "",
		`{"changes":[{"key":"A","value":"1"},{"key":"B","value":"2"}]}`, nil)
	doAuthed(t, "PUT", base+"/secrets", cookie, "",
		`{"changes":[{"key":"A","value":"9"},{"key":"C","value":"3"},{"key":"B","delete":true}]}`, nil)

	// Version list has >= 2 entries.
	var vl struct {
		Versions []map[string]any `json:"versions"`
	}
	if code := doAuthed(t, "GET", base+"/versions", cookie, "", "", &vl); code != 200 || len(vl.Versions) < 2 {
		t.Fatalf("version list: %d, n=%d", code, len(vl.Versions))
	}

	// Diff v1 vs v2: A changed, C added, B removed (key names only).
	var d struct {
		Added, Changed, Removed []string
	}
	if code := doAuthed(t, "GET", base+"/versions/diff?a=1&b=2", cookie, "", "", &d); code != 200 {
		t.Fatalf("diff: %d", code)
	}
	has := func(xs []string, k string) bool {
		for _, x := range xs {
			if x == k {
				return true
			}
		}
		return false
	}
	if !has(d.Changed, "A") || !has(d.Added, "C") || !has(d.Removed, "B") {
		t.Fatalf("diff wrong: %+v", d)
	}

	// Bad diff params -> 400.
	if code := doAuthed(t, "GET", base+"/versions/diff?a=x&b=2", cookie, "", "", nil); code != http.StatusBadRequest {
		t.Fatalf("bad diff params: want 400, got %d", code)
	}

	// Rollback to v1 -> new version whose state matches v1 (A=1, B present, no C).
	var rv versionResponse
	if code := doAuthed(t, "POST", base+"/rollback", cookie, "", `{"target_version":1}`, &rv); code != 200 {
		t.Fatalf("rollback: %d", code)
	}
	var one struct{ Value string }
	doAuthed(t, "GET", base+"/secrets/A", cookie, "", "", &one)
	if one.Value != "1" {
		t.Fatalf("after rollback A=%q, want 1", one.Value)
	}
}
