package api

import (
	"net/http"
	"testing"
)

// TestProjectRename covers PATCH /v1/projects/{pid}: owner can rename (slug
// stays immutable), developer is forbidden (lacks project:update), empty/
// whitespace name is a validation error, and an unknown pid 404s.
func TestProjectRename(t *testing.T) {
	ts, _, ownerEmail, ownerPass, _ := authStackFull(t)
	owner := login(t, ts.URL, ownerEmail, ownerPass)

	var proj struct{ ID, Slug, Name string }
	if code := doAuthed(t, "POST", ts.URL+"/v1/projects", owner, "", `{"slug":"renameme","name":"Original"}`, &proj); code != http.StatusCreated {
		t.Fatalf("create project: %d", code)
	}

	// Owner renames successfully; slug is unchanged.
	var renamed struct{ ID, Slug, Name string }
	if code := doAuthed(t, "PATCH", ts.URL+"/v1/projects/"+proj.ID, owner, "", `{"name":"Renamed"}`, &renamed); code != http.StatusOK {
		t.Fatalf("owner rename: %d", code)
	}
	if renamed.Name != "Renamed" {
		t.Fatalf("want name Renamed, got %q", renamed.Name)
	}
	if renamed.Slug != "renameme" {
		t.Fatalf("slug must stay immutable, got %q", renamed.Slug)
	}

	// Developer (project:update is admin+) is forbidden.
	var createdUser struct{ ID, Password string }
	if code := doAuthed(t, "POST", ts.URL+"/v1/users", owner, "", `{"email":"dev-rename@corp.io"}`, &createdUser); code != http.StatusOK {
		t.Fatalf("create user: %d", code)
	}
	if code := doAuthed(t, "PUT", ts.URL+"/v1/projects/"+proj.ID+"/members/"+createdUser.ID, owner, "", `{"role":"developer"}`, nil); code != http.StatusNoContent {
		t.Fatalf("grant developer: %d", code)
	}
	dev := login(t, ts.URL, "dev-rename@corp.io", createdUser.Password)
	if code := doAuthed(t, "PATCH", ts.URL+"/v1/projects/"+proj.ID, dev, "", `{"name":"ByDev"}`, nil); code != http.StatusForbidden {
		t.Fatalf("developer rename: want 403, got %d", code)
	}

	// Empty/whitespace name -> validation error.
	if code := doAuthed(t, "PATCH", ts.URL+"/v1/projects/"+proj.ID, owner, "", `{"name":""}`, nil); code != http.StatusBadRequest {
		t.Fatalf("empty name: want 400, got %d", code)
	}
	if code := doAuthed(t, "PATCH", ts.URL+"/v1/projects/"+proj.ID, owner, "", `{"name":"   "}`, nil); code != http.StatusBadRequest {
		t.Fatalf("whitespace name: want 400, got %d", code)
	}

	// Unknown pid -> 404.
	if code := doAuthed(t, "PATCH", ts.URL+"/v1/projects/00000000-0000-0000-0000-000000000000", owner, "", `{"name":"X"}`, nil); code != http.StatusNotFound {
		t.Fatalf("unknown pid: want 404, got %d", code)
	}
}

// TestProjectListLastActivity asserts GET /v1/projects surfaces
// last_activity_at: non-null for a project with a saved config version,
// JSON null for a project with none.
func TestProjectListLastActivity(t *testing.T) {
	ts, srv, email, password, cid := authStackFull(t)
	cookie := login(t, ts.URL, email, password)

	// authStackFull already created project "authstack" with config cid;
	// save a version on it so it has activity.
	if code := doAuthed(t, "PUT", ts.URL+"/v1/configs/"+cid+"/secrets", cookie, "",
		`{"changes":[{"key":"A","value":"1"}]}`, nil); code != http.StatusOK {
		t.Fatalf("save secrets: %d", code)
	}

	// A second, empty project with no config versions.
	if _, err := srv.service.CreateProject(t.Context(), "empty-proj", "Empty"); err != nil {
		t.Fatal(err)
	}

	var list struct {
		Projects []map[string]any `json:"projects"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/projects", cookie, "", "", &list); code != http.StatusOK {
		t.Fatalf("list: %d", code)
	}

	var foundActive, foundEmpty bool
	for _, p := range list.Projects {
		slug, _ := p["slug"].(string)
		switch slug {
		case "authstack":
			foundActive = true
			if p["last_activity_at"] == nil {
				t.Fatalf("active project: want non-null last_activity_at, got nil: %+v", p)
			}
		case "empty-proj":
			foundEmpty = true
			if v, ok := p["last_activity_at"]; !ok {
				t.Fatalf("empty project: last_activity_at key missing: %+v", p)
			} else if v != nil {
				t.Fatalf("empty project: want null last_activity_at, got %v", v)
			}
		}
	}
	if !foundActive || !foundEmpty {
		t.Fatalf("did not find both projects in list: active=%v empty=%v list=%+v", foundActive, foundEmpty, list.Projects)
	}
}
