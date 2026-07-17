package api

import (
	"net/http"
	"testing"
)

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
