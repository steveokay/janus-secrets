package api

import (
	"context"
	"testing"
)

func TestMembershipE2E(t *testing.T) {
	ts, srv, email, password, _ := authStackFull(t)
	admin := login(t, ts.URL, email, password)

	// A project to scope a binding to (same wired service authStack itself uses).
	ctx := context.Background()
	proj, err := srv.service.CreateProject(ctx, "app", "App")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	// Admin creates a member user.
	var member struct{ ID, Password string }
	if code := doAuthed(t, "POST", ts.URL+"/v1/users", admin, "", `{"email":"dev@corp.io"}`, &member); code != 200 {
		t.Fatalf("create user: %d", code)
	}

	// Owner grants developer at the project scope.
	if code := doAuthed(t, "PUT", ts.URL+"/v1/projects/"+proj.ID+"/members/"+member.ID, admin, "", `{"role":"developer"}`, nil); code != 204 {
		t.Fatalf("grant developer: %d", code)
	}

	// The member (developer) can read members (member:read) but cannot grant
	// admin — above their own effective role → 403.
	dev := login(t, ts.URL, "dev@corp.io", member.Password)
	if code := doAuthed(t, "GET", ts.URL+"/v1/projects/"+proj.ID+"/members", dev, "", "", nil); code != 200 {
		t.Fatalf("dev list members: %d", code)
	}
	var env errEnvelope
	if code := doAuthed(t, "PUT", ts.URL+"/v1/projects/"+proj.ID+"/members/"+member.ID, dev, "", `{"role":"admin"}`, &env); code != 403 || env.Error.Code != "forbidden" {
		t.Fatalf("dev grant admin: %d %+v", code, env)
	}

	// Never-lock-out: admin cannot remove the last instance owner (self).
	adminID := meID(t, ts.URL, admin)
	if code := doAuthed(t, "DELETE", ts.URL+"/v1/instance/members/"+adminID, admin, "", "", nil); code != 409 {
		t.Fatalf("remove last owner: got %d, want 409", code)
	}

	// Owner revokes the project binding.
	if code := doAuthed(t, "DELETE", ts.URL+"/v1/projects/"+proj.ID+"/members/"+member.ID, admin, "", "", nil); code != 204 {
		t.Fatalf("revoke: %d", code)
	}
}

// meID fetches the caller's own user id from /v1/auth/me (handleMe already
// returns "id").
func meID(t *testing.T, base, cookie string) string {
	t.Helper()
	var me struct {
		ID string `json:"id"`
	}
	if code := doAuthed(t, "GET", base+"/v1/auth/me", cookie, "", "", &me); code != 200 {
		t.Fatalf("me: %d", code)
	}
	if me.ID == "" {
		t.Fatal("me: empty id")
	}
	return me.ID
}
