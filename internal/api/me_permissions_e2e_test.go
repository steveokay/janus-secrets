package api

import (
	"fmt"
	"slices"
	"testing"
)

// meResult mirrors the /v1/auth/me payload the SPA consumes.
type meResult struct {
	Kind        string `json:"kind"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Permissions struct {
		Instance []string `json:"instance"`
		Anywhere []string `json:"anywhere"`
	} `json:"permissions"`
}

// The nav gates on this payload, so the endpoint has to answer accurately for
// the two principal kinds that can hold a session or a token. The point of the
// feature is that a user stops discovering their permissions by collecting
// 403s — which only works if the hint matches what the server will actually do.
func TestMeReportsEffectivePermissionsE2E(t *testing.T) {
	ts, _, email, password, configID := authStackFull(t)
	cookie := login(t, ts.URL, email, password)

	t.Run("bootstrap owner", func(t *testing.T) {
		var me meResult
		if code := doAuthed(t, "GET", ts.URL+"/v1/auth/me", cookie, "", "", &me); code != 200 {
			t.Fatalf("me: %d", code)
		}
		if me.Kind != "user" || me.Name != email {
			t.Fatalf("me = %+v", me)
		}
		// Instance-scoped features the nav gates on.
		for _, a := range []string{"group:manage", "transit:read", "user:manage", "sys:seal"} {
			if !slices.Contains(me.Permissions.Instance, a) {
				t.Errorf("owner should hold %q at instance scope, got %v", a, me.Permissions.Instance)
			}
		}
		// Anywhere is a superset of instance, always.
		for _, a := range me.Permissions.Instance {
			if !slices.Contains(me.Permissions.Anywhere, a) {
				t.Errorf("anywhere must contain every instance action; missing %q", a)
			}
		}
	})

	t.Run("read-only config token", func(t *testing.T) {
		var minted struct{ Token string }
		body := fmt.Sprintf(`{"name":"probe","scope":{"kind":"config","id":%q},"access":"read"}`, configID)
		if code := doAuthed(t, "POST", ts.URL+"/v1/tokens", cookie, "", body, &minted); code != 200 {
			t.Fatalf("mint: %d", code)
		}

		var me meResult
		if code := doAuthed(t, "GET", ts.URL+"/v1/auth/me", "", minted.Token, "", &me); code != 200 {
			t.Fatalf("token me: %d", code)
		}
		if me.Kind != "service_token" {
			t.Fatalf("kind = %q", me.Kind)
		}
		if len(me.Permissions.Instance) != 0 {
			t.Errorf("a config token has no instance reach, got %v", me.Permissions.Instance)
		}
		if !slices.Contains(me.Permissions.Anywhere, "secret:read") {
			t.Errorf("a read token should hold secret:read, got %v", me.Permissions.Anywhere)
		}
		if slices.Contains(me.Permissions.Anywhere, "secret:write") {
			t.Error("a READ token must never report secret:write")
		}
	})
}

// The hint is not a grant. A principal that forges or ignores the payload gains
// nothing, because the endpoints re-decide server-side — this asserts that
// directly rather than trusting the comment.
func TestMePermissionsAreOnlyAHintE2E(t *testing.T) {
	ts, _, email, password, configID := authStackFull(t)
	cookie := login(t, ts.URL, email, password)

	var minted struct{ Token string }
	body := fmt.Sprintf(`{"name":"probe","scope":{"kind":"config","id":%q},"access":"read"}`, configID)
	if code := doAuthed(t, "POST", ts.URL+"/v1/tokens", cookie, "", body, &minted); code != 200 {
		t.Fatalf("mint: %d", code)
	}

	// The token's own /me says it holds nothing at instance scope. The server
	// must enforce that regardless of what any client believes.
	var env errEnvelope
	if code := doAuthed(t, "GET", ts.URL+"/v1/users", "", minted.Token, "", &env); code != 403 {
		t.Fatalf("a config token must be refused instance-scoped user management, got %d", code)
	}
}
