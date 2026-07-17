package api

import (
	"net/http"
	"testing"

	"github.com/steveokay/janus-secrets/internal/store"
)

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
