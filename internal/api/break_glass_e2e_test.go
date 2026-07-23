package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/steveokay/janus-secrets/internal/store"
)

// grantView mirrors the value-safe activation/list response.
type grantView struct {
	ID           string    `json:"id"`
	UserID       string    `json:"user_id"`
	ScopeLevel   string    `json:"scope_level"`
	ElevatedRole string    `json:"elevated_role"`
	Reason       string    `json:"reason"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// setupBreakGlass boots the stack and returns the server, its base URL, the
// owner cookie, and a project the non-owner user will hold a role on (with a
// child env + config so the scope chain resolves).
func setupBreakGlass(t *testing.T) (*Server, string, string, *store.Project) {
	t.Helper()
	ts, srv, ownerEmail, ownerPw, _ := authStackFull(t)
	ctx := context.Background()
	ownerCookie := login(t, ts.URL, ownerEmail, ownerPw)

	p, err := srv.service.CreateProject(ctx, "bg", "BreakGlass")
	if err != nil {
		t.Fatal(err)
	}
	e, err := srv.service.CreateEnvironment(ctx, p.ID, "prod", "Prod")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.service.CreateConfig(ctx, e.ID, "root", nil); err != nil {
		t.Fatal(err)
	}
	return srv, ts.URL, ownerCookie, p
}

func TestBreakGlassActivateElevateRevokeE2E(t *testing.T) {
	srv, base, _, proj := setupBreakGlass(t)
	ctx := context.Background()

	// A developer on project P: can write secrets but cannot manage members.
	uid, pw, err := srv.auth.CreateUser(ctx, "dev@corp.io")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.authz.Grant(ctx, store.RoleBindingInput{
		SubjectUserID: uid, ScopeLevel: "project", ProjectID: &proj.ID, Role: "developer",
	}); err != nil {
		t.Fatal(err)
	}
	cookie := login(t, base, "dev@corp.io", pw)

	// Baseline: developer cannot manage members on the project (403).
	if code := doAuthed(t, "PUT", base+"/v1/projects/"+proj.ID+"/members/"+uid, cookie, "",
		`{"role":"viewer"}`, nil); code != http.StatusForbidden {
		t.Fatalf("developer should not manage members pre-elevation: %d", code)
	}

	// Activate break-glass to admin on project P.
	var g grantView
	body := fmt.Sprintf(`{"scope_level":"project","project_id":%q,"role":"admin","reason":"prod incident 42","ttl":"20m"}`, proj.ID)
	if code := doAuthed(t, "POST", base+"/v1/break-glass", cookie, "", body, &g); code != http.StatusCreated {
		t.Fatalf("activate: want 201, got %d", code)
	}
	if g.ID == "" || g.ElevatedRole != "admin" || g.Reason != "prod incident 42" {
		t.Fatalf("grant view unexpected: %+v", g)
	}

	// Now the developer, elevated to admin, CAN manage members.
	if code := doAuthed(t, "PUT", base+"/v1/projects/"+proj.ID+"/members/"+uid, cookie, "",
		`{"role":"viewer"}`, nil); code == http.StatusForbidden {
		t.Fatalf("elevated user should manage members: got 403")
	}

	// Revoke → elevation gone.
	if code := doAuthed(t, "DELETE", base+"/v1/break-glass/"+g.ID, cookie, "", "", nil); code != http.StatusNoContent {
		t.Fatalf("revoke: want 204, got %d", code)
	}
	if code := doAuthed(t, "PUT", base+"/v1/projects/"+proj.ID+"/members/"+uid, cookie, "",
		`{"role":"viewer"}`, nil); code != http.StatusForbidden {
		t.Fatalf("after revoke developer must not manage members: %d", code)
	}
	// Double revoke → 409 (nothing live).
	if code := doAuthed(t, "DELETE", base+"/v1/break-glass/"+g.ID, cookie, "", "", nil); code != http.StatusConflict {
		t.Fatalf("double revoke: want 409, got %d", code)
	}
}

func TestBreakGlassGuardsE2E(t *testing.T) {
	srv, base, _, proj := setupBreakGlass(t)
	ctx := context.Background()

	// A viewer on project P.
	uid, pw, err := srv.auth.CreateUser(ctx, "viewer@corp.io")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.authz.Grant(ctx, store.RoleBindingInput{
		SubjectUserID: uid, ScopeLevel: "project", ProjectID: &proj.ID, Role: "viewer",
	}); err != nil {
		t.Fatal(err)
	}
	cookie := login(t, base, "viewer@corp.io", pw)

	// Mandatory reason: empty → 400.
	empty := fmt.Sprintf(`{"scope_level":"project","project_id":%q,"role":"admin","reason":"   "}`, proj.ID)
	if code := doAuthed(t, "POST", base+"/v1/break-glass", cookie, "", empty, nil); code != http.StatusBadRequest {
		t.Fatalf("empty reason: want 400, got %d", code)
	}

	// No base binding on a scope: instance (viewer holds nothing at instance) → 403.
	noBinding := `{"scope_level":"instance","role":"admin","reason":"x"}`
	if code := doAuthed(t, "POST", base+"/v1/break-glass", cookie, "", noBinding, nil); code != http.StatusForbidden {
		t.Fatalf("no base binding: want 403, got %d", code)
	}

	// No-op elevation: target ≤ held role (viewer→viewer) → 400.
	noop := fmt.Sprintf(`{"scope_level":"project","project_id":%q,"role":"viewer","reason":"x"}`, proj.ID)
	if code := doAuthed(t, "POST", base+"/v1/break-glass", cookie, "", noop, nil); code != http.StatusBadRequest {
		t.Fatalf("no-op elevation: want 400, got %d", code)
	}

	// Invalid role → 400.
	badRole := fmt.Sprintf(`{"scope_level":"project","project_id":%q,"role":"superuser","reason":"x"}`, proj.ID)
	if code := doAuthed(t, "POST", base+"/v1/break-glass", cookie, "", badRole, nil); code != http.StatusBadRequest {
		t.Fatalf("bad role: want 400, got %d", code)
	}
}

func TestBreakGlassTTLClampE2E(t *testing.T) {
	srv, base, _, proj := setupBreakGlass(t)
	ctx := context.Background()

	uid, pw, err := srv.auth.CreateUser(ctx, "clamp@corp.io")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.authz.Grant(ctx, store.RoleBindingInput{
		SubjectUserID: uid, ScopeLevel: "project", ProjectID: &proj.ID, Role: "developer",
	}); err != nil {
		t.Fatal(err)
	}
	cookie := login(t, base, "clamp@corp.io", pw)

	// Request 10h; the default max is 1h → clamp.
	var g grantView
	body := fmt.Sprintf(`{"scope_level":"project","project_id":%q,"role":"admin","reason":"clamp me","ttl":"10h"}`, proj.ID)
	before := time.Now()
	if code := doAuthed(t, "POST", base+"/v1/break-glass", cookie, "", body, &g); code != http.StatusCreated {
		t.Fatalf("activate: %d", code)
	}
	// expires_at must not exceed now + 1h (+ small slack).
	if g.ExpiresAt.After(before.Add(time.Hour + time.Minute)) {
		t.Fatalf("ttl not clamped: expires_at=%s (now=%s)", g.ExpiresAt, before)
	}
}

func TestBreakGlassListVisibilityE2E(t *testing.T) {
	srv, base, ownerCookie, proj := setupBreakGlass(t)
	ctx := context.Background()

	uid, pw, err := srv.auth.CreateUser(ctx, "lister@corp.io")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.authz.Grant(ctx, store.RoleBindingInput{
		SubjectUserID: uid, ScopeLevel: "project", ProjectID: &proj.ID, Role: "developer",
	}); err != nil {
		t.Fatal(err)
	}
	cookie := login(t, base, "lister@corp.io", pw)

	body := fmt.Sprintf(`{"scope_level":"project","project_id":%q,"role":"admin","reason":"listing"}`, proj.ID)
	if code := doAuthed(t, "POST", base+"/v1/break-glass", cookie, "", body, nil); code != http.StatusCreated {
		t.Fatalf("activate: %d", code)
	}

	// The user sees their own grant.
	var mine struct {
		Grants []grantView `json:"grants"`
	}
	if code := doAuthed(t, "GET", base+"/v1/break-glass", cookie, "", "", &mine); code != http.StatusOK || len(mine.Grants) != 1 {
		t.Fatalf("self list: code=%d grants=%d", code, len(mine.Grants))
	}

	// The owner (instance member:manage) sees it too.
	var all struct {
		Grants []grantView `json:"grants"`
	}
	if code := doAuthed(t, "GET", base+"/v1/break-glass", ownerCookie, "", "", &all); code != http.StatusOK || len(all.Grants) != 1 {
		t.Fatalf("admin list: code=%d grants=%d", code, len(all.Grants))
	}

	// The list must never carry secret material — reason is the only text and is
	// operator-entered. Assert it is present and the payload shape is value-safe.
	if all.Grants[0].Reason != "listing" {
		t.Fatalf("reason missing/altered: %+v", all.Grants[0])
	}
}

// TestBreakGlassActivateAuditedE2E proves activation stamps a LOUD audit event.
func TestBreakGlassActivateAuditedE2E(t *testing.T) {
	srv, base, ownerCookie, proj := setupBreakGlass(t)
	ctx := context.Background()

	uid, pw, err := srv.auth.CreateUser(ctx, "audit@corp.io")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.authz.Grant(ctx, store.RoleBindingInput{
		SubjectUserID: uid, ScopeLevel: "project", ProjectID: &proj.ID, Role: "developer",
	}); err != nil {
		t.Fatal(err)
	}
	cookie := login(t, base, "audit@corp.io", pw)

	body := fmt.Sprintf(`{"scope_level":"project","project_id":%q,"role":"admin","reason":"loud-audit"}`, proj.ID)
	if code := doAuthed(t, "POST", base+"/v1/break-glass", cookie, "", body, nil); code != http.StatusCreated {
		t.Fatalf("activate: %d", code)
	}

	// The audit export (owner is instance owner → AuditRead) must contain the
	// breakglass.activate event.
	req, _ := http.NewRequest("GET", base+"/v1/audit/export?format=jsonl", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: ownerCookie})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(raw), "breakglass.activate") {
		t.Fatalf("activate audit event missing from export")
	}
	// The reason text is operator-entered (non-secret) and expected in the detail.
	if !strings.Contains(string(raw), "loud-audit") {
		t.Fatalf("audit detail should include the reason")
	}
}
