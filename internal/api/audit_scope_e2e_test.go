package api

import (
	"fmt"
	"strings"
	"testing"
)

type auditEventsResp struct {
	Events []struct {
		Seq      int64  `json:"seq"`
		Action   string `json:"action"`
		Resource string `json:"resource"`
	} `json:"events"`
}

// seedProjectWithSecret creates a project/env/config and writes one secret,
// producing audit events attributable to that project.
func seedProjectWithSecret(t *testing.T, base, cookie, slug, key string) (pid, cid string) {
	t.Helper()
	var proj struct{ ID string }
	if code := doAuthed(t, "POST", base+"/v1/projects", cookie, "", `{"slug":"`+slug+`","name":"`+slug+`"}`, &proj); code != 201 {
		t.Fatalf("create project %s: %d", slug, code)
	}
	var env struct{ ID string }
	if code := doAuthed(t, "POST", base+"/v1/projects/"+proj.ID+"/environments", cookie, "",
		`{"slug":"dev","name":"Dev"}`, &env); code != 201 {
		t.Fatalf("create env: %d", code)
	}
	var cfg struct{ ID string }
	if code := doAuthed(t, "POST", base+"/v1/projects/"+proj.ID+"/environments/"+env.ID+"/configs", cookie, "",
		`{"name":"default"}`, &cfg); code != 201 {
		t.Fatalf("create config: %d", code)
	}
	if code := doAuthed(t, "PUT", base+"/v1/configs/"+cfg.ID+"/secrets", cookie, "",
		`{"changes":[{"key":"`+key+`","value":"v"}]}`, nil); code != 200 {
		t.Fatalf("seed secret: %d", code)
	}
	return proj.ID, cfg.ID
}

// The point of the feature: a team lead reviews their own project's trail and
// sees nobody else's. Before this, every audit endpoint authorized against the
// instance scope, so the only options were everything or nothing — and audit
// rows carry resource paths and key names, so "everything" leaked the shape of
// every other team's secrets.
func TestScopedAuditReadShowsOnlyReadableProjectsE2E(t *testing.T) {
	ts, _, email, password, _ := authStackFull(t)
	owner := login(t, ts.URL, email, password)

	minePID, _ := seedProjectWithSecret(t, ts.URL, owner, "mine", "MINE_KEY")
	_, _ = seedProjectWithSecret(t, ts.URL, owner, "theirs", "THEIRS_KEY")

	var lead struct{ ID, Password string }
	if code := doAuthed(t, "POST", ts.URL+"/v1/users", owner, "", `{"email":"lead@corp.io"}`, &lead); code != 200 {
		t.Fatalf("create user: %d", code)
	}
	if code := doAuthed(t, "PUT", ts.URL+"/v1/projects/"+minePID+"/members/"+lead.ID, owner, "", `{"role":"admin"}`, nil); code != 204 {
		t.Fatalf("grant project admin: %d", code)
	}
	session := login(t, ts.URL, "lead@corp.io", lead.Password)

	var got auditEventsResp
	if code := doAuthed(t, "GET", ts.URL+"/v1/audit/events?limit=200", session, "", "", &got); code != 200 {
		t.Fatalf("scoped audit read: got %d, want 200", code)
	}
	if len(got.Events) == 0 {
		t.Fatal("scoped reader saw no events at all")
	}
	for _, e := range got.Events {
		if strings.Contains(e.Resource, "THEIRS") {
			t.Fatalf("scoped reader saw another project's event: %+v", e)
		}
	}
	// They must see their OWN project's secret write.
	sawMine := false
	for _, e := range got.Events {
		if e.Action == "secret.write" {
			sawMine = true
		}
	}
	if !sawMine {
		t.Fatal("scoped reader did not see their own project's secret.write")
	}

	// The instance-wide view is unchanged and strictly larger.
	var all auditEventsResp
	if code := doAuthed(t, "GET", ts.URL+"/v1/audit/events?limit=200", owner, "", "", &all); code != 200 {
		t.Fatalf("instance audit read: %d", code)
	}
	if len(all.Events) <= len(got.Events) {
		t.Fatalf("instance view (%d) must be larger than the scoped view (%d)", len(all.Events), len(got.Events))
	}
}

