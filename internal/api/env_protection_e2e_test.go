package api

import (
	"context"
	"fmt"
	"testing"
)

// envProtectionFixture builds project → env → config with one committed version,
// returning the ids and an owner session.
func envProtectionFixture(t *testing.T) (base, owner, pid, eid, cid string) {
	t.Helper()
	ts, srv, email, password, _ := authStackFull(t)
	owner = login(t, ts.URL, email, password)
	ctx := context.Background()
	proj, err := srv.service.CreateProject(ctx, "app", "App")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	env, err := srv.service.CreateEnvironment(ctx, proj.ID, "prod", "Production")
	if err != nil {
		t.Fatalf("create env: %v", err)
	}
	cfg, err := srv.service.CreateConfig(ctx, env.ID, "default", nil)
	if err != nil {
		t.Fatalf("create config: %v", err)
	}
	return ts.URL, owner, proj.ID, env.ID, cfg.ID
}

// The gap this closes: require_approval was per-config and defaulted to false,
// so a config created in production started UNPROTECTED. Protecting the
// ENVIRONMENT must cover every config in it, including ones created later.
func TestEnvironmentProtectionGovernsWritesE2E(t *testing.T) {
	base, owner, pid, eid, cid := envProtectionFixture(t)

	// Baseline: unprotected, so a save COMMITS (200 with a version).
	body := `{"changes":[{"key":"A","value":"1"}]}`
	if code := doAuthed(t, "PUT", base+"/v1/configs/"+cid+"/secrets", owner, "", body, nil); code != 200 {
		t.Fatalf("unprotected save: got %d, want 200", code)
	}

	// Protect the ENVIRONMENT (not the config).
	if code := doAuthed(t, "PUT", base+"/v1/projects/"+pid+"/environments/"+eid+"/require-approval",
		owner, "", `{"enabled":true}`, nil); code != 200 {
		t.Fatalf("protect env: %d", code)
	}

	// The very same save now files an approval request instead (202).
	if code := doAuthed(t, "PUT", base+"/v1/configs/"+cid+"/secrets", owner, "",
		`{"changes":[{"key":"A","value":"2"}]}`, nil); code != 202 {
		t.Fatalf("save under env protection: got %d, want 202", code)
	}

	// A config created AFTER the environment was protected inherits it — the
	// whole point, since the old per-config flag defaulted to false.
	var fresh struct {
		ID                       string `json:"id"`
		RequireApproval          bool   `json:"require_approval"`
		EnvRequireApproval       bool   `json:"environment_require_approval"`
		EffectiveRequireApproval bool   `json:"effective_require_approval"`
	}
	if code := doAuthed(t, "POST", base+"/v1/projects/"+pid+"/environments/"+eid+"/configs",
		owner, "", `{"name":"latecomer"}`, &fresh); code != 201 {
		t.Fatalf("create config: %d", code)
	}
	if fresh.RequireApproval {
		t.Fatal("the config's OWN flag should still be false")
	}
	if !fresh.EnvRequireApproval || !fresh.EffectiveRequireApproval {
		t.Fatalf("new config not covered by env protection: %+v", fresh)
	}
	if code := doAuthed(t, "PUT", base+"/v1/configs/"+fresh.ID+"/secrets", owner, "",
		`{"changes":[{"key":"B","value":"1"}]}`, nil); code != 202 {
		t.Fatalf("save on a config created under env protection: got %d, want 202", code)
	}

	// Turning the environment off again restores direct commits, and the
	// config's own flag was never touched.
	if code := doAuthed(t, "PUT", base+"/v1/projects/"+pid+"/environments/"+eid+"/require-approval",
		owner, "", `{"enabled":false}`, nil); code != 200 {
		t.Fatalf("unprotect env: %d", code)
	}
	if code := doAuthed(t, "PUT", base+"/v1/configs/"+cid+"/secrets", owner, "",
		`{"changes":[{"key":"A","value":"3"}]}`, nil); code != 200 {
		t.Fatalf("after unprotecting: got %d, want 200", code)
	}
}

