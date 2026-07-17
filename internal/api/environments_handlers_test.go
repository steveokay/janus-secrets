package api

import (
	"context"
	"net/http"
	"strings"
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

// TestEnvClone covers POST /v1/projects/{pid}/environments/{eid}/clone:
//   - owner clones a source env (with a config holding a secret) -> 201, a new env
//     with a different id and the requested slug; the cloned config's secret reveals
//     to the SAME value as the source (end-to-end proof the clone copied secrets).
//   - developer is forbidden (env:create is admin+).
//   - cloning into an already-existing slug -> 409.
//   - the recorded env.clone audit event carries NO secret value (ids only).
func TestEnvClone(t *testing.T) {
	ts, srv, ownerEmail, ownerPass, _ := authStackFull(t)
	owner := login(t, ts.URL, ownerEmail, ownerPass)

	const cloneSecretValue = "clone-src-secret-value-do-not-leak"

	// Source project + env + config holding a secret.
	var proj struct{ ID string }
	if code := doAuthed(t, "POST", ts.URL+"/v1/projects", owner, "", `{"slug":"envclone","name":"EnvClone"}`, &proj); code != http.StatusCreated {
		t.Fatalf("create project: %d", code)
	}
	var src struct{ ID, Slug string }
	if code := doAuthed(t, "POST", ts.URL+"/v1/projects/"+proj.ID+"/environments", owner, "", `{"slug":"dev","name":"Dev"}`, &src); code != http.StatusCreated {
		t.Fatalf("create src env: %d", code)
	}
	var srcCfg struct{ ID string }
	if code := doAuthed(t, "POST", ts.URL+"/v1/projects/"+proj.ID+"/environments/"+src.ID+"/configs", owner, "", `{"name":"root"}`, &srcCfg); code != http.StatusCreated {
		t.Fatalf("create src config: %d", code)
	}
	if code := doAuthed(t, "PUT", ts.URL+"/v1/configs/"+srcCfg.ID+"/secrets/API_KEY", owner, "",
		`{"value":"`+cloneSecretValue+`"}`, nil); code != http.StatusOK {
		t.Fatalf("write src secret: %d", code)
	}

	// Owner clones the source env -> 201, new env with a distinct id + requested slug.
	var cloned struct{ ID, Slug, Name string }
	if code := doAuthed(t, "POST", ts.URL+"/v1/projects/"+proj.ID+"/environments/"+src.ID+"/clone", owner, "",
		`{"slug":"staging","name":"Staging"}`, &cloned); code != http.StatusCreated {
		t.Fatalf("owner clone: want 201, got %d", code)
	}
	if cloned.ID == "" || cloned.ID == src.ID {
		t.Fatalf("clone must be a new env, got id=%q (src=%q)", cloned.ID, src.ID)
	}
	if cloned.Slug != "staging" {
		t.Fatalf("clone slug: want staging, got %q", cloned.Slug)
	}

	// The cloned config's secret reveals to the same source value (end-to-end).
	var clonedCfgs struct {
		Configs []struct{ ID, Name string } `json:"configs"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/projects/"+proj.ID+"/environments/"+cloned.ID+"/configs", owner, "", "", &clonedCfgs); code != http.StatusOK {
		t.Fatalf("list cloned configs: %d", code)
	}
	var clonedCfgID string
	for _, c := range clonedCfgs.Configs {
		if c.Name == "root" {
			clonedCfgID = c.ID
		}
	}
	if clonedCfgID == "" {
		t.Fatalf("cloned env missing 'root' config: %+v", clonedCfgs.Configs)
	}
	var revealed struct{ Key, Value string }
	if code := doAuthed(t, "GET", ts.URL+"/v1/configs/"+clonedCfgID+"/secrets/API_KEY", owner, "", "", &revealed); code != http.StatusOK {
		t.Fatalf("reveal cloned secret: %d", code)
	}
	if revealed.Value != cloneSecretValue {
		t.Fatalf("cloned secret value = %q, want the source value", revealed.Value)
	}

	// Developer (env:create is admin+) is forbidden.
	var createdUser struct{ ID, Password string }
	if code := doAuthed(t, "POST", ts.URL+"/v1/users", owner, "", `{"email":"dev-envclone@corp.io"}`, &createdUser); code != http.StatusOK {
		t.Fatalf("create user: %d", code)
	}
	if code := doAuthed(t, "PUT", ts.URL+"/v1/projects/"+proj.ID+"/members/"+createdUser.ID, owner, "", `{"role":"developer"}`, nil); code != http.StatusNoContent {
		t.Fatalf("grant developer: %d", code)
	}
	dev := login(t, ts.URL, "dev-envclone@corp.io", createdUser.Password)
	if code := doAuthed(t, "POST", ts.URL+"/v1/projects/"+proj.ID+"/environments/"+src.ID+"/clone", dev, "",
		`{"slug":"devclone","name":"DevClone"}`, nil); code != http.StatusForbidden {
		t.Fatalf("developer clone: want 403, got %d", code)
	}

	// Cloning into an already-existing slug ("staging" now exists) -> 409.
	if code := doAuthed(t, "POST", ts.URL+"/v1/projects/"+proj.ID+"/environments/"+src.ID+"/clone", owner, "",
		`{"slug":"staging","name":"Staging2"}`, nil); code != http.StatusConflict {
		t.Fatalf("duplicate slug clone: want 409, got %d", code)
	}

	// An env.clone audit event was recorded; its detail carries only ids, and NO
	// audit row anywhere contains the source secret value.
	repo := store.NewAuditRepo(srv.st)
	var sawClone bool
	var cloneDetail string
	var fullDump strings.Builder
	if err := repo.Iterate(context.Background(), func(a store.AuditRow) error {
		fullDump.WriteString(a.Action + "|" + a.Resource + "|" + derefStr(a.Detail) + "\n")
		if a.Action == "env.clone" && a.Result == "success" {
			sawClone = true
			cloneDetail = derefStr(a.Detail)
			if a.Resource != "environments/"+cloned.ID {
				t.Fatalf("env.clone resource = %q, want environments/%s", a.Resource, cloned.ID)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("iterate audit: %v", err)
	}
	if !sawClone {
		t.Fatal("no env.clone audit event recorded")
	}
	// Value-free: detail should be the source env id only, never slug/name/secret.
	if cloneDetail != "from:"+src.ID {
		t.Fatalf("env.clone detail = %q, want %q", cloneDetail, "from:"+src.ID)
	}
	if strings.Contains(fullDump.String(), cloneSecretValue) {
		t.Fatal("source secret value leaked into an audit_events row via clone")
	}
	if strings.Contains(fullDump.String(), "staging") {
		t.Fatal("clone slug leaked into an audit_events detail")
	}
}
