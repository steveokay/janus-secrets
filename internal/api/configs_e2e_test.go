package api

import (
	"net/http"
	"testing"
)

func TestConfigsE2E(t *testing.T) {
	ts, _, email, password, _ := authStackFull(t)
	cookie := login(t, ts.URL, email, password)

	var proj projectResponse
	doAuthed(t, "POST", ts.URL+"/v1/projects", cookie, "", `{"slug":"cfg","name":"Cfg"}`, &proj)
	var env envResponse
	doAuthed(t, "POST", ts.URL+"/v1/projects/"+proj.ID+"/environments", cookie, "", `{"slug":"dev","name":"Dev"}`, &env)

	base := ts.URL + "/v1/projects/" + proj.ID + "/environments/" + env.ID + "/configs"
	var cfg configResponse
	if code := doAuthed(t, "POST", base, cookie, "", `{"name":"root"}`, &cfg); code != http.StatusCreated {
		t.Fatalf("config create: %d", code)
	}
	if cfg.EnvironmentID != env.ID || cfg.Name != "root" {
		t.Fatalf("bad config: %+v", cfg)
	}

	var list struct {
		Configs []configResponse `json:"configs"`
	}
	if code := doAuthed(t, "GET", base, cookie, "", "", &list); code != 200 || len(list.Configs) != 1 {
		t.Fatalf("config list: %d, n=%d", code, len(list.Configs))
	}

	cbase := ts.URL + "/v1/configs/" + cfg.ID
	if code := doAuthed(t, "GET", cbase, cookie, "", "", nil); code != 200 {
		t.Fatalf("config get: %d", code)
	}
	if code := doAuthed(t, "DELETE", cbase, cookie, "", "", nil); code != http.StatusNoContent {
		t.Fatalf("config soft-delete: %d", code)
	}
	if code := doAuthed(t, "GET", cbase, cookie, "", "", nil); code != http.StatusNotFound {
		t.Fatalf("config get after delete: %d", code)
	}
	if code := doAuthed(t, "POST", cbase+"/restore", cookie, "", "", nil); code != 200 {
		t.Fatalf("config restore: %d", code)
	}
	if code := doAuthed(t, "DELETE", cbase+"?destroy=true", cookie, "", "", nil); code != http.StatusNoContent {
		t.Fatalf("config destroy: %d", code)
	}
}
