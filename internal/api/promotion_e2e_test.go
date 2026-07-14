package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/steveokay/janus-secrets/internal/store"
)

// TestPromotionAPIE2E exercises the promotion pipeline + config locked-keys
// endpoints: an admin may set the pipeline and lock/unlock keys; a developer
// may read both but is forbidden from mutating (needs promotion:manage).
func TestPromotionAPIE2E(t *testing.T) {
	ts, srv, adminEmail, adminPassword, cid := authStackFull(t)
	ctx := context.Background()
	adminCookie := login(t, ts.URL, adminEmail, adminPassword)

	// A dedicated project for the pipeline (env ids ordered dev -> stg).
	p, err := srv.service.CreateProject(ctx, "promoproj", "Promotion Project")
	if err != nil {
		t.Fatal(err)
	}
	dev, err := srv.service.CreateEnvironment(ctx, p.ID, "dev", "Dev")
	if err != nil {
		t.Fatal(err)
	}
	stg, err := srv.service.CreateEnvironment(ctx, p.ID, "staging", "Staging")
	if err != nil {
		t.Fatal(err)
	}

	// A developer: project-scoped developer on the pipeline project AND on the
	// authstack project that owns cid, so they can read locked-keys but cannot
	// mutate (developer lacks promotion:manage).
	devID, devPassword, err := srv.auth.CreateUser(ctx, "promo-dev@corp.io")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.authz.Grant(ctx, store.RoleBindingInput{
		SubjectUserID: devID, ScopeLevel: "project", ProjectID: &p.ID, Role: "developer",
	}); err != nil {
		t.Fatal(err)
	}
	// The config cid belongs to the "authstack" project; grant developer there too.
	cfgProjID := configProjectID(t, srv, cid)
	if err := srv.authz.Grant(ctx, store.RoleBindingInput{
		SubjectUserID: devID, ScopeLevel: "project", ProjectID: &cfgProjID, Role: "developer",
	}); err != nil {
		t.Fatal(err)
	}
	devCookie := login(t, ts.URL, "promo-dev@corp.io", devPassword)

	// --- Pipeline ---

	// Admin sets the pipeline.
	var putResp struct {
		EnvironmentIDs []string `json:"environment_ids"`
	}
	if code := doAuthed(t, "PUT", ts.URL+"/v1/projects/"+p.ID+"/pipeline", adminCookie, "",
		`{"environment_ids":["`+dev.ID+`","`+stg.ID+`"]}`, &putResp); code != 200 {
		t.Fatalf("admin pipeline PUT: want 200, got %d", code)
	}
	if len(putResp.EnvironmentIDs) != 2 || putResp.EnvironmentIDs[0] != dev.ID || putResp.EnvironmentIDs[1] != stg.ID {
		t.Fatalf("pipeline PUT echo: got %+v", putResp.EnvironmentIDs)
	}

	// Developer reads the pipeline.
	var getResp struct {
		EnvironmentIDs []string `json:"environment_ids"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/projects/"+p.ID+"/pipeline", devCookie, "", "", &getResp); code != 200 {
		t.Fatalf("developer pipeline GET: want 200, got %d", code)
	}
	if len(getResp.EnvironmentIDs) != 2 || getResp.EnvironmentIDs[0] != dev.ID || getResp.EnvironmentIDs[1] != stg.ID {
		t.Fatalf("pipeline GET: got %+v", getResp.EnvironmentIDs)
	}

	// Developer cannot mutate the pipeline.
	if code := doAuthed(t, "PUT", ts.URL+"/v1/projects/"+p.ID+"/pipeline", devCookie, "",
		`{"environment_ids":["`+stg.ID+`"]}`, nil); code != http.StatusForbidden {
		t.Fatalf("developer pipeline PUT: want 403, got %d", code)
	}

	// --- Locked keys ---

	// Developer cannot lock a key.
	if code := doAuthed(t, "POST", ts.URL+"/v1/configs/"+cid+"/locked-keys", devCookie, "",
		`{"key":"DATABASE_URL"}`, nil); code != http.StatusForbidden {
		t.Fatalf("developer locked-key POST: want 403, got %d", code)
	}

	// Admin locks a key.
	if code := doAuthed(t, "POST", ts.URL+"/v1/configs/"+cid+"/locked-keys", adminCookie, "",
		`{"key":"DATABASE_URL"}`, nil); code != 200 {
		t.Fatalf("admin locked-key POST: want 200, got %d", code)
	}

	// Developer reads locked keys.
	var lkResp struct {
		Keys []string `json:"keys"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/configs/"+cid+"/locked-keys", devCookie, "", "", &lkResp); code != 200 {
		t.Fatalf("locked-keys GET: want 200, got %d", code)
	}
	if len(lkResp.Keys) != 1 || lkResp.Keys[0] != "DATABASE_URL" {
		t.Fatalf("locked-keys GET: got %+v", lkResp.Keys)
	}

	// Admin unlocks the key.
	if code := doAuthed(t, "DELETE", ts.URL+"/v1/configs/"+cid+"/locked-keys/DATABASE_URL", adminCookie, "", "", nil); code != 200 {
		t.Fatalf("admin locked-key DELETE: want 200, got %d", code)
	}

	// Now empty.
	lkResp.Keys = nil
	if code := doAuthed(t, "GET", ts.URL+"/v1/configs/"+cid+"/locked-keys", adminCookie, "", "", &lkResp); code != 200 {
		t.Fatalf("locked-keys GET after unlock: want 200, got %d", code)
	}
	if len(lkResp.Keys) != 0 {
		t.Fatalf("locked-keys GET after unlock: want empty, got %+v", lkResp.Keys)
	}
}

// configProjectID resolves the owning project id of a config via the store
// (configs join to projects through environments).
func configProjectID(t *testing.T, srv *Server, cid string) string {
	t.Helper()
	c, err := store.NewConfigRepo(srv.st).Get(context.Background(), cid)
	if err != nil {
		t.Fatal(err)
	}
	e, err := store.NewEnvironmentRepo(srv.st).Get(context.Background(), c.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	return e.ProjectID
}
