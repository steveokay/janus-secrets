package api

import "testing"

func TestOIDCFederationConfigRBAC(t *testing.T) {
	ts, srv, adminEmail, adminPassword, _ := authStackFull(t)
	ctx := t.Context()
	owner := login(t, ts.URL, adminEmail, adminPassword)

	vid, vpw, err := srv.auth.CreateUser(ctx, "viewer@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if code := doAuthed(t, "PUT", ts.URL+"/v1/instance/members/"+vid, owner, "", `{"role":"viewer"}`, nil); code != 204 {
		t.Fatalf("grant viewer: %d", code)
	}
	viewer := login(t, ts.URL, "viewer@example.com", vpw)

	body := `{"issuer":"https://token.actions.githubusercontent.com","audience":"janus","enabled":true}`
	if code := doAuthed(t, "PUT", ts.URL+"/v1/sys/oidc/federation", owner, "", body, nil); code != 200 {
		t.Fatalf("owner PUT: %d", code)
	}
	if code := doAuthed(t, "PUT", ts.URL+"/v1/sys/oidc/federation", viewer, "", body, nil); code != 403 {
		t.Fatalf("viewer PUT: want 403, got %d", code)
	}
	var got struct {
		Audience string `json:"audience"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/sys/oidc/federation", owner, "", "", &got); code != 200 || got.Audience != "janus" {
		t.Fatalf("owner GET: %d %+v", code, got)
	}
	if code := doAuthed(t, "DELETE", ts.URL+"/v1/sys/oidc/federation", owner, "", "", nil); code != 204 {
		t.Fatalf("owner DELETE: %d", code)
	}
}
