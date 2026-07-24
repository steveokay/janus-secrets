package api

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/steveokay/janus-secrets/internal/store"
)

// TestBreakGlassCannotMintDurableElevatedGrant verifies that a user who has
// temporarily elevated via break-glass cannot convert that elevation into a
// PERMANENT role binding at the elevated role. The member-grant delegation cap
// is measured against the granter's DURABLE (bound) role, not their effective
// role (which includes active break-glass grants) — so an emergency elevation
// can never be laundered into lasting privilege. Regression for finding M-1.
func TestBreakGlassCannotMintDurableElevatedGrant(t *testing.T) {
	srv, base, _, proj := setupBreakGlass(t)
	ctx := context.Background()

	// A developer on project P (durable/bound role = developer).
	uid, pw, err := srv.auth.CreateUser(ctx, "bg-dev@corp.io")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.authz.Grant(ctx, store.RoleBindingInput{
		SubjectUserID: uid, ScopeLevel: "project", ProjectID: &proj.ID, Role: "developer",
	}); err != nil {
		t.Fatal(err)
	}
	cookie := login(t, base, "bg-dev@corp.io", pw)

	// A second user to be the grant subject (so we're not relying on self-grant).
	subjectID, _, err := srv.auth.CreateUser(ctx, "bg-subject@corp.io")
	if err != nil {
		t.Fatal(err)
	}

	// Elevate to admin on project P via break-glass (guarded: already holds a
	// role, admin > developer).
	var g grantView
	body := fmt.Sprintf(`{"scope_level":"project","project_id":%q,"role":"admin","reason":"incident","ttl":"20m"}`, proj.ID)
	if code := doAuthed(t, "POST", base+"/v1/break-glass", cookie, "", body, &g); code != http.StatusCreated {
		t.Fatalf("activate break-glass: want 201, got %d", code)
	}

	// While elevated, member:manage passes (that IS the break-glass power), so a
	// grant WITHIN the durable role is allowed: developer can delegate up to
	// developer. Granting "developer" to the subject succeeds.
	if code := doAuthed(t, "PUT", base+"/v1/projects/"+proj.ID+"/members/"+subjectID, cookie, "",
		`{"role":"developer"}`, nil); code != http.StatusNoContent {
		t.Fatalf("grant at/below bound role (developer) should succeed while elevated: got %d", code)
	}

	// But granting a role ABOVE the durable (bound) role must be refused, even
	// though the caller's EFFECTIVE role is admin via the live grant. This is the
	// fix: the elevation cannot be minted into a permanent admin/owner binding.
	for _, role := range []string{"admin", "owner"} {
		if code := doAuthed(t, "PUT", base+"/v1/projects/"+proj.ID+"/members/"+subjectID, cookie, "",
			fmt.Sprintf(`{"role":%q}`, role), nil); code != http.StatusForbidden {
			t.Fatalf("granting %q above bound role while break-glass-elevated: want 403, got %d", role, code)
		}
	}

	// Sanity: an actual bound admin (no break-glass) CAN grant admin — proving the
	// cap tracks the durable role, not a blanket block. Owner (from setup) grants
	// the developer a durable admin binding; then that user (now bound admin) can
	// delegate admin.
	if err := srv.authz.Grant(ctx, store.RoleBindingInput{
		SubjectUserID: uid, ScopeLevel: "project", ProjectID: &proj.ID, Role: "admin",
	}); err != nil {
		t.Fatal(err)
	}
	if code := doAuthed(t, "PUT", base+"/v1/projects/"+proj.ID+"/members/"+subjectID, cookie, "",
		`{"role":"admin"}`, nil); code != http.StatusNoContent {
		t.Fatalf("bound admin should delegate admin: got %d", code)
	}
}
