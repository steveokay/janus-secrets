package api

import (
	"net/http"
	"testing"
)

// makeUser creates a user via POST /v1/users (admin), returns its id + one-time
// password, and grants it `role` at instance scope.
func makeUser(t *testing.T, ts, adminCookie, email, role string) (string, string) {
	t.Helper()
	var created struct{ ID, Password string }
	if code := doAuthed(t, "POST", ts+"/v1/users", adminCookie, "",
		`{"email":"`+email+`"}`, &created); code != 200 {
		t.Fatalf("create user %s: %d", email, code)
	}
	if code := doAuthed(t, "PUT", ts+"/v1/instance/members/"+created.ID, adminCookie, "",
		`{"role":"`+role+`"}`, nil); code != http.StatusNoContent {
		t.Fatalf("grant %s to %s: %d", role, email, code)
	}
	return created.ID, created.Password
}

func TestSecretsRBACMatrix(t *testing.T) {
	ts, _, adminEmail, adminPass, cid := authStackFull(t)
	admin := login(t, ts.URL, adminEmail, adminPass)

	_, viewerPass := makeUser(t, ts.URL, admin, "viewer@corp.io", "viewer")
	_, devPass := makeUser(t, ts.URL, admin, "dev@corp.io", "developer")
	viewer := login(t, ts.URL, "viewer@corp.io", viewerPass)
	dev := login(t, ts.URL, "dev@corp.io", devPass)

	sec := ts.URL + "/v1/configs/" + cid + "/secrets"

	// Seed a secret as admin so the config has a version to masked-list
	// (ListSecrets reports 404 for a config with no version yet).
	if code := doAuthed(t, "PUT", sec, admin, "", `{"changes":[{"key":"SEED","value":"s"}]}`, nil); code != 200 {
		t.Fatalf("admin seed write: %d", code)
	}

	// viewer: can masked-list and reveal; cannot write; cannot create project.
	if code := doAuthed(t, "GET", sec, viewer, "", "", nil); code != 200 {
		t.Fatalf("viewer masked list: %d", code)
	}
	if code := doAuthed(t, "PUT", sec, viewer, "", `{"changes":[{"key":"X","value":"1"}]}`, nil); code != http.StatusForbidden {
		t.Fatalf("viewer write: want 403, got %d", code)
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/projects", viewer, "", `{"slug":"nope","name":"n"}`, nil); code != http.StatusForbidden {
		t.Fatalf("viewer create project: want 403, got %d", code)
	}

	// developer: can write; can create a config; cannot destroy the project (owner-only).
	if code := doAuthed(t, "PUT", sec, dev, "", `{"changes":[{"key":"Y","value":"2"}]}`, nil); code != 200 {
		t.Fatalf("dev write: %d", code)
	}
	// The seeded config's project id: resolve via the config chain by creating an
	// env/config as admin under a fresh project, then have dev attempt destroy.
	var proj projectResponse
	doAuthed(t, "POST", ts.URL+"/v1/projects", admin, "", `{"slug":"rbac","name":"R"}`, &proj)
	if code := doAuthed(t, "DELETE", ts.URL+"/v1/projects/"+proj.ID+"?destroy=true", dev, "", "", nil); code != http.StatusForbidden {
		t.Fatalf("dev destroy project: want 403, got %d", code)
	}

	// owner (admin bootstrap) can destroy.
	if code := doAuthed(t, "DELETE", ts.URL+"/v1/projects/"+proj.ID+"?destroy=true", admin, "", "", nil); code != http.StatusNoContent {
		t.Fatalf("owner destroy project: %d", code)
	}
}
