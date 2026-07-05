package api

import (
	"net/http"
	"testing"
)

func TestTransitKeyLifecycleE2E(t *testing.T) {
	ts, _, email, password, _ := authStackFull(t) // real server + owner admin
	cookie := login(t, ts.URL, email, password)

	// Create an aes key.
	var created map[string]any
	if code := doAuthed(t, "POST", ts.URL+"/v1/transit/keys", cookie, "",
		`{"name":"app","type":"aes256-gcm"}`, &created); code != http.StatusCreated {
		t.Fatalf("create key: %d", code)
	}
	if created["name"] != "app" || int(created["latest_version"].(float64)) != 1 {
		t.Fatalf("create body: %v", created)
	}

	// List includes it.
	var list map[string]any
	if code := doAuthed(t, "GET", ts.URL+"/v1/transit/keys", cookie, "", "", &list); code != 200 {
		t.Fatalf("list: %d", code)
	}

	// Get returns metadata.
	var got map[string]any
	if code := doAuthed(t, "GET", ts.URL+"/v1/transit/keys/app", cookie, "", "", &got); code != 200 {
		t.Fatalf("get: %d", code)
	}

	// Rotate → latest_version 2.
	var rotated map[string]any
	if code := doAuthed(t, "POST", ts.URL+"/v1/transit/keys/app/rotate", cookie, "", "", &rotated); code != 200 {
		t.Fatalf("rotate: %d", code)
	}
	if int(rotated["latest_version"].(float64)) != 2 {
		t.Fatalf("latest_version: %v", rotated["latest_version"])
	}

	// Delete requires deletion_allowed.
	if code := doAuthed(t, "DELETE", ts.URL+"/v1/transit/keys/app", cookie, "", "", nil); code == http.StatusNoContent {
		t.Fatal("delete without deletion_allowed must fail")
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/transit/keys/app/config", cookie, "",
		`{"deletion_allowed":true}`, nil); code != 200 {
		t.Fatalf("config: %d", code)
	}
	if code := doAuthed(t, "DELETE", ts.URL+"/v1/transit/keys/app", cookie, "", "", nil); code != http.StatusNoContent {
		t.Fatalf("delete: %d", code)
	}
}
