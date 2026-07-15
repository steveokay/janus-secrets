package api

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	"github.com/steveokay/janus-secrets/internal/secrets"
	"github.com/steveokay/janus-secrets/internal/store"
)

// TestPromoteApplyE2E exercises the promote preview + apply endpoints against a
// dev->staging->prod pipeline. It asserts: owner can preview (diff with statuses)
// and apply selected keys forward (value verified promoted); a developer lacking
// secret:promote on the target is forbidden; an illegal pipeline step is 409; and
// a locked target key blocks apply with 409.
func TestPromoteApplyE2E(t *testing.T) {
	ts, srv, ownerEmail, ownerPassword, _ := authStackFull(t)
	ctx := context.Background()
	ownerCookie := login(t, ts.URL, ownerEmail, ownerPassword)

	// A dedicated project with a dev->staging->prod pipeline.
	p, err := srv.service.CreateProject(ctx, "promoapply", "Promote Apply Project")
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
	prod, err := srv.service.CreateEnvironment(ctx, p.ID, "prod", "Prod")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.NewPipelineRepo(srv.st).Set(ctx, p.ID, []string{dev.ID, stg.ID, prod.ID}); err != nil {
		t.Fatal(err)
	}

	// Same-named "root" configs in each env.
	devCfg, err := srv.service.CreateConfig(ctx, dev.ID, "root", nil)
	if err != nil {
		t.Fatal(err)
	}
	stgCfg, err := srv.service.CreateConfig(ctx, stg.ID, "root", nil)
	if err != nil {
		t.Fatal(err)
	}
	prodCfg, err := srv.service.CreateConfig(ctx, prod.ID, "root", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Seed dev's config: A and B.
	cv, err := srv.service.SetSecrets(ctx, devCfg.ID, []secrets.SecretChange{
		{Key: "A", Value: []byte("aval")},
		{Key: "B", Value: []byte("bval")},
	}, "seed", "test")
	if err != nil {
		t.Fatal(err)
	}
	srcVersion := cv.Version

	// --- Preview (owner) ---
	var prev struct {
		SourceVersion int `json:"source_version"`
		TargetExists  bool `json:"target_exists"`
		Entries       []struct {
			Key    string `json:"key"`
			Status string `json:"status"`
		} `json:"entries"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/promote/preview?from="+devCfg.ID+"&to="+stgCfg.ID, ownerCookie, "", "", &prev); code != 200 {
		t.Fatalf("owner preview: want 200, got %d", code)
	}
	if len(prev.Entries) == 0 {
		t.Fatalf("preview: want at least one entry, got %+v", prev)
	}
	foundStatus := false
	for _, e := range prev.Entries {
		if e.Status != "" {
			foundStatus = true
		}
	}
	if !foundStatus {
		t.Fatalf("preview: want an entry with a status, got %+v", prev.Entries)
	}

	// --- Illegal step (dev -> prod) on preview -> 409 ---
	if code := doAuthed(t, "GET", ts.URL+"/v1/promote/preview?from="+devCfg.ID+"&to="+prodCfg.ID, ownerCookie, "", "", nil); code != http.StatusConflict {
		t.Fatalf("illegal-step preview: want 409, got %d", code)
	}

	// --- Developer lacking secret:promote on the target -> 403 on apply ---
	// Grant developer on the SOURCE (dev) env only, so they have secret:read on
	// the source but no secret:promote on the staging target.
	devUserID, devUserPassword, err := srv.auth.CreateUser(ctx, "promo-applier@corp.io")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.authz.Grant(ctx, store.RoleBindingInput{
		SubjectUserID: devUserID, ScopeLevel: "environment", EnvironmentID: &dev.ID, Role: "developer",
	}); err != nil {
		t.Fatal(err)
	}
	// Grant viewer on staging so config resolution/read passes but promote is denied.
	if err := srv.authz.Grant(ctx, store.RoleBindingInput{
		SubjectUserID: devUserID, ScopeLevel: "environment", EnvironmentID: &stg.ID, Role: "viewer",
	}); err != nil {
		t.Fatal(err)
	}
	devUserCookie := login(t, ts.URL, "promo-applier@corp.io", devUserPassword)
	if code := doAuthed(t, "POST", ts.URL+"/v1/promote", devUserCookie, "",
		`{"from_config":"`+devCfg.ID+`","to_config":"`+stgCfg.ID+`","source_version":`+strconv.Itoa(srcVersion)+`,"selections":[{"key":"B","action":"set"}]}`, nil); code != http.StatusForbidden {
		t.Fatalf("developer apply (no secret:promote on target): want 403, got %d", code)
	}

	// --- Illegal step on apply (dev -> prod) -> 409 ---
	if code := doAuthed(t, "POST", ts.URL+"/v1/promote", ownerCookie, "",
		`{"from_config":"`+devCfg.ID+`","to_config":"`+prodCfg.ID+`","source_version":`+strconv.Itoa(srcVersion)+`,"selections":[{"key":"B","action":"set"}]}`, nil); code != http.StatusConflict {
		t.Fatalf("illegal-step apply: want 409, got %d", code)
	}

	// --- Owner apply promoting B -> 200, B appears in staging ---
	var applyResp struct {
		TargetVersion int      `json:"target_version"`
		Applied       []string `json:"applied"`
		Skipped       []string `json:"skipped"`
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/promote", ownerCookie, "",
		`{"from_config":"`+devCfg.ID+`","to_config":"`+stgCfg.ID+`","source_version":`+strconv.Itoa(srcVersion)+`,"selections":[{"key":"B","action":"set"}]}`, &applyResp); code != 200 {
		t.Fatalf("owner apply: want 200, got %d", code)
	}
	if len(applyResp.Applied) != 1 || applyResp.Applied[0] != "B" {
		t.Fatalf("owner apply: want applied [B], got %+v", applyResp.Applied)
	}
	// Verify B was promoted to staging.
	var reveal struct {
		Value string `json:"value"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/configs/"+stgCfg.ID+"/secrets/B", ownerCookie, "", "", &reveal); code != 200 {
		t.Fatalf("staging reveal B: want 200, got %d", code)
	}
	if reveal.Value != "bval" {
		t.Fatalf("staging reveal B: want bval, got %q", reveal.Value)
	}

	// --- Provenance: staging's latest version + config both report the source ---
	var vlist struct {
		Versions []struct {
			Version             int    `json:"version"`
			PromotedFromEnv     string `json:"promoted_from_env"`
			PromotedFromVersion int    `json:"promoted_from_version"`
		} `json:"versions"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/configs/"+stgCfg.ID+"/versions", ownerCookie, "", "", &vlist); code != 200 {
		t.Fatalf("staging versions: want 200, got %d", code)
	}
	if len(vlist.Versions) == 0 {
		t.Fatalf("staging versions: want at least one, got none")
	}
	latest := vlist.Versions[len(vlist.Versions)-1]
	if latest.PromotedFromEnv != dev.Name {
		t.Fatalf("latest version promoted_from_env: want %q, got %q", dev.Name, latest.PromotedFromEnv)
	}
	if latest.PromotedFromVersion != srcVersion {
		t.Fatalf("latest version promoted_from_version: want %d, got %d", srcVersion, latest.PromotedFromVersion)
	}

	// Config-list for the staging env surfaces the same provenance on stgCfg.
	var clist struct {
		Configs []struct {
			ID                  string `json:"id"`
			PromotedFromEnv     string `json:"promoted_from_env"`
			PromotedFromVersion int    `json:"promoted_from_version"`
		} `json:"configs"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/projects/"+p.ID+"/environments/"+stg.ID+"/configs", ownerCookie, "", "", &clist); code != 200 {
		t.Fatalf("staging config list: want 200, got %d", code)
	}
	foundProv := false
	for _, c := range clist.Configs {
		if c.ID == stgCfg.ID {
			foundProv = true
			if c.PromotedFromEnv != dev.Name {
				t.Fatalf("config-list promoted_from_env: want %q, got %q", dev.Name, c.PromotedFromEnv)
			}
			if c.PromotedFromVersion != srcVersion {
				t.Fatalf("config-list promoted_from_version: want %d, got %d", srcVersion, c.PromotedFromVersion)
			}
		}
	}
	if !foundProv {
		t.Fatalf("config-list: staging config %s not found", stgCfg.ID)
	}
	// A must NOT have been promoted (only B selected).
	if code := doAuthed(t, "GET", ts.URL+"/v1/configs/"+stgCfg.ID+"/secrets/A", ownerCookie, "", "", nil); code == 200 {
		t.Fatalf("staging reveal A: want non-200 (A not promoted), got 200")
	}

	// --- Locked key: lock B on staging, then apply selecting B -> 409 ---
	if err := store.NewLockedKeyRepo(srv.st).Lock(ctx, stgCfg.ID, "B", ""); err != nil {
		t.Fatal(err)
	}
	// Re-seed dev B with a new value + bump version so there is something to promote.
	cv2, err := srv.service.SetSecrets(ctx, devCfg.ID, []secrets.SecretChange{
		{Key: "B", Value: []byte("bval2")},
	}, "reseed", "test")
	if err != nil {
		t.Fatal(err)
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/promote", ownerCookie, "",
		`{"from_config":"`+devCfg.ID+`","to_config":"`+stgCfg.ID+`","source_version":`+strconv.Itoa(cv2.Version)+`,"selections":[{"key":"B","action":"set"}]}`, nil); code != http.StatusConflict {
		t.Fatalf("locked-key apply: want 409, got %d", code)
	}

	// --- Create-target with a PROJECT-SCOPED developer -> 200 (Finding 1 regression) ---
	// A project-scoped developer has config:create + secret:promote + secret:read.
	// Promotion with create:true resolves the target env through the scope resolver
	// so the create-target authz sees the ProjectID (not a bare EnvID). Before the
	// fix this 403'd because bindingApplies requires res.ProjectID for a
	// PROJECT-scoped binding to match; after the fix it succeeds and the target is
	// created + secrets promoted.
	createrID, createrPassword, err := srv.auth.CreateUser(ctx, "promo-creator@corp.io")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.authz.Grant(ctx, store.RoleBindingInput{
		SubjectUserID: createrID, ScopeLevel: "project", ProjectID: &p.ID, Role: "developer",
	}); err != nil {
		t.Fatal(err)
	}
	createrCookie := login(t, ts.URL, "promo-creator@corp.io", createrPassword)
	// Seed a source config in staging to promote forward into a NEW prod config.
	// staging root already has B (bval) from the earlier owner apply; promote it
	// forward into a brand-new prod config "default" (does not exist yet).
	stgRootVer, err := srv.service.LatestVersion(ctx, stgCfg.ID)
	if err != nil {
		t.Fatal(err)
	}
	var createResp struct {
		TargetVersion int      `json:"target_version"`
		Applied       []string `json:"applied"`
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/promote", createrCookie, "",
		`{"from_config":"`+stgCfg.ID+`","to_env":"`+prod.ID+`","to_name":"default","create":true,"source_version":`+strconv.Itoa(stgRootVer)+`,"selections":[{"key":"B","action":"set"}]}`, &createResp); code != 200 {
		t.Fatalf("project-scoped developer create-target apply: want 200, got %d", code)
	}
	if len(createResp.Applied) != 1 || createResp.Applied[0] != "B" {
		t.Fatalf("create-target apply: want applied [B], got %+v", createResp.Applied)
	}
}

// TestPromoteCreatePreviewE2E covers the value-revealing create-target preview:
// GET /v1/promote/preview?from=<src>&to_env=<env> where the target env has no
// config yet. Every source key comes back as an "add" with target_exists false.
// A developer lacking config:create on the target env is forbidden.
func TestPromoteCreatePreviewE2E(t *testing.T) {
	ts, srv, ownerEmail, ownerPassword, _ := authStackFull(t)
	ctx := context.Background()
	ownerCookie := login(t, ts.URL, ownerEmail, ownerPassword)

	p, err := srv.service.CreateProject(ctx, "promocreatepreview", "Promote Create Preview")
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
	prod, err := srv.service.CreateEnvironment(ctx, p.ID, "prod", "Prod")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.NewPipelineRepo(srv.st).Set(ctx, p.ID, []string{dev.ID, stg.ID, prod.ID}); err != nil {
		t.Fatal(err)
	}
	// Only dev has a config; staging has NO config yet (create-target scenario).
	devCfg, err := srv.service.CreateConfig(ctx, dev.ID, "root", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.service.SetSecrets(ctx, devCfg.ID, []secrets.SecretChange{
		{Key: "A", Value: []byte("aval")},
		{Key: "B", Value: []byte("bval")},
	}, "seed", "test"); err != nil {
		t.Fatal(err)
	}

	// --- Owner create-preview (dev -> staging env, no config) -> 200, all adds ---
	var prev struct {
		SourceVersion int  `json:"source_version"`
		TargetExists  bool `json:"target_exists"`
		Entries       []struct {
			Key         string `json:"key"`
			Status      string `json:"status"`
			SourceValue string `json:"source_value"`
		} `json:"entries"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/promote/preview?from="+devCfg.ID+"&to_env="+stg.ID, ownerCookie, "", "", &prev); code != 200 {
		t.Fatalf("owner create-preview: want 200, got %d", code)
	}
	if prev.TargetExists {
		t.Fatalf("create-preview: want target_exists=false, got true")
	}
	if len(prev.Entries) != 2 {
		t.Fatalf("create-preview: want 2 entries, got %d (%+v)", len(prev.Entries), prev.Entries)
	}
	got := map[string]string{}
	for _, e := range prev.Entries {
		if e.Status != "add" {
			t.Fatalf("create-preview: key %q want status add, got %q", e.Key, e.Status)
		}
		got[e.Key] = e.SourceValue
	}
	if got["A"] != "aval" || got["B"] != "bval" {
		t.Fatalf("create-preview: want A=aval,B=bval, got %+v", got)
	}

	// --- Developer lacking config:create on the target env -> 403 ---
	// Grant developer on dev (source read) + viewer on staging (no config:create).
	viewerID, viewerPassword, err := srv.auth.CreateUser(ctx, "promo-createpreview-viewer@corp.io")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.authz.Grant(ctx, store.RoleBindingInput{
		SubjectUserID: viewerID, ScopeLevel: "environment", EnvironmentID: &dev.ID, Role: "developer",
	}); err != nil {
		t.Fatal(err)
	}
	if err := srv.authz.Grant(ctx, store.RoleBindingInput{
		SubjectUserID: viewerID, ScopeLevel: "environment", EnvironmentID: &stg.ID, Role: "viewer",
	}); err != nil {
		t.Fatal(err)
	}
	viewerCookie := login(t, ts.URL, "promo-createpreview-viewer@corp.io", viewerPassword)
	if code := doAuthed(t, "GET", ts.URL+"/v1/promote/preview?from="+devCfg.ID+"&to_env="+stg.ID, viewerCookie, "", "", nil); code != http.StatusForbidden {
		t.Fatalf("viewer create-preview (no config:create): want 403, got %d", code)
	}
}