// A config may ADD protection its environment does not require, but must never
// be able to REMOVE protection the environment does — that is the union rule,
// and it is what stops "production is four-eyes" being switched off one config
// at a time.
func TestConfigCannotWeakenEnvironmentProtectionE2E(t *testing.T) {
	base, owner, pid, eid, cid := envProtectionFixture(t)

	if code := doAuthed(t, "PUT", base+"/v1/projects/"+pid+"/environments/"+eid+"/require-approval",
		owner, "", `{"enabled":true}`, nil); code != 200 {
		t.Fatalf("protect env: %d", code)
	}
	// Explicitly set the CONFIG flag to false — the strongest attempt at opting
	// out from below.
	if code := doAuthed(t, "PUT", base+"/v1/configs/"+cid+"/require-approval",
		owner, "", `{"enabled":false}`, nil); code != 200 {
		t.Fatalf("set config flag false: %d", code)
	}
	if code := doAuthed(t, "PUT", base+"/v1/configs/"+cid+"/secrets", owner, "",
		`{"changes":[{"key":"A","value":"x"}]}`, nil); code != 202 {
		t.Fatalf("config opted out of env protection: got %d, want 202", code)
	}

	// And the reverse composes: config-only protection still works with the
	// environment unprotected.
	if code := doAuthed(t, "PUT", base+"/v1/projects/"+pid+"/environments/"+eid+"/require-approval",
		owner, "", `{"enabled":false}`, nil); code != 200 {
		t.Fatalf("unprotect env: %d", code)
	}
	if code := doAuthed(t, "PUT", base+"/v1/configs/"+cid+"/require-approval",
		owner, "", `{"enabled":true}`, nil); code != 200 {
		t.Fatalf("protect config: %d", code)
	}
	if code := doAuthed(t, "PUT", base+"/v1/configs/"+cid+"/secrets", owner, "",
		`{"changes":[{"key":"A","value":"y"}]}`, nil); code != 202 {
		t.Fatalf("config-only protection: got %d, want 202", code)
	}
}

// Every door must be locked, not just the front one. Rollback and promote-apply
// both mutate secrets and both route through the same protection check — a
// regression here was a real HIGH finding once (PR #148).
func TestEnvironmentProtectionCoversRollbackE2E(t *testing.T) {
	base, owner, pid, eid, cid := envProtectionFixture(t)

	for i, v := range []string{"1", "2"} {
		body := fmt.Sprintf(`{"changes":[{"key":"A","value":%q}]}`, v)
		if code := doAuthed(t, "PUT", base+"/v1/configs/"+cid+"/secrets", owner, "", body, nil); code != 200 {
			t.Fatalf("seed save %d: %d", i, code)
		}
	}
	if code := doAuthed(t, "PUT", base+"/v1/projects/"+pid+"/environments/"+eid+"/require-approval",
		owner, "", `{"enabled":true}`, nil); code != 200 {
		t.Fatalf("protect env: %d", code)
	}
	// A rollback to v1 must become a request, not a commit.
	if code := doAuthed(t, "POST", base+"/v1/configs/"+cid+"/rollback", owner, "", `{"target_version":1}`, nil); code != 202 {
		t.Fatalf("rollback under env protection: got %d, want 202", code)
	}
}

// The toggle is a security control, so it needs the same authority as the
// per-config one and must not be reachable by a developer.
func TestEnvProtectionToggleRequiresPromotionManageE2E(t *testing.T) {
	base, owner, pid, eid, _ := envProtectionFixture(t)

	var dev struct{ ID, Password string }
	if code := doAuthed(t, "POST", base+"/v1/users", owner, "", `{"email":"dev@corp.io"}`, &dev); code != 200 {
		t.Fatalf("create user: %d", code)
	}
	if code := doAuthed(t, "PUT", base+"/v1/projects/"+pid+"/members/"+dev.ID, owner, "", `{"role":"developer"}`, nil); code != 204 {
		t.Fatalf("grant developer: %d", code)
	}
	devSession := login(t, base, "dev@corp.io", dev.Password)
	if code := doAuthed(t, "PUT", base+"/v1/projects/"+pid+"/environments/"+eid+"/require-approval",
		devSession, "", `{"enabled":false}`, nil); code != 403 {
		t.Fatalf("developer toggling env protection: got %d, want 403", code)
	}
}
