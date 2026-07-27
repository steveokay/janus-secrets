package api

import (
	"context"
	"testing"
)

type derivedResp struct {
	Bindings []struct {
		GroupID string `json:"group_id"`
		Role    string `json:"role"`
	} `json:"bindings"`
	Derived []struct {
		UserID       string `json:"user_id"`
		Role         string `json:"role"`
		ViaGroupID   string `json:"via_group_id"`
		ViaGroupName string `json:"via_group_name"`
	} `json:"derived_members"`
	Truncated bool `json:"derived_truncated"`
}

// The defect this fixes: the Members screen read direct role_bindings only, so
// a user whose role came entirely from a group rendered as "no binding" — wrong
// information on the screen whose job is to say who has access.
func TestScopeReportsGroupDerivedMembersE2E(t *testing.T) {
	ts, srv, email, password, _ := authStackFull(t)
	owner := login(t, ts.URL, email, password)
	ctx := context.Background()
	proj, err := srv.service.CreateProject(ctx, "app", "App")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	var dev struct{ ID, Password string }
	if code := doAuthed(t, "POST", ts.URL+"/v1/users", owner, "", `{"email":"dev@corp.io"}`, &dev); code != 200 {
		t.Fatalf("create user: %d", code)
	}
	g := createGroup(t, ts.URL, owner, `{"name":"payments","kind":"local"}`)
	if code := doAuthed(t, "PUT", ts.URL+"/v1/groups/"+g.ID+"/members/"+dev.ID, owner, "", "", nil); code != 204 {
		t.Fatalf("add member: %d", code)
	}
	if code := doAuthed(t, "PUT", ts.URL+"/v1/projects/"+proj.ID+"/group-members/"+g.ID, owner, "", `{"role":"developer"}`, nil); code != 204 {
		t.Fatalf("bind group: %d", code)
	}

	// The user holds NO direct binding, so the plain members list is empty...
	var direct struct {
		Members []struct {
			UserID string `json:"user_id"`
		} `json:"members"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/projects/"+proj.ID+"/members", owner, "", "", &direct); code != 200 {
		t.Fatalf("list members: %d", code)
	}
	for _, m := range direct.Members {
		if m.UserID == dev.ID {
			t.Fatal("the user should have no DIRECT binding in this fixture")
		}
	}

	// ...but the scope must still report that they reach it, and via what.
	var got derivedResp
	if code := doAuthed(t, "GET", ts.URL+"/v1/projects/"+proj.ID+"/group-members", owner, "", "", &got); code != 200 {
		t.Fatalf("list group access: %d", code)
	}
	if len(got.Derived) != 1 {
		t.Fatalf("derived members = %+v, want exactly 1", got.Derived)
	}
	d := got.Derived[0]
	if d.UserID != dev.ID || d.Role != "developer" || d.ViaGroupID != g.ID || d.ViaGroupName != "payments" {
		t.Fatalf("derived member = %+v", d)
	}
	if got.Truncated {
		t.Fatal("one member must not report truncation")
	}

	// Removing the membership removes the derived row — the screen tracks
	// reality rather than caching a grant that no longer applies.
	if code := doAuthed(t, "DELETE", ts.URL+"/v1/groups/"+g.ID+"/members/"+dev.ID, owner, "", "", nil); code != 204 {
		t.Fatalf("remove member: %d", code)
	}
	got = derivedResp{}
	if code := doAuthed(t, "GET", ts.URL+"/v1/projects/"+proj.ID+"/group-members", owner, "", "", &got); code != 200 {
		t.Fatalf("re-list: %d", code)
	}
	if len(got.Derived) != 0 {
		t.Fatalf("derived after removal = %+v, want none", got.Derived)
	}
}

// The whole point of resolving this server-side: a project admin holds
// member:read on their scope but NOT instance group:manage, so they cannot list
// a group's members themselves. Without this they could not answer "who can act
// on my project?" at all.
func TestScopeAdminSeesDerivedWithoutGroupManageE2E(t *testing.T) {
	ts, srv, email, password, _ := authStackFull(t)
	owner := login(t, ts.URL, email, password)
	ctx := context.Background()
	proj, err := srv.service.CreateProject(ctx, "app", "App")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	var lead, member struct{ ID, Password string }
	if code := doAuthed(t, "POST", ts.URL+"/v1/users", owner, "", `{"email":"lead@corp.io"}`, &lead); code != 200 {
		t.Fatalf("create lead: %d", code)
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/users", owner, "", `{"email":"member@corp.io"}`, &member); code != 200 {
		t.Fatalf("create member: %d", code)
	}
	if code := doAuthed(t, "PUT", ts.URL+"/v1/projects/"+proj.ID+"/members/"+lead.ID, owner, "", `{"role":"admin"}`, nil); code != 204 {
		t.Fatalf("grant project admin: %d", code)
	}
	g := createGroup(t, ts.URL, owner, `{"name":"payments","kind":"local"}`)
	if code := doAuthed(t, "PUT", ts.URL+"/v1/groups/"+g.ID+"/members/"+member.ID, owner, "", "", nil); code != 204 {
		t.Fatalf("add member: %d", code)
	}
	if code := doAuthed(t, "PUT", ts.URL+"/v1/projects/"+proj.ID+"/group-members/"+g.ID, owner, "", `{"role":"developer"}`, nil); code != 204 {
		t.Fatalf("bind group: %d", code)
	}

	leadSession := login(t, ts.URL, "lead@corp.io", lead.Password)
	// Confirm the premise: the project admin genuinely cannot read the catalog.
	if code := doAuthed(t, "GET", ts.URL+"/v1/groups/"+g.ID+"/members", leadSession, "", "", nil); code != 403 {
		t.Fatalf("project admin listing group members: got %d, want 403", code)
	}
	// But they can see who reaches THEIR scope, and why.
	var got derivedResp
	if code := doAuthed(t, "GET", ts.URL+"/v1/projects/"+proj.ID+"/group-members", leadSession, "", "", &got); code != 200 {
		t.Fatalf("project admin listing scope group access: %d", code)
	}
	if len(got.Derived) != 1 || got.Derived[0].UserID != member.ID || got.Derived[0].ViaGroupName != "payments" {
		t.Fatalf("derived = %+v", got.Derived)
	}
}

// A group bound at another scope must not bleed into this one's derived list.
func TestDerivedMembersAreScopedE2E(t *testing.T) {
	ts, srv, email, password, _ := authStackFull(t)
	owner := login(t, ts.URL, email, password)
	ctx := context.Background()
	projA, err := srv.service.CreateProject(ctx, "a", "A")
	if err != nil {
		t.Fatalf("create a: %v", err)
	}
	projB, err := srv.service.CreateProject(ctx, "b", "B")
	if err != nil {
		t.Fatalf("create b: %v", err)
	}

	var u struct{ ID, Password string }
	if code := doAuthed(t, "POST", ts.URL+"/v1/users", owner, "", `{"email":"u@corp.io"}`, &u); code != 200 {
		t.Fatalf("create user: %d", code)
	}
	g := createGroup(t, ts.URL, owner, `{"name":"team","kind":"local"}`)
	if code := doAuthed(t, "PUT", ts.URL+"/v1/groups/"+g.ID+"/members/"+u.ID, owner, "", "", nil); code != 204 {
		t.Fatalf("add member: %d", code)
	}
	if code := doAuthed(t, "PUT", ts.URL+"/v1/projects/"+projA.ID+"/group-members/"+g.ID, owner, "", `{"role":"admin"}`, nil); code != 204 {
		t.Fatalf("bind on A: %d", code)
	}

	var onA, onB derivedResp
	if code := doAuthed(t, "GET", ts.URL+"/v1/projects/"+projA.ID+"/group-members", owner, "", "", &onA); code != 200 {
		t.Fatalf("list A: %d", code)
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/projects/"+projB.ID+"/group-members", owner, "", "", &onB); code != 200 {
		t.Fatalf("list B: %d", code)
	}
	if len(onA.Derived) != 1 {
		t.Fatalf("A derived = %+v, want 1", onA.Derived)
	}
	if len(onB.Derived) != 0 {
		t.Fatalf("B derived = %+v, want none — a project binding must not leak", onB.Derived)
	}
}
