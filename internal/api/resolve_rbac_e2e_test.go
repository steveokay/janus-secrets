package api

import (
	"net/http"
	"testing"
)

func TestResolveReferenceRBACAndInheritance(t *testing.T) {
	ts, _, ownerEmail, ownerPass, _ := authStackFull(t)
	owner := login(t, ts.URL, ownerEmail, ownerPass)

	type idResp struct{ ID string `json:"id"` }

	// billing/prod/api HOST=db.internal
	var billing, bEnv, bCfg idResp
	doAuthed(t, "POST", ts.URL+"/v1/projects", owner, "", `{"slug":"billing","name":"Billing"}`, &billing)
	doAuthed(t, "POST", ts.URL+"/v1/projects/"+billing.ID+"/environments", owner, "", `{"slug":"prod","name":"Prod"}`, &bEnv)
	doAuthed(t, "POST", ts.URL+"/v1/projects/"+billing.ID+"/environments/"+bEnv.ID+"/configs", owner, "", `{"name":"api"}`, &bCfg)
	if code := doAuthed(t, "PUT", ts.URL+"/v1/configs/"+bCfg.ID+"/secrets", owner, "", `{"changes":[{"key":"HOST","value":"db.internal"}]}`, nil); code != 200 {
		t.Fatalf("seed billing: %d", code)
	}

	// app/prod/web URL=${projects.billing.prod.api.HOST}
	var app, aEnv, web idResp
	doAuthed(t, "POST", ts.URL+"/v1/projects", owner, "", `{"slug":"app","name":"App"}`, &app)
	doAuthed(t, "POST", ts.URL+"/v1/projects/"+app.ID+"/environments", owner, "", `{"slug":"prod","name":"Prod"}`, &aEnv)
	doAuthed(t, "POST", ts.URL+"/v1/projects/"+app.ID+"/environments/"+aEnv.ID+"/configs", owner, "", `{"name":"web"}`, &web)
	if code := doAuthed(t, "PUT", ts.URL+"/v1/configs/"+web.ID+"/secrets", owner, "", `{"changes":[{"key":"URL","value":"${projects.billing.prod.api.HOST}"}]}`, nil); code != 200 {
		t.Fatalf("seed web: %d", code)
	}

	// developer with read on APP project only (not billing)
	var created struct{ ID, Password string }
	if code := doAuthed(t, "POST", ts.URL+"/v1/users", owner, "", `{"email":"dev-ref@corp.io"}`, &created); code != 200 {
		t.Fatalf("create user: %d", code)
	}
	if code := doAuthed(t, "PUT", ts.URL+"/v1/projects/"+app.ID+"/members/"+created.ID, owner, "", `{"role":"developer"}`, nil); code != http.StatusNoContent {
		t.Fatalf("grant developer on app: %d", code)
	}
	dev := login(t, ts.URL, "dev-ref@corp.io", created.Password)

	webSecrets := ts.URL + "/v1/configs/" + web.ID + "/secrets"

	// (a) resolved reveal dereferences billing (dev can't read) → 403 atomic.
	if code := doAuthed(t, "GET", webSecrets+"?reveal=true", dev, "", "", nil); code != http.StatusForbidden {
		t.Fatalf("forbidden reference: want 403, got %d", code)
	}
	// (b) raw reveal does not dereference → dev can read app → 200.
	if code := doAuthed(t, "GET", webSecrets+"?reveal=true&raw=true", dev, "", "", nil); code != 200 {
		t.Fatalf("raw reveal by dev: want 200, got %d", code)
	}

	// (c) transparent inheritance: token scoped to a version-less branch reads
	//     inherited base values → 200.
	var inhP, inhEnv, base, branch idResp
	doAuthed(t, "POST", ts.URL+"/v1/projects", owner, "", `{"slug":"inh","name":"Inh"}`, &inhP)
	doAuthed(t, "POST", ts.URL+"/v1/projects/"+inhP.ID+"/environments", owner, "", `{"slug":"prod","name":"Prod"}`, &inhEnv)
	doAuthed(t, "POST", ts.URL+"/v1/projects/"+inhP.ID+"/environments/"+inhEnv.ID+"/configs", owner, "", `{"name":"base"}`, &base)
	if code := doAuthed(t, "PUT", ts.URL+"/v1/configs/"+base.ID+"/secrets", owner, "", `{"changes":[{"key":"SHARED","value":"inherited-val"}]}`, nil); code != 200 {
		t.Fatalf("seed base: %d", code)
	}
	doAuthed(t, "POST", ts.URL+"/v1/projects/"+inhP.ID+"/environments/"+inhEnv.ID+"/configs", owner, "", `{"name":"branch","inherits_from":"`+base.ID+`"}`, &branch)

	var minted struct{ Token string `json:"token"` }
	if code := doAuthed(t, "POST", ts.URL+"/v1/tokens", owner, "",
		`{"name":"branch-tok","scope":{"kind":"config","id":"`+branch.ID+`"},"access":"read"}`, &minted); code != 200 || minted.Token == "" {
		t.Fatalf("mint branch token: %d", code)
	}
	var got struct{ Secrets map[string]string `json:"secrets"` }
	if code := doAuthed(t, "GET", ts.URL+"/v1/configs/"+branch.ID+"/secrets?reveal=true", "", minted.Token, "", &got); code != 200 {
		t.Fatalf("branch-scoped token resolved reveal: want 200, got %d", code)
	}
	if got.Secrets["SHARED"] != "inherited-val" {
		t.Fatalf("inherited value = %q, want inherited-val", got.Secrets["SHARED"])
	}
}
