package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/steveokay/janus-secrets/internal/secrets"
)

// makeUserNoGrant creates a user via POST /v1/users (admin) and returns its id
// and one-time password WITHOUT granting any role (deny-by-default: the user
// sees nothing until a scoped binding is added).
func makeUserNoGrant(t *testing.T, ts, adminCookie, email string) (string, string) {
	t.Helper()
	var created struct{ ID, Password string }
	if code := doAuthed(t, "POST", ts+"/v1/users", adminCookie, "",
		`{"email":"`+email+`"}`, &created); code != 200 {
		t.Fatalf("create user %s: %d", email, code)
	}
	return created.ID, created.Password
}

// grantProject grants role to uid scoped to a single project.
func grantProject(t *testing.T, ts, adminCookie, projectID, uid, role string) {
	t.Helper()
	if code := doAuthed(t, "PUT", ts+"/v1/projects/"+projectID+"/members/"+uid, adminCookie, "",
		`{"role":"`+role+`"}`, nil); code != http.StatusNoContent {
		t.Fatalf("grant %s on project %s to %s: %d", role, projectID, uid, code)
	}
}

// seedProjectConfig creates project→env→config via the wired service and writes
// the given keys into a config version, returning the project id + config id.
func seedProjectConfig(t *testing.T, srv *Server, slug string, keys ...string) (projectID, configID string) {
	t.Helper()
	ctx := context.Background()
	p, err := srv.service.CreateProject(ctx, slug, slug)
	if err != nil {
		t.Fatal(err)
	}
	e, err := srv.service.CreateEnvironment(ctx, p.ID, "prod", "Prod")
	if err != nil {
		t.Fatal(err)
	}
	c, err := srv.service.CreateConfig(ctx, e.ID, "root", nil)
	if err != nil {
		t.Fatal(err)
	}
	changes := make([]secrets.SecretChange, 0, len(keys))
	for _, k := range keys {
		changes = append(changes, secrets.SecretChange{Key: k, Value: []byte("sentinel-" + k)})
	}
	if _, err := srv.service.SetSecrets(ctx, c.ID, changes, "seed", "admin"); err != nil {
		t.Fatal(err)
	}
	return p.ID, c.ID
}

// auditCount returns the current audit chain event count via /v1/audit/verify.
func auditCount(t *testing.T, ts, cookie string) int64 {
	t.Helper()
	var v struct {
		Valid bool  `json:"valid"`
		Count int64 `json:"count"`
	}
	if code := doAuthed(t, "GET", ts+"/v1/audit/verify", cookie, "", "", &v); code != 200 {
		t.Fatalf("audit verify: %d", code)
	}
	return v.Count
}

func TestSearchKeysValidationE2E(t *testing.T) {
	ts, _, email, password, _ := authStackFull(t)
	cookie := login(t, ts.URL, email, password)

	var env errEnvelope
	// q shorter than 2 chars → 400.
	for _, q := range []string{"", "a", "%20"} {
		if code := doAuthed(t, "GET", ts.URL+"/v1/search/keys?q="+q, cookie, "", "", &env); code != 400 {
			t.Fatalf("q=%q: want 400, got %d", q, code)
		}
	}
	// Unauthenticated → 401.
	if code := doAuthed(t, "GET", ts.URL+"/v1/search/keys?q=abc", "", "", "", nil); code != 401 {
		t.Fatalf("unauth search: want 401, got %d", code)
	}
}

