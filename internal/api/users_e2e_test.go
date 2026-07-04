package api

import (
	"testing"
)

func TestUserManagementE2E(t *testing.T) {
	ts, email, password, _ := authStack(t)
	admin := login(t, ts.URL, email, password)

	// Admin (instance owner) creates a user.
	var created struct{ ID, Email, Password string }
	if code := doAuthed(t, "POST", ts.URL+"/v1/users", admin, "", `{"email":"member@corp.io"}`, &created); code != 200 {
		t.Fatalf("create user: %d", code)
	}
	if created.ID == "" || len(created.Password) < 16 {
		t.Fatalf("created = %+v", created)
	}

	// The new user can log in but is not an admin: creating users → 403.
	member := login(t, ts.URL, "member@corp.io", created.Password)
	var env errEnvelope
	if code := doAuthed(t, "POST", ts.URL+"/v1/users", member, "", `{"email":"x@corp.io"}`, &env); code != 403 || env.Error.Code != "forbidden" {
		t.Fatalf("member create user: %d %+v", code, env)
	}

	// Admin disables the member → member login stops working.
	if code := doAuthed(t, "POST", ts.URL+"/v1/users/"+created.ID+"/disable", admin, "", "", nil); code != 204 {
		t.Fatalf("disable: %d", code)
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/auth/me", member, "", "", nil); code == 200 {
		t.Fatal("disabled member session still valid")
	}
}
