package api

import (
	"net/http"
	"testing"

	"github.com/steveokay/janus-secrets/internal/store"
)

// TestEnvRename covers PATCH /v1/projects/{pid}/environments/{eid}: owner can
// rename (slug stays immutable), developer is forbidden (lacks env:update),
// empty/whitespace name is a validation error, and an unknown eid 404s.
func TestEnvRename(t *testing.T) {
	ts, _, ownerEmail, ownerPass, _ := authStackFull(t)
	owner := login(t, ts.URL, ownerEmail, ownerPass)

	var proj struct{ ID string }
	if code := doAuthed(t, "POST", ts.URL+"/v1/projects", owner, "", `{"slug":"envrename","name":"EnvRename"}`, &proj); code != http.StatusCreated {
		t.Fatalf("create project: %d", code)
	}
	var env struct{ ID, Slug, Name string }
	if code := doAuthed(t, "POST", ts.URL+"/v1/projects/"+proj.ID+"/environments", owner, "", `{"slug":"stage","name":"Stage"}`, &env); code != http.StatusCreated {
		t.Fatalf("create env: %d", code)
	}

	// Owner renames successfully; slug is unchanged.
	var renamed struct{ ID, Slug, Name string }
	if code := doAuthed(t, "PATCH", ts.URL+"/v1/projects/"+proj.ID+"/environments/"+env.ID, owner, "", `{"name":"Staging"}`, &renamed); code != http.StatusOK {
		t.Fatalf("owner rename: %d", code)
	}
	if renamed.Name != "Staging" {
		t.Fatalf("want name Staging, got %q", renamed.Name)
	}
	if renamed.Slug != "stage" {
		t.Fatalf("slug must stay immutable, got %q", renamed.Slug)
	}

	// Developer (env:update is admin+) is forbidden.
	var createdUser struct{ ID, Password string }
	if code := doAuthed(t, "POST", ts.URL+"/v1/users", owner, "", `{"email":"dev-envrename@corp.io"}`, &createdUser); code != http.StatusOK {
		t.Fatalf("create user: %d", code)
	}
	if code := doAuthed(t, "PUT", ts.URL+"/v1/projects/"+proj.ID+"/members/"+createdUser.ID, owner, "", `{"role":"developer"}`, nil); code != http.StatusNoContent {
		t.Fatalf("grant developer: %d", code)
	}
	dev := login(t, ts.URL, "dev-envrename@corp.io", createdUser.Password)
	if code := doAuthed(t, "PATCH", ts.URL+"/v1/projects/"+proj.ID+"/environments/"+env.ID, dev, "", `{"name":"ByDev"}`, nil); code != http.StatusForbidden {
		t.Fatalf("developer rename: want 403, got %d", code)
	}

	// Empty/whitespace name -> validation error.
	if code := doAuthed(t, "PATCH", ts.URL+"/v1/projects/"+proj.ID+"/environments/"+env.ID, owner, "", `{"name":""}`, nil); code != http.StatusBadRequest {
		t.Fatalf("empty name: want 400, got %d", code)
	}
	if code := doAuthed(t, "PATCH", ts.URL+"/v1/projects/"+proj.ID+"/environments/"+env.ID, owner, "", `{"name":"   "}`, nil); code != http.StatusBadRequest {
		t.Fatalf("whitespace name: want 400, got %d", code)
	}

	// Unknown eid -> 404.
	if code := doAuthed(t, "PATCH", ts.URL+"/v1/projects/"+proj.ID+"/environments/00000000-0000-0000-0000-000000000000", owner, "", `{"name":"X"}`, nil); code != http.StatusNotFound {
		t.Fatalf("unknown eid: want 404, got %d", code)
	}
}

// TestEnvListLastActivity asserts GET /v1/projects/{pid}/environments surfaces
// last_activity_at: non-null for an environment with a saved config version,
// JSON null for an environment with none.
func TestEnvListLastActivity(t *testing.T) {
	ts, srv, email, password, cid := authStackFull(t)
	cookie := login(t, ts.URL, email, password)

	// authStackFull already created project "authstack" > env "prod" > config
	// cid; save a version so the "prod" env has activity.
	if code := doAuthed(t, "PUT", ts.URL+"/v1/configs/"+cid+"/secrets", cookie, "",
		`{"changes":[{"key":"A","value":"1"}]}`, nil); code != http.StatusOK {
		t.Fatalf("save secrets: %d", code)
	}

	cfg, err := store.NewConfigRepo(srv.st).Get(t.Context(), cid)
	if err != nil {
		t.Fatal(err)
	}
	prodEnv, err := store.NewEnvironmentRepo(srv.st).Get(t.Context(), cfg.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}

	// A second environment in the same project with no config versions.
	emptyEnv, err := srv.service.CreateEnvironment(t.Context(), prodEnv.ProjectID, "dev", "Dev")
	if err != nil {
		t.Fatal(err)
	}

	var list struct {
		Environments []map[string]any `json:"environments"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/projects/"+prodEnv.ProjectID+"/environments", cookie, "", "", &list); code != http.StatusOK {
		t.Fatalf("list: %d", code)
	}

	var foundActive, foundEmpty bool
	for _, e := range list.Environments {
		id, _ := e["id"].(string)
		switch id {
		case prodEnv.ID:
			foundActive = true
			if e["last_activity_at"] == nil {
				t.Fatalf("active env: want non-null last_activity_at, got nil: %+v", e)
			}
		case emptyEnv.ID:
			foundEmpty = true
			if v, ok := e["last_activity_at"]; !ok {
				t.Fatalf("empty env: last_activity_at key missing: %+v", e)
			} else if v != nil {
				t.Fatalf("empty env: want null last_activity_at, got %v", v)
			}
		}
	}
	if !foundActive || !foundEmpty {
		t.Fatalf("did not find both envs in list: active=%v empty=%v list=%+v", foundActive, foundEmpty, list.Environments)
	}
}
