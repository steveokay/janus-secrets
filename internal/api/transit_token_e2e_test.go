package api

import (
	"net/http"
	"testing"
)

func TestTransitScopedTokenUsesTransitOnly(t *testing.T) {
	ts, _, email, password, _ := authStackFull(t)
	cookie := login(t, ts.URL, email, password)

	// Owner creates a transit key.
	if code := doAuthed(t, "POST", ts.URL+"/v1/transit/keys", cookie, "", `{"name":"app","type":"aes256-gcm"}`, nil); code != 200 && code != 201 {
		t.Fatalf("create transit key: %d", code)
	}

	// Mint an all-keys transit-use token (empty scope id = all keys).
	var minted struct {
		Token string `json:"token"`
	}
	code := doAuthed(t, "POST", ts.URL+"/v1/tokens", cookie, "",
		`{"name":"ci","scope":{"kind":"transit","id":""},"access":"use"}`, &minted)
	if code != http.StatusOK || minted.Token == "" {
		t.Fatalf("mint transit token: %d token=%q", code, minted.Token)
	}

	// The token CAN encrypt with its transit scope.
	if code := doAuthed(t, "POST", ts.URL+"/v1/transit/encrypt/app", "", minted.Token,
		`{"plaintext":"aGk="}`, nil); code != 200 {
		t.Fatalf("transit token encrypt: want 200, got %d", code)
	}

	// The token CANNOT touch non-transit resources: project create is instance-scoped
	// and outside a transit token's capabilities → 403.
	if code := doAuthed(t, "POST", ts.URL+"/v1/projects", "", minted.Token,
		`{"slug":"nope","name":"nope"}`, nil); code != http.StatusForbidden {
		t.Fatalf("transit token must not create projects: want 403, got %d", code)
	}
}