// Instance-level events (logins, user management) belong to no project and must
// never appear in a scoped view — including every event written BEFORE the
// migration, which is why NULL is the fail-closed reading.
func TestScopedAuditReadHidesInstanceEventsE2E(t *testing.T) {
	ts, _, email, password, _ := authStackFull(t)
	owner := login(t, ts.URL, email, password)
	pid, _ := seedProjectWithSecret(t, ts.URL, owner, "mine", "K")

	var lead struct{ ID, Password string }
	if code := doAuthed(t, "POST", ts.URL+"/v1/users", owner, "", `{"email":"lead@corp.io"}`, &lead); code != 200 {
		t.Fatalf("create user: %d", code)
	}
	if code := doAuthed(t, "PUT", ts.URL+"/v1/projects/"+pid+"/members/"+lead.ID, owner, "", `{"role":"admin"}`, nil); code != 204 {
		t.Fatalf("grant: %d", code)
	}
	session := login(t, ts.URL, "lead@corp.io", lead.Password)

	var got auditEventsResp
	if code := doAuthed(t, "GET", ts.URL+"/v1/audit/events?limit=200", session, "", "", &got); code != 200 {
		t.Fatalf("scoped read: %d", code)
	}
	for _, e := range got.Events {
		switch e.Action {
		case "auth.login", "user.create", "sys.init", "sys.unseal":
			t.Fatalf("instance-level event leaked into a scoped view: %+v", e)
		}
	}
}

// A user with no audit:read anywhere is denied outright — not shown an empty
// list, which would read as "nothing happened".
func TestScopedAuditReadDeniesUnboundUserE2E(t *testing.T) {
	ts, _, email, password, _ := authStackFull(t)
	owner := login(t, ts.URL, email, password)
	seedProjectWithSecret(t, ts.URL, owner, "mine", "K")

	var nobody struct{ ID, Password string }
	if code := doAuthed(t, "POST", ts.URL+"/v1/users", owner, "", `{"email":"nobody@corp.io"}`, &nobody); code != 200 {
		t.Fatalf("create user: %d", code)
	}
	session := login(t, ts.URL, "nobody@corp.io", nobody.Password)

	for _, path := range []string{"/v1/audit/events", "/v1/audit/histogram", "/v1/audit/export"} {
		if code := doAuthed(t, "GET", ts.URL+path, session, "", "", nil); code != 403 {
			t.Fatalf("%s for an unbound user: got %d, want 403", path, code)
		}
	}
}

