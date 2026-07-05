package api

import (
	"net/http"
	"testing"
)

func TestEnvironmentsE2E(t *testing.T) {
	ts, _, email, password, _ := authStackFull(t)
	cookie := login(t, ts.URL, email, password)

	var proj projectResponse
	if code := doAuthed(t, "POST", ts.URL+"/v1/projects", cookie, "",
		`{"slug":"svc","name":"Svc"}`, &proj); code != http.StatusCreated {
		t.Fatalf("project create: %d", code)
	}

	var env envResponse
	if code := doAuthed(t, "POST", ts.URL+"/v1/projects/"+proj.ID+"/environments", cookie, "",
		`{"slug":"staging","name":"Staging"}`, &env); code != http.StatusCreated {
		t.Fatalf("env create: %d", code)
	}
	if env.ProjectID != proj.ID || env.Slug != "staging" {
		t.Fatalf("bad env: %+v", env)
	}

	var list struct {
		Environments []envResponse `json:"environments"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/projects/"+proj.ID+"/environments", cookie, "", "", &list); code != 200 {
		t.Fatalf("env list: %d", code)
	}
	if len(list.Environments) != 1 {
		t.Fatalf("want 1 env, got %d", len(list.Environments))
	}

	// Soft-delete -> restore -> destroy.
	base := ts.URL + "/v1/projects/" + proj.ID + "/environments/" + env.ID
	if code := doAuthed(t, "DELETE", base, cookie, "", "", nil); code != http.StatusNoContent {
		t.Fatalf("env soft-delete: %d", code)
	}
	if code := doAuthed(t, "GET", base, cookie, "", "", nil); code != http.StatusNotFound {
		t.Fatalf("env get after delete: %d", code)
	}
	if code := doAuthed(t, "POST", base+"/restore", cookie, "", "", nil); code != 200 {
		t.Fatalf("env restore: %d", code)
	}
	if code := doAuthed(t, "DELETE", base+"?destroy=true", cookie, "", "", nil); code != http.StatusNoContent {
		t.Fatalf("env destroy: %d", code)
	}
}
