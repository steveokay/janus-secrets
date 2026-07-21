package api

import (
	"testing"
)

// TestSessionManagementE2E drives the self-service session surface: two logins
// produce two sessions, the list marks the requesting one current and carries
// no HMAC, a single revoke removes only its target, and revoke-others keeps the
// caller's own session.
func TestSessionManagementE2E(t *testing.T) {
	ts, _, email, password, _ := authStackFull(t)

	c1 := login(t, ts.URL, email, password) // "current" session for the assertions
	c2 := login(t, ts.URL, email, password) // a second device
	c3 := login(t, ts.URL, email, password) // a third device

	type sess struct {
		ID        string `json:"id"`
		IP        string `json:"ip"`
		UserAgent string `json:"user_agent"`
		Current   bool   `json:"current"`
		HMAC      string `json:"token_hmac"` // must never be populated
	}
	list := func(cookie string) []sess {
		var out struct {
			Sessions []sess `json:"sessions"`
		}
		if code := doAuthed(t, "GET", ts.URL+"/v1/auth/sessions", cookie, "", "", &out); code != 200 {
			t.Fatalf("list sessions: %d", code)
		}
		return out.Sessions
	}

	got := list(c1)
	if len(got) != 3 {
		t.Fatalf("want 3 sessions, got %d", len(got))
	}
	currents := 0
	for _, s := range got {
		if s.HMAC != "" {
			t.Fatalf("session list leaked credential material: %q", s.HMAC)
		}
		if s.IP == "" { // login went over httptest → RemoteAddr is set
			t.Fatalf("session ip not captured: %+v", s)
		}
		if s.Current {
			currents++
		}
	}
	if currents != 1 {
		t.Fatalf("exactly one session must be current, got %d", currents)
	}

	// Revoke c2's session (identified from c2's own view where it is current).
	var c2id string
	for _, s := range list(c2) {
		if s.Current {
			c2id = s.ID
		}
	}
	if c2id == "" {
		t.Fatal("could not resolve c2's session id")
	}
	if code := doAuthed(t, "DELETE", ts.URL+"/v1/auth/sessions/"+c2id, c1, "", "", nil); code != 204 {
		t.Fatalf("revoke one: %d", code)
	}
	// c2 is now dead (401 on an authed route); c1 and c3 still work.
	if code := doAuthed(t, "GET", ts.URL+"/v1/auth/me", c2, "", "", nil); code != 401 {
		t.Fatalf("revoked session still valid: %d", code)
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/auth/me", c1, "", "", nil); code != 200 {
		t.Fatalf("current session wrongly revoked: %d", code)
	}
	if len(list(c1)) != 2 {
		t.Fatalf("want 2 sessions after single revoke, got %d", len(list(c1)))
	}

	// Revoking another user's session id is indistinguishable from missing → 404.
	if code := doAuthed(t, "DELETE", ts.URL+"/v1/auth/sessions/00000000-0000-0000-0000-000000000000", c1, "", "", nil); code != 404 {
		t.Fatalf("revoke unknown id: want 404, got %d", code)
	}

	// Revoke all others: c1 survives, c3 dies.
	var res struct {
		Revoked int `json:"revoked"`
	}
	if code := doAuthed(t, "DELETE", ts.URL+"/v1/auth/sessions", c1, "", "", &res); code != 200 {
		t.Fatalf("revoke others: %d", code)
	}
	if res.Revoked != 1 {
		t.Fatalf("revoke-others count: want 1 (c3), got %d", res.Revoked)
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/auth/me", c3, "", "", nil); code != 401 {
		t.Fatalf("c3 should be revoked: %d", code)
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/auth/me", c1, "", "", nil); code != 200 {
		t.Fatalf("c1 must survive revoke-others: %d", code)
	}
	if l := list(c1); len(l) != 1 || !l[0].Current {
		t.Fatalf("only the current session should remain: %+v", l)
	}
}