// verify covers the WHOLE chain, so a subset cannot be verified. A scoped
// reader must be denied rather than shown a verification meaning something
// other than it appears to.
func TestAuditVerifyStaysInstanceOnlyE2E(t *testing.T) {
	ts, _, email, password, _ := authStackFull(t)
	owner := login(t, ts.URL, email, password)
	pid, _ := seedProjectWithSecret(t, ts.URL, owner, "mine", "K")

	var lead struct{ ID, Password string }
	if code := doAuthed(t, "POST", ts.URL+"/v1/users", owner, "", `{"email":"lead@corp.io"}`, &lead); code != 200 {
		t.Fatalf("create user: %d", code)
	}
	if code := doAuthed(t, "PUT", ts.URL+"/v1/projects/"+pid+"/members/"+lead.ID, owner, "", `{"role":"admin"}`, nil); code != 204 {
		t.Fatalf("grant: %d", code)
	}
	session := login(t, ts.URL, "lead@corp.io", lead.Password)

	if code := doAuthed(t, "GET", ts.URL+"/v1/audit/verify", session, "", "", nil); code != 403 {
		t.Fatalf("project admin calling verify: got %d, want 403", code)
	}
	// ...and the chain still verifies for an instance reader with the new
	// column populated, proving project_id is outside the hash.
	var v struct {
		Valid bool  `json:"valid"`
		Count int64 `json:"count"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/audit/verify", owner, "", "", &v); code != 200 {
		t.Fatalf("instance verify: %d", code)
	}
	if !v.Valid {
		t.Fatal("chain failed to verify with project_id populated — the column must not be hashed")
	}
}

// The restriction is applied in SQL, so keyset pagination across a scoped view
// neither skips nor repeats. A post-filter would silently truncate the trail.
func TestScopedAuditPaginationIsCompleteE2E(t *testing.T) {
	ts, _, email, password, _ := authStackFull(t)
	owner := login(t, ts.URL, email, password)

	minePID, mineCID := seedProjectWithSecret(t, ts.URL, owner, "mine", "K0")
	_, theirsCID := seedProjectWithSecret(t, ts.URL, owner, "theirs", "T0")

	// Interleave writes across both projects so a naive post-filter would drop
	// whole pages of the scoped project's events.
	for i := 1; i <= 12; i++ {
		body := fmt.Sprintf(`{"changes":[{"key":"K%d","value":"v"}]}`, i)
		if code := doAuthed(t, "PUT", ts.URL+"/v1/configs/"+mineCID+"/secrets", owner, "", body, nil); code != 200 {
			t.Fatalf("mine write %d: %d", i, code)
		}
		tbody := fmt.Sprintf(`{"changes":[{"key":"T%d","value":"v"}]}`, i)
		if code := doAuthed(t, "PUT", ts.URL+"/v1/configs/"+theirsCID+"/secrets", owner, "", tbody, nil); code != 200 {
			t.Fatalf("theirs write %d: %d", i, code)
		}
	}

	var lead struct{ ID, Password string }
	if code := doAuthed(t, "POST", ts.URL+"/v1/users", owner, "", `{"email":"lead@corp.io"}`, &lead); code != 200 {
		t.Fatalf("create user: %d", code)
	}
	if code := doAuthed(t, "PUT", ts.URL+"/v1/projects/"+minePID+"/members/"+lead.ID, owner, "", `{"role":"admin"}`, nil); code != 204 {
		t.Fatalf("grant: %d", code)
	}
	session := login(t, ts.URL, "lead@corp.io", lead.Password)

	// Walk every page at a small limit.
	seen := map[int64]bool{}
	writes := 0
	var cursor int64
	for page := 0; page < 30; page++ {
		url := ts.URL + "/v1/audit/events?limit=3"
		if cursor > 0 {
			url += "&cursor=" + fmt.Sprint(cursor)
		}
		var resp struct {
			Events []struct {
				Seq      int64  `json:"seq"`
				Action   string `json:"action"`
				Resource string `json:"resource"`
			} `json:"events"`
			NextCursor *int64 `json:"next_cursor"`
		}
		if code := doAuthed(t, "GET", url, session, "", "", &resp); code != 200 {
			t.Fatalf("page %d: %d", page, code)
		}
		for _, e := range resp.Events {
			if seen[e.Seq] {
				t.Fatalf("duplicate seq %d across pages", e.Seq)
			}
			seen[e.Seq] = true
			if strings.Contains(e.Resource, "theirs") {
				t.Fatalf("foreign event in a scoped page: %+v", e)
			}
			if e.Action == "secret.write" {
				writes++
			}
		}
		if resp.NextCursor == nil || *resp.NextCursor <= 0 {
			break
		}
		cursor = *resp.NextCursor
	}
	// 1 seed + 12 interleaved writes on the readable project.
	if writes != 13 {
		t.Fatalf("saw %d secret.write events across all pages, want 13 — paging dropped rows", writes)
	}
}

// Destroying a project records its audit event AFTER the row is gone, so the
// scope column must not be a foreign key — an FK made destroy 500. The event is
// still written, still attributed, and simply matches no scoped filter (a
// destroyed project is readable by nobody).
func TestDestroyingAProjectStillAuditsE2E(t *testing.T) {
	ts, _, email, password, _ := authStackFull(t)
	owner := login(t, ts.URL, email, password)
	pid, _ := seedProjectWithSecret(t, ts.URL, owner, "doomed", "K")

	if code := doAuthed(t, "DELETE", ts.URL+"/v1/projects/"+pid, owner, "", "", nil); code != 204 {
		t.Fatalf("soft delete: %d", code)
	}
	if code := doAuthed(t, "DELETE", ts.URL+"/v1/projects/"+pid+"?destroy=true", owner, "", "", nil); code != 204 {
		t.Fatalf("destroy: got %d, want 204", code)
	}

	// The destroy is in the ledger, and the chain still verifies.
	var got auditEventsResp
	if code := doAuthed(t, "GET", ts.URL+"/v1/audit/events?limit=200", owner, "", "", &got); code != 200 {
		t.Fatalf("read audit: %d", code)
	}
	// The handler records project.delete for both soft-delete and destroy,
	// distinguished by its detail; two must be present.
	deletes := 0
	for _, e := range got.Events {
		if e.Action == "project.delete" {
			deletes++
		}
	}
	if deletes < 2 {
		t.Fatalf("saw %d project.delete events, want the soft-delete and the destroy", deletes)
	}
	var v struct {
		Valid bool `json:"valid"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/audit/verify", owner, "", "", &v); code != 200 || !v.Valid {
		t.Fatalf("chain verify after destroy: code=%d valid=%v", code, v.Valid)
	}
}
