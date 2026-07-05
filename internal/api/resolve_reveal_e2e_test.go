package api

import (
	"testing"
)

func TestResolvedRevealAndRaw(t *testing.T) {
	ts, _, email, password, _ := authStackFull(t)
	cookie := login(t, ts.URL, email, password)

	// billing/prod/api HOST=db.internal
	var billing struct {
		ID string `json:"id"`
	}
	doAuthed(t, "POST", ts.URL+"/v1/projects", cookie, "", `{"slug":"billing","name":"Billing"}`, &billing)
	var bEnv struct {
		ID string `json:"id"`
	}
	doAuthed(t, "POST", ts.URL+"/v1/projects/"+billing.ID+"/environments", cookie, "", `{"slug":"prod","name":"Prod"}`, &bEnv)
	var bCfg struct {
		ID string `json:"id"`
	}
	doAuthed(t, "POST", ts.URL+"/v1/projects/"+billing.ID+"/environments/"+bEnv.ID+"/configs", cookie, "", `{"name":"api"}`, &bCfg)
	if code := doAuthed(t, "PUT", ts.URL+"/v1/configs/"+bCfg.ID+"/secrets", cookie, "", `{"changes":[{"key":"HOST","value":"db.internal"}]}`, nil); code != 200 {
		t.Fatalf("seed billing HOST: %d", code)
	}

	// app/prod/web URL=${projects.billing.prod.api.HOST}
	var app struct {
		ID string `json:"id"`
	}
	doAuthed(t, "POST", ts.URL+"/v1/projects", cookie, "", `{"slug":"app","name":"App"}`, &app)
	var aEnv struct {
		ID string `json:"id"`
	}
	doAuthed(t, "POST", ts.URL+"/v1/projects/"+app.ID+"/environments", cookie, "", `{"slug":"prod","name":"Prod"}`, &aEnv)
	var web struct {
		ID string `json:"id"`
	}
	doAuthed(t, "POST", ts.URL+"/v1/projects/"+app.ID+"/environments/"+aEnv.ID+"/configs", cookie, "", `{"name":"web"}`, &web)
	if code := doAuthed(t, "PUT", ts.URL+"/v1/configs/"+web.ID+"/secrets", cookie, "", `{"changes":[{"key":"URL","value":"${projects.billing.prod.api.HOST}"}]}`, nil); code != 200 {
		t.Fatalf("seed web URL: %d", code)
	}

	base := ts.URL + "/v1/configs/" + web.ID + "/secrets"
	var resolved struct {
		Secrets map[string]string `json:"secrets"`
	}
	if code := doAuthed(t, "GET", base+"?reveal=true", cookie, "", "", &resolved); code != 200 {
		t.Fatalf("resolved reveal: %d", code)
	}
	if resolved.Secrets["URL"] != "db.internal" {
		t.Fatalf("URL resolved = %q, want db.internal", resolved.Secrets["URL"])
	}
	var raw struct {
		Secrets map[string]string `json:"secrets"`
	}
	if code := doAuthed(t, "GET", base+"?reveal=true&raw=true", cookie, "", "", &raw); code != 200 {
		t.Fatalf("raw reveal: %d", code)
	}
	if raw.Secrets["URL"] != "${projects.billing.prod.api.HOST}" {
		t.Fatalf("URL raw = %q", raw.Secrets["URL"])
	}
	// Single-key resolved.
	var one struct {
		Value string `json:"value"`
	}
	if code := doAuthed(t, "GET", base+"/URL", cookie, "", "", &one); code != 200 || one.Value != "db.internal" {
		t.Fatalf("single-key resolved: %d %q", code, one.Value)
	}
}
