package api

import (
	"fmt"
	"net/http"
	"testing"
)

// mkEnv creates an environment under pid and returns its id.
func mkEnv(t *testing.T, base, cookie, pid, slug string) string {
	t.Helper()
	var e envResponse
	if code := doAuthed(t, "POST", base+"/v1/projects/"+pid+"/environments", cookie, "",
		fmt.Sprintf(`{"slug":%q,"name":%q}`, slug, slug), &e); code != http.StatusCreated {
		t.Fatalf("create env %s under %s: %d", slug, pid, code)
	}
	return e.ID
}

// mkConfig creates a config under an environment and returns its id.
func mkConfig(t *testing.T, base, cookie, pid, eid, name string) string {
	t.Helper()
	var c configResponse
	if code := doAuthed(t, "POST", base+"/v1/projects/"+pid+"/environments/"+eid+"/configs", cookie, "",
		fmt.Sprintf(`{"name":%q}`, name), &c); code != http.StatusCreated {
		t.Fatalf("create config %s under %s/%s: %d", name, pid, eid, code)
	}
	return c.ID
}

// TestCrossProjectIDORBlocked proves that an actor with a project-scoped admin
// binding on project A cannot reach project B's environment/config by placing
// B's ids under A's pid in the path (cross-project IDOR). The RBAC resource must
// be resolved from the target id's real parent chain, not from the path pid.
func TestCrossProjectIDORBlocked(t *testing.T) {
	ts, srv, adminEmail, adminPass, _ := authStackFull(t)
	admin := login(t, ts.URL, adminEmail, adminPass)

	// Project A (attacker will own this) and project B (victim).
	var projA, projB projectResponse
	if code := doAuthed(t, "POST", ts.URL+"/v1/projects", admin, "", `{"slug":"proj-a","name":"A"}`, &projA); code != http.StatusCreated {
		t.Fatalf("create A: %d", code)
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/projects", admin, "", `{"slug":"proj-b","name":"B"}`, &projB); code != http.StatusCreated {
		t.Fatalf("create B: %d", code)
	}
	eidA := mkEnv(t, ts.URL, admin, projA.ID, "prod")
	_ = mkConfig(t, ts.URL, admin, projA.ID, eidA, "root")
	eidB := mkEnv(t, ts.URL, admin, projB.ID, "prod")
	cidB := mkConfig(t, ts.URL, admin, projB.ID, eidB, "root")
	_ = cidB

	// Low-privilege attacker: create the user, then grant a PROJECT-scoped admin
	// binding on project A ONLY (the project-members route is safe).
	var created struct{ ID, Password string }
	if code := doAuthed(t, "POST", ts.URL+"/v1/users", admin, "", `{"email":"attacker@corp.io"}`, &created); code != 200 {
		t.Fatalf("create attacker: %d", code)
	}
	if code := doAuthed(t, "PUT", ts.URL+"/v1/projects/"+projA.ID+"/members/"+created.ID, admin, "",
		`{"role":"admin"}`, nil); code != http.StatusNoContent {
		t.Fatalf("grant project-admin on A: %d", code)
	}
	attacker := login(t, ts.URL, "attacker@corp.io", created.Password)

	// --- Cross-project attempts: A's pid in the path, B's ids as the target. ---
	// Each MUST be blocked (403 forbidden, or 404 if resolve-then-deny/not visible).

	if code := doAuthed(t, "GET", ts.URL+"/v1/projects/"+projA.ID+"/environments/"+eidB, attacker, "", "", nil); code == http.StatusOK {
		t.Errorf("IDOR: attacker GET env B succeeded (%d), want blocked", code)
	}

	if code := doAuthed(t, "POST", ts.URL+"/v1/projects/"+projA.ID+"/environments/"+eidB+"/configs", attacker, "",
		`{"name":"x"}`, nil); code == http.StatusCreated {
		t.Errorf("IDOR: attacker created config under env B (%d), want blocked", code)
	}

	if code := doAuthed(t, "GET", ts.URL+"/v1/projects/"+projA.ID+"/environments/"+eidB+"/configs", attacker, "", "", nil); code == http.StatusOK {
		t.Errorf("IDOR: attacker listed configs of env B (%d), want blocked", code)
	}

	if code := doAuthed(t, "PUT", ts.URL+"/v1/projects/"+projA.ID+"/environments/"+eidB+"/members/"+created.ID, attacker, "",
		`{"role":"admin"}`, nil); code == http.StatusNoContent {
		t.Errorf("IDOR: attacker granted membership on env B (%d), want blocked", code)
	}

	// Destructive op runs LAST so the checks above exercise a live env B.
	if code := doAuthed(t, "DELETE", ts.URL+"/v1/projects/"+projA.ID+"/environments/"+eidB+"?destroy=true", attacker, "", "", nil); code == http.StatusNoContent {
		t.Errorf("IDOR: attacker DESTROY env B succeeded (%d), want blocked", code)
	}
	// Verify env B still exists afterward (admin can still see it).
	if code := doAuthed(t, "GET", ts.URL+"/v1/projects/"+projB.ID+"/environments/"+eidB, admin, "", "", nil); code != http.StatusOK {
		t.Errorf("env B was destroyed via IDOR (admin GET -> %d)", code)
	}

	// --- Regression guard: attacker CAN still do the same ops within project A. ---
	if code := doAuthed(t, "GET", ts.URL+"/v1/projects/"+projA.ID+"/environments/"+eidA, attacker, "", "", nil); code != http.StatusOK {
		t.Errorf("legit same-project GET env A blocked (%d), want 200", code)
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/projects/"+projA.ID+"/environments/"+eidA+"/configs", attacker, "",
		`{"name":"legit"}`, nil); code != http.StatusCreated {
		t.Errorf("legit same-project config create under A blocked (%d), want 201", code)
	}

	_ = srv
}