// TestSearchKeysDenyByDefaultE2E is the security core: two users scoped to
// different projects each see key hits ONLY in configs they can read.
func TestSearchKeysDenyByDefaultE2E(t *testing.T) {
	ts, srv, email, password, _ := authStackFull(t)
	admin := login(t, ts.URL, email, password)

	// The same key name lives in a config under project A and under project B.
	projA, cfgA := seedProjectConfig(t, srv, "alpha", "SHARED_KEY")
	projB, cfgB := seedProjectConfig(t, srv, "bravo", "SHARED_KEY")

	uidA, passA := makeUserNoGrant(t, ts.URL, admin, "usera@corp.io")
	uidB, passB := makeUserNoGrant(t, ts.URL, admin, "userb@corp.io")
	grantProject(t, ts.URL, admin, projA, uidA, "viewer")
	grantProject(t, ts.URL, admin, projB, uidB, "viewer")
	userA := login(t, ts.URL, "usera@corp.io", passA)
	userB := login(t, ts.URL, "userb@corp.io", passB)

	type result struct {
		Key      string `json:"key"`
		ConfigID string `json:"config_id"`
	}
	type resp struct {
		Results   []result `json:"results"`
		Truncated bool     `json:"truncated"`
	}

	// User A: sees SHARED_KEY in config A, NOT in config B.
	var ra resp
	if code := doAuthed(t, "GET", ts.URL+"/v1/search/keys?q=SHARED", userA, "", "", &ra); code != 200 {
		t.Fatalf("userA search: %d", code)
	}
	sawA, sawBfromA := false, false
	for _, r := range ra.Results {
		if r.ConfigID == cfgA {
			sawA = true
		}
		if r.ConfigID == cfgB {
			sawBfromA = true
		}
	}
	if !sawA {
		t.Fatalf("userA did not see its own config A key: %+v", ra.Results)
	}
	if sawBfromA {
		t.Fatalf("DENY-BY-DEFAULT VIOLATION: userA saw project B's key: %+v", ra.Results)
	}

	// User B: sees SHARED_KEY in config B, NOT in config A.
	var rb resp
	if code := doAuthed(t, "GET", ts.URL+"/v1/search/keys?q=SHARED", userB, "", "", &rb); code != 200 {
		t.Fatalf("userB search: %d", code)
	}
	sawB, sawAfromB := false, false
	for _, r := range rb.Results {
		if r.ConfigID == cfgB {
			sawB = true
		}
		if r.ConfigID == cfgA {
			sawAfromB = true
		}
	}
	if !sawB {
		t.Fatalf("userB did not see its own config B key: %+v", rb.Results)
	}
	if sawAfromB {
		t.Fatalf("DENY-BY-DEFAULT VIOLATION: userB saw project A's key: %+v", rb.Results)
	}

	// Admin (instance owner) sees both.
	var radm resp
	if code := doAuthed(t, "GET", ts.URL+"/v1/search/keys?q=SHARED", admin, "", "", &radm); code != 200 {
		t.Fatalf("admin search: %d", code)
	}
	adminSawA, adminSawB := false, false
	for _, r := range radm.Results {
		if r.ConfigID == cfgA {
			adminSawA = true
		}
		if r.ConfigID == cfgB {
			adminSawB = true
		}
	}
	if !adminSawA || !adminSawB {
		t.Fatalf("admin should see both configs: %+v", radm.Results)
	}
}

// TestSearchKeysEnrichmentAndNoAuditE2E asserts the response carries structural
// metadata (no value) and that a search writes NO audit event.
func TestSearchKeysEnrichmentAndNoAuditE2E(t *testing.T) {
	ts, srv, email, password, _ := authStackFull(t)
	admin := login(t, ts.URL, email, password)
	_, cfg := seedProjectConfig(t, srv, "enrich", "STRIPE_KEY")

	before := auditCount(t, ts.URL, admin)

	var out struct {
		Results []struct {
			Key             string `json:"key"`
			ProjectID       string `json:"project_id"`
			ProjectName     string `json:"project_name"`
			ProjectSlug     string `json:"project_slug"`
			EnvironmentID   string `json:"environment_id"`
			EnvironmentSlug string `json:"environment_slug"`
			ConfigID        string `json:"config_id"`
			ConfigName      string `json:"config_name"`
		} `json:"results"`
		Truncated bool `json:"truncated"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/search/keys?q=STRIPE", admin, "", "", &out); code != 200 {
		t.Fatalf("search: %d", code)
	}
	var hit *struct {
		Key             string `json:"key"`
		ProjectID       string `json:"project_id"`
		ProjectName     string `json:"project_name"`
		ProjectSlug     string `json:"project_slug"`
		EnvironmentID   string `json:"environment_id"`
		EnvironmentSlug string `json:"environment_slug"`
		ConfigID        string `json:"config_id"`
		ConfigName      string `json:"config_name"`
	}
	for i := range out.Results {
		if out.Results[i].ConfigID == cfg {
			hit = &out.Results[i]
			break
		}
	}
	if hit == nil {
		t.Fatalf("STRIPE_KEY not found: %+v", out.Results)
	}
	if hit.Key != "STRIPE_KEY" || hit.ProjectSlug != "enrich" || hit.EnvironmentSlug != "prod" || hit.ConfigName != "root" {
		t.Fatalf("enrichment wrong: %+v", *hit)
	}
	if hit.ProjectID == "" || hit.EnvironmentID == "" {
		t.Fatalf("enrichment ids empty: %+v", *hit)
	}

	// A search must NOT write an audit event (metadata list view). The only
	// audit growth allowed between the two counts is the /v1/audit/verify reads,
	// which are themselves not audited, so the count must be unchanged.
	after := auditCount(t, ts.URL, admin)
	if after != before {
		t.Fatalf("search wrote an audit event: count %d -> %d", before, after)
	}
}
