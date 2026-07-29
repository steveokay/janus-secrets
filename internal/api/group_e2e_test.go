package api

import (
	"context"
	"testing"
)

type groupResp struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Kind       string  `json:"kind"`
	ClaimValue *string `json:"claim_value"`
}

// createGroup is the happy-path helper the invariant tests build on.
func createGroup(t *testing.T, base, cookie, body string) groupResp {
	t.Helper()
	var g groupResp
	if code := doAuthed(t, "POST", base+"/v1/groups", cookie, "", body, &g); code != 201 {
		t.Fatalf("create group %s: %d", body, code)
	}
	return g
}

// The end-to-end shape the feature exists for: bind a group once, and every
// member of it holds that role — with the access disappearing the moment the
// binding or the membership goes away, because permissions are resolved per
// request and never frozen into a session.
func TestGroupBindingGrantsAndRevokesE2E(t *testing.T) {
	ts, srv, email, password, _ := authStackFull(t)
	admin := login(t, ts.URL, email, password)
	ctx := context.Background()
	proj, err := srv.service.CreateProject(ctx, "app", "App")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	var member struct{ ID, Password string }
	if code := doAuthed(t, "POST", ts.URL+"/v1/users", admin, "", `{"email":"dev@corp.io"}`, &member); code != 200 {
		t.Fatalf("create user: %d", code)
	}
	dev := login(t, ts.URL, "dev@corp.io", member.Password)

	// No bindings at all: the project is invisible, not merely unreadable.
	if code := doAuthed(t, "GET", ts.URL+"/v1/projects/"+proj.ID+"/members", dev, "", "", nil); code != 403 {
		t.Fatalf("unbound user reading members: got %d, want 403", code)
	}

	g := createGroup(t, ts.URL, admin, `{"name":"payments","kind":"local"}`)
	if code := doAuthed(t, "PUT", ts.URL+"/v1/groups/"+g.ID+"/members/"+member.ID, admin, "", "", nil); code != 204 {
		t.Fatalf("add member: %d", code)
	}
	// Membership alone grants nothing until the group is bound somewhere.
	if code := doAuthed(t, "GET", ts.URL+"/v1/projects/"+proj.ID+"/members", dev, "", "", nil); code != 403 {
		t.Fatalf("member of an unbound group: got %d, want 403", code)
	}

	if code := doAuthed(t, "PUT", ts.URL+"/v1/projects/"+proj.ID+"/group-members/"+g.ID, admin, "", `{"role":"developer"}`, nil); code != 204 {
		t.Fatalf("bind group: %d", code)
	}
	// Same live session, no re-login: the next request already sees the grant.
	if code := doAuthed(t, "GET", ts.URL+"/v1/projects/"+proj.ID+"/members", dev, "", "", nil); code != 200 {
		t.Fatalf("group-derived read: got %d, want 200", code)
	}

	// The binding is visible on the scope's Groups listing.
	var listed struct {
		Bindings []struct {
			GroupID   string `json:"group_id"`
			GroupName string `json:"group_name"`
			Role      string `json:"role"`
		} `json:"bindings"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/projects/"+proj.ID+"/group-members", admin, "", "", &listed); code != 200 {
		t.Fatalf("list group bindings: %d", code)
	}
	if len(listed.Bindings) != 1 || listed.Bindings[0].GroupName != "payments" || listed.Bindings[0].Role != "developer" {
		t.Fatalf("group bindings = %+v", listed.Bindings)
	}

	// Removing the membership revokes the access on the very next request.
	if code := doAuthed(t, "DELETE", ts.URL+"/v1/groups/"+g.ID+"/members/"+member.ID, admin, "", "", nil); code != 204 {
		t.Fatalf("remove member: %d", code)
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/projects/"+proj.ID+"/members", dev, "", "", nil); code != 403 {
		t.Fatalf("after removal: got %d, want 403", code)
	}

	// Re-add, then delete the whole group: the binding cascades away too.
	if code := doAuthed(t, "PUT", ts.URL+"/v1/groups/"+g.ID+"/members/"+member.ID, admin, "", "", nil); code != 204 {
		t.Fatalf("re-add member: %d", code)
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/projects/"+proj.ID+"/members", dev, "", "", nil); code != 200 {
		t.Fatalf("after re-add: got %d, want 200", code)
	}
	if code := doAuthed(t, "DELETE", ts.URL+"/v1/groups/"+g.ID, admin, "", "", nil); code != 204 {
		t.Fatalf("delete group: %d", code)
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/projects/"+proj.ID+"/members", dev, "", "", nil); code != 403 {
		t.Fatalf("after group delete: got %d, want 403", code)
	}
}

// Owner is never group-derived. The API rejects it before the store does, but
// both refuse: instance ownership rotates the master key, prunes the audit
// chain and destroys secret history, so it must be a deliberate direct grant.
func TestGroupBindingCannotGrantOwnerE2E(t *testing.T) {
	ts, _, email, password, _ := authStackFull(t)
	admin := login(t, ts.URL, email, password)
	g := createGroup(t, ts.URL, admin, `{"name":"root","kind":"local"}`)

	var env errEnvelope
	code := doAuthed(t, "PUT", ts.URL+"/v1/instance/group-members/"+g.ID, admin, "", `{"role":"owner"}`, &env)
	if code != 400 {
		t.Fatalf("bind group as owner: got %d, want 400 (%+v)", code, env)
	}
	// admin is fine at the same scope, so the rejection is about the ROLE, not
	// the route or the caller.
	if code := doAuthed(t, "PUT", ts.URL+"/v1/instance/group-members/"+g.ID, admin, "", `{"role":"admin"}`, nil); code != 204 {
		t.Fatalf("bind group as admin: %d", code)
	}
}

// Membership of an IdP-fed group comes from the IdP, full stop. This is the
// invariant that lets an access review run against Entra be COMPLETE.
func TestOIDCGroupRejectsManualMembershipE2E(t *testing.T) {
	ts, _, email, password, _ := authStackFull(t)
	admin := login(t, ts.URL, email, password)

	var member struct{ ID, Password string }
	if code := doAuthed(t, "POST", ts.URL+"/v1/users", admin, "", `{"email":"x@corp.io"}`, &member); code != 200 {
		t.Fatalf("create user: %d", code)
	}
	g := createGroup(t, ts.URL, admin, `{"name":"payments","kind":"oidc","claim_value":"grp-payments"}`)

	var env errEnvelope
	if code := doAuthed(t, "PUT", ts.URL+"/v1/groups/"+g.ID+"/members/"+member.ID, admin, "", "", &env); code != 409 {
		t.Fatalf("manual add to an oidc group: got %d, want 409 (%+v)", code, env)
	}
	if code := doAuthed(t, "DELETE", ts.URL+"/v1/groups/"+g.ID+"/members/"+member.ID, admin, "", "", &env); code != 409 {
		t.Fatalf("manual remove from an oidc group: got %d, want 409 (%+v)", code, env)
	}
}

func TestGroupValidationE2E(t *testing.T) {
	ts, _, email, password, _ := authStackFull(t)
	admin := login(t, ts.URL, email, password)

	tests := []struct {
		name string
		body string
		want int
	}{
		{"oidc group needs a claim value", `{"name":"a","kind":"oidc"}`, 400},
		{"local group rejects a claim value", `{"name":"b","kind":"local","claim_value":"x"}`, 400},
		{"unknown kind", `{"name":"c","kind":"ldap"}`, 400},
		{"empty name", `{"name":"","kind":"local"}`, 400},
		{"valid local", `{"name":"ok","kind":"local"}`, 201},
		{"duplicate name", `{"name":"ok","kind":"local"}`, 409},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if code := doAuthed(t, "POST", ts.URL+"/v1/groups", admin, "", tc.body, nil); code != tc.want {
				t.Fatalf("got %d, want %d", code, tc.want)
			}
		})
	}
}

// The delegation cap must apply to group bindings exactly as it does to user
// bindings — otherwise groups are a way around it. And curating the group
// catalog is instance-scoped group:manage, so a project admin cannot reach
// another project by adding themselves to a group.
func TestGroupBindingDelegationCapE2E(t *testing.T) {
	ts, srv, email, password, _ := authStackFull(t)
	owner := login(t, ts.URL, email, password)
	ctx := context.Background()
	projA, err := srv.service.CreateProject(ctx, "a", "A")
	if err != nil {
		t.Fatalf("create project a: %v", err)
	}
	projB, err := srv.service.CreateProject(ctx, "b", "B")
	if err != nil {
		t.Fatalf("create project b: %v", err)
	}

	var dev struct{ ID, Password string }
	if code := doAuthed(t, "POST", ts.URL+"/v1/users", owner, "", `{"email":"lead@corp.io"}`, &dev); code != 200 {
		t.Fatalf("create user: %d", code)
	}
	// A developer on project A — enough to see members, not to manage them.
	if code := doAuthed(t, "PUT", ts.URL+"/v1/projects/"+projA.ID+"/members/"+dev.ID, owner, "", `{"role":"developer"}`, nil); code != 204 {
		t.Fatalf("grant developer: %d", code)
	}
	lead := login(t, ts.URL, "lead@corp.io", dev.Password)
	g := createGroup(t, ts.URL, owner, `{"name":"team","kind":"local"}`)

	// A developer holds no member:manage, so they cannot bind a group at all.
	if code := doAuthed(t, "PUT", ts.URL+"/v1/projects/"+projA.ID+"/group-members/"+g.ID, lead, "", `{"role":"viewer"}`, nil); code != 403 {
		t.Fatalf("developer binding a group: got %d, want 403", code)
	}

	// Promote to admin on A. Now they can bind up to their own role there...
	if code := doAuthed(t, "PUT", ts.URL+"/v1/projects/"+projA.ID+"/members/"+dev.ID, owner, "", `{"role":"admin"}`, nil); code != 204 {
		t.Fatalf("promote to admin: %d", code)
	}
	if code := doAuthed(t, "PUT", ts.URL+"/v1/projects/"+projA.ID+"/group-members/"+g.ID, lead, "", `{"role":"developer"}`, nil); code != 204 {
		t.Fatalf("admin binding a group below their role: %d", code)
	}
	// ...but not at project B, where they hold nothing.
	if code := doAuthed(t, "PUT", ts.URL+"/v1/projects/"+projB.ID+"/group-members/"+g.ID, lead, "", `{"role":"viewer"}`, nil); code != 403 {
		t.Fatalf("binding a group on a foreign project: got %d, want 403", code)
	}
	// ...and cannot curate the catalog, which is what keeps the two authorities
	// separate: they can grant a group access to their project, but cannot put
	// themselves into a group that is bound elsewhere.
	if code := doAuthed(t, "POST", ts.URL+"/v1/groups", lead, "", `{"name":"mine","kind":"local"}`, nil); code != 403 {
		t.Fatalf("project admin creating a group: got %d, want 403", code)
	}
	if code := doAuthed(t, "PUT", ts.URL+"/v1/groups/"+g.ID+"/members/"+dev.ID, lead, "", "", nil); code != 403 {
		t.Fatalf("project admin adding a group member: got %d, want 403", code)
	}

	// Unbinding is symmetrical.
	if code := doAuthed(t, "DELETE", ts.URL+"/v1/projects/"+projA.ID+"/group-members/"+g.ID, lead, "", "", nil); code != 204 {
		t.Fatalf("unbind: %d", code)
	}
	if code := doAuthed(t, "DELETE", ts.URL+"/v1/projects/"+projA.ID+"/group-members/"+g.ID, lead, "", "", nil); code != 404 {
		t.Fatalf("double unbind: got %d, want 404", code)
	}
}

// A group binding is durable, so it counts toward the delegation cap — a user
// who is admin only THROUGH a group can still grant up to admin.
func TestGroupDerivedRoleCountsTowardDelegationE2E(t *testing.T) {
	ts, srv, email, password, _ := authStackFull(t)
	owner := login(t, ts.URL, email, password)
	ctx := context.Background()
	proj, err := srv.service.CreateProject(ctx, "app", "App")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	var lead, other struct{ ID, Password string }
	if code := doAuthed(t, "POST", ts.URL+"/v1/users", owner, "", `{"email":"lead@corp.io"}`, &lead); code != 200 {
		t.Fatalf("create lead: %d", code)
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/users", owner, "", `{"email":"other@corp.io"}`, &other); code != 200 {
		t.Fatalf("create other: %d", code)
	}

	g := createGroup(t, ts.URL, owner, `{"name":"leads","kind":"local"}`)
	if code := doAuthed(t, "PUT", ts.URL+"/v1/groups/"+g.ID+"/members/"+lead.ID, owner, "", "", nil); code != 204 {
		t.Fatalf("add member: %d", code)
	}
	if code := doAuthed(t, "PUT", ts.URL+"/v1/projects/"+proj.ID+"/group-members/"+g.ID, owner, "", `{"role":"admin"}`, nil); code != 204 {
		t.Fatalf("bind group admin: %d", code)
	}

	// The lead has NO direct binding — admin comes purely from the group.
	leadSession := login(t, ts.URL, "lead@corp.io", lead.Password)
	if code := doAuthed(t, "PUT", ts.URL+"/v1/projects/"+proj.ID+"/members/"+other.ID, leadSession, "", `{"role":"developer"}`, nil); code != 204 {
		t.Fatalf("group-admin granting developer: %d", code)
	}
	// Still capped: they cannot exceed their own (group-derived) role.
	if code := doAuthed(t, "PUT", ts.URL+"/v1/projects/"+proj.ID+"/members/"+other.ID, leadSession, "", `{"role":"owner"}`, nil); code != 403 {
		t.Fatalf("group-admin granting owner: got %d, want 403", code)
	}
}

// TestGroupMembershipCompletenessIsReportedE2E pins the one place the group
// model gives a narrower answer than the question implies.
//
// A `local` group's member list IS its membership — it is admin-managed. An
// `oidc` group's is a snapshot refreshed at each login, so anyone who has never
// signed into Janus is absent from the count. Nothing is mis-granted (they get
// the access on first sign-in); the risk is a READER mistaking a partial answer
// for a complete one during an access review, which is why the API states it
// rather than leaving every client to re-derive it from `kind`.
func TestGroupMembershipCompletenessIsReportedE2E(t *testing.T) {
	ts, _, email, password, _ := authStackFull(t)
	admin := login(t, ts.URL, email, password)

	type groupWire struct {
		ID                 string `json:"id"`
		Kind               string `json:"kind"`
		MemberCount        int    `json:"member_count"`
		MembershipComplete bool   `json:"membership_complete"`
	}

	var local, oidc groupWire
	if code := doAuthed(t, "POST", ts.URL+"/v1/groups", admin, "",
		`{"name":"complete-local","kind":"local"}`, &local); code != 201 {
		t.Fatalf("create local: %d", code)
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/groups", admin, "",
		`{"name":"complete-oidc","kind":"oidc","claim_value":"grp-x"}`, &oidc); code != 201 {
		t.Fatalf("create oidc: %d", code)
	}

	if !local.MembershipComplete {
		t.Error("a local group's membership is admin-managed and therefore complete")
	}
	if oidc.MembershipComplete {
		t.Error("an oidc group's membership is a login-time snapshot and must NOT be reported as complete")
	}

	// It must survive the list path too — the Groups table renders from it, and
	// that table is where a bare count would read as a membership list.
	var list struct {
		Groups []groupWire `json:"groups"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/groups", admin, "", "", &list); code != 200 {
		t.Fatalf("list: %d", code)
	}
	var seenLocal, seenOIDC bool
	for _, g := range list.Groups {
		switch g.ID {
		case local.ID:
			seenLocal = true
			if !g.MembershipComplete {
				t.Error("list: local group reported incomplete")
			}
		case oidc.ID:
			seenOIDC = true
			if g.MembershipComplete {
				t.Error("list: oidc group reported complete")
			}
		}
	}
	if !seenLocal || !seenOIDC {
		t.Fatalf("both groups should appear in the list (local=%v oidc=%v)", seenLocal, seenOIDC)
	}
}
