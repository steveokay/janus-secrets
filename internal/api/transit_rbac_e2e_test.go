package api

import (
	"net/http"
	"testing"
)

// TestTransitRBACMatrix locks in the transit-engine RBAC: role capabilities
// (viewer read-only, developer use, admin manage) plus service-token transit
// scoping (key-restricted tokens, and config-scoped tokens denied transit).
func TestTransitRBACMatrix(t *testing.T) {
	ts, _, adminEmail, adminPass, configID := authStackFull(t)
	admin := login(t, ts.URL, adminEmail, adminPass)

	// Admin (owner) creates two keys.
	if code := doAuthed(t, "POST", ts.URL+"/v1/transit/keys", admin, "",
		`{"name":"app","type":"aes256-gcm"}`, nil); code != http.StatusCreated {
		t.Fatalf("create key app: want 201, got %d", code)
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/transit/keys", admin, "",
		`{"name":"other","type":"aes256-gcm"}`, nil); code != http.StatusCreated {
		t.Fatalf("create key other: want 201, got %d", code)
	}

	_, viewerPass := makeUser(t, ts.URL, admin, "tviewer@corp.io", "viewer")
	_, devPass := makeUser(t, ts.URL, admin, "tdev@corp.io", "developer")
	viewer := login(t, ts.URL, "tviewer@corp.io", viewerPass)
	dev := login(t, ts.URL, "tdev@corp.io", devPass)

	const ptBody = `{"plaintext":"aGk="}` // base64("hi")

	// viewer: transit:read only.
	if code := doAuthed(t, "GET", ts.URL+"/v1/transit/keys", viewer, "", "", nil); code != http.StatusOK {
		t.Fatalf("viewer list keys: want 200, got %d", code)
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/transit/encrypt/app", viewer, "", ptBody, nil); code != http.StatusForbidden {
		t.Fatalf("viewer encrypt: want 403, got %d", code)
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/transit/keys", viewer, "",
		`{"name":"nope","type":"aes256-gcm"}`, nil); code != http.StatusForbidden {
		t.Fatalf("viewer create key: want 403, got %d", code)
	}

	// developer: transit:read + transit:use, but not manage.
	if code := doAuthed(t, "POST", ts.URL+"/v1/transit/encrypt/app", dev, "", ptBody, nil); code != http.StatusOK {
		t.Fatalf("dev encrypt: want 200, got %d", code)
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/transit/keys/app/rotate", dev, "", "", nil); code != http.StatusForbidden {
		t.Fatalf("dev rotate: want 403, got %d", code)
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/transit/keys", dev, "",
		`{"name":"nope2","type":"aes256-gcm"}`, nil); code != http.StatusForbidden {
		t.Fatalf("dev create key: want 403, got %d", code)
	}

	// Key-restricted transit-use token: allowed its own key, denied any other.
	var restricted struct {
		Token string `json:"token"`
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/tokens", admin, "",
		`{"name":"restricted","scope":{"kind":"transit","id":"app"},"access":"use"}`, &restricted); code != http.StatusOK || restricted.Token == "" {
		t.Fatalf("mint restricted transit token: want 200, got %d token=%q", code, restricted.Token)
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/transit/encrypt/app", "", restricted.Token, ptBody, nil); code != http.StatusOK {
		t.Fatalf("restricted token encrypt own key: want 200, got %d", code)
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/transit/encrypt/other", "", restricted.Token, ptBody, nil); code != http.StatusForbidden {
		t.Fatalf("restricted token encrypt other key: want 403, got %d", code)
	}

	// Config-scoped (secrets) token grants no transit capability at all.
	var cfgTok struct {
		Token string `json:"token"`
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/tokens", admin, "",
		`{"name":"cfg","scope":{"kind":"config","id":"`+configID+`"},"access":"readwrite"}`, &cfgTok); code != http.StatusOK || cfgTok.Token == "" {
		t.Fatalf("mint config token: want 200, got %d token=%q", code, cfgTok.Token)
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/transit/encrypt/app", "", cfgTok.Token, ptBody, nil); code != http.StatusForbidden {
		t.Fatalf("config token encrypt: want 403, got %d", code)
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/transit/keys", "", cfgTok.Token, "", nil); code != http.StatusForbidden {
		t.Fatalf("config token list keys: want 403, got %d", code)
	}
}
