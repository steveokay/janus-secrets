package api

import (
	"net/http"
	"strings"
	"testing"
)

func TestProjectsE2E(t *testing.T) {
	ts, _, email, password, _ := authStackFull(t)
	cookie := login(t, ts.URL, email, password)

	// Create (admin/owner has project:create at instance).
	var created projectResponse
	if code := doAuthed(t, "POST", ts.URL+"/v1/projects", cookie, "",
		`{"slug":"billing","name":"Billing"}`, &created); code != http.StatusCreated {
		t.Fatalf("create: %d", code)
	}
	if created.ID == "" || created.Slug != "billing" {
		t.Fatalf("bad create response: %+v", created)
	}

	// Duplicate slug -> 409.
	if code := doAuthed(t, "POST", ts.URL+"/v1/projects", cookie, "",
		`{"slug":"billing","name":"dup"}`, nil); code != http.StatusConflict {
		t.Fatalf("dup slug: want 409, got %d", code)
	}

	// Get + list.
	var got projectResponse
	if code := doAuthed(t, "GET", ts.URL+"/v1/projects/"+created.ID, cookie, "", "", &got); code != 200 {
		t.Fatalf("get: %d", code)
	}
	var list struct {
		Projects []projectResponse `json:"projects"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/projects", cookie, "", "", &list); code != 200 {
		t.Fatalf("list: %d", code)
	}
	if len(list.Projects) == 0 {
		t.Fatal("list empty")
	}

	// Soft-delete -> 204, then get -> 404, then restore -> 200, then get -> 200.
	if code := doAuthed(t, "DELETE", ts.URL+"/v1/projects/"+created.ID, cookie, "", "", nil); code != http.StatusNoContent {
		t.Fatalf("soft-delete: %d", code)
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/projects/"+created.ID, cookie, "", "", nil); code != http.StatusNotFound {
		t.Fatalf("get after delete: want 404, got %d", code)
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/projects/"+created.ID+"/restore", cookie, "", "", nil); code != 200 {
		t.Fatalf("restore: %d", code)
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/projects/"+created.ID, cookie, "", "", nil); code != 200 {
		t.Fatalf("get after restore: %d", code)
	}

	// Hard-destroy (owner) -> 204, then get -> 404.
	if code := doAuthed(t, "DELETE", ts.URL+"/v1/projects/"+created.ID+"?destroy=true", cookie, "", "", nil); code != http.StatusNoContent {
		t.Fatalf("destroy: %d", code)
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/projects/"+created.ID, cookie, "", "", nil); code != http.StatusNotFound {
		t.Fatalf("get after destroy: %d", code)
	}

	// Audit: create/delete/restore recorded; the export contains project.create.
	body := func() string {
		_, b := rawGet(t, ts.URL+"/v1/audit/export?format=jsonl&action=project.create", cookie)
		return b
	}()
	if !strings.Contains(body, "project.create") {
		t.Fatalf("expected project.create in audit export, got:\n%s", body)
	}
}
