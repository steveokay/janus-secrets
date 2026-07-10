package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/steveokay/janus-secrets/internal/secrets"
)

// TestSyncCRUDViaAPI drives the sync-target lifecycle as admin: create a github
// target (masked response must not echo the PAT sent), list by project, get by
// id, patch the interval, delete, then confirm the target is gone. It does NOT
// assert a successful sync-now network call: the engine's githubBaseURL points
// at api.github.com in a booted server and there is no setter, so a real reach
// would need live GitHub. CRUD + masking is the contract this task owns; the
// authz path is identical to rotation's (already covered by rotation e2e).
func TestSyncCRUDViaAPI(t *testing.T) {
	ts, srv, adminEmail, adminPass, cid := authStackFull(t)
	admin := login(t, ts.URL, adminEmail, adminPass)

	// Seed a secret in the config so a target has something to manage.
	if _, err := srv.service.SetSecrets(context.Background(), cid, []secrets.SecretChange{
		{Key: "API_KEY", Value: []byte("v1")},
	}, "seed", "test"); err != nil {
		t.Fatal(err)
	}

	const rawPAT = "ghp_should_not_leak"
	createBody := `{"config_id":"` + cid + `","provider":"github","interval_seconds":3600,` +
		`"addr":{"owner":"o","repo":"r"},"creds":{"pat":"` + rawPAT + `"}}`

	var created struct {
		ID              string `json:"id"`
		ProjectID       string `json:"project_id"`
		ConfigID        string `json:"config_id"`
		Provider        string `json:"provider"`
		IntervalSeconds int64  `json:"interval_seconds"`
		Status          string `json:"status"`
		Prune           bool   `json:"prune"`
	}
	req, err := http.NewRequest("POST", ts.URL+"/v1/sync/targets", strings.NewReader(createBody))
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: admin})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	rawBody := readAllString(t, resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d %s", resp.StatusCode, rawBody)
	}
	// Masking: the create response must not leak the PAT or the creds field.
	if strings.Contains(rawBody, rawPAT) || strings.Contains(rawBody, "\"pat\"") || strings.Contains(rawBody, "creds") {
		t.Fatalf("create response leaked github creds: %s", rawBody)
	}
	if err := decodeInto(rawBody, &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.ID == "" || created.Provider != "github" || created.ConfigID != cid || !created.Prune {
		t.Fatalf("created = %+v", created)
	}

	// List by project.
	var list struct {
		Targets []struct {
			ID string `json:"id"`
		} `json:"targets"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/sync/targets?project_id="+created.ProjectID, admin, "", "", &list); code != http.StatusOK {
		t.Fatalf("list: %d", code)
	}
	found := false
	for _, tg := range list.Targets {
		if tg.ID == created.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("list missing created target: %+v", list)
	}

	// Get by id.
	var got struct {
		ID              string `json:"id"`
		IntervalSeconds int64  `json:"interval_seconds"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/sync/targets/"+created.ID, admin, "", "", &got); code != http.StatusOK || got.ID != created.ID {
		t.Fatalf("get: %d %+v", code, got)
	}

	// Patch interval.
	var updated struct {
		IntervalSeconds int64 `json:"interval_seconds"`
	}
	if code := doAuthed(t, "PATCH", ts.URL+"/v1/sync/targets/"+created.ID, admin, "",
		`{"interval_seconds":7200}`, &updated); code != http.StatusOK || updated.IntervalSeconds != 7200 {
		t.Fatalf("patch: %d %+v", code, updated)
	}

	// Delete.
	var del struct {
		Deleted bool `json:"deleted"`
	}
	if code := doAuthed(t, "DELETE", ts.URL+"/v1/sync/targets/"+created.ID, admin, "", "", &del); code != http.StatusOK || !del.Deleted {
		t.Fatalf("delete: %d %+v", code, del)
	}

	// Get after delete -> 404 sync_not_found.
	var env errEnvelope
	if code := doAuthed(t, "GET", ts.URL+"/v1/sync/targets/"+created.ID, admin, "", "", &env); code != http.StatusNotFound || env.Error.Code != CodeSyncNotFound {
		t.Fatalf("get after delete: %d %+v", code, env)
	}
}

// TestSyncForbiddenForNonAdmin confirms sync:manage is not granted to lower
// instance roles — a developer gets 403 on create.
func TestSyncForbiddenForNonAdmin(t *testing.T) {
	ts, _, adminEmail, adminPass, cid := authStackFull(t)
	admin := login(t, ts.URL, adminEmail, adminPass)

	_, devPass := makeUser(t, ts.URL, admin, "syncdev@corp.io", "developer")
	dev := login(t, ts.URL, "syncdev@corp.io", devPass)

	createBody := `{"config_id":"` + cid + `","provider":"github","interval_seconds":3600,` +
		`"addr":{"owner":"o","repo":"r"},"creds":{"pat":"ghp_x"}}`
	if code := doAuthed(t, "POST", ts.URL+"/v1/sync/targets", dev, "", createBody, nil); code != http.StatusForbidden {
		t.Fatalf("developer create: want 403, got %d", code)
	}
}
