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

	// --- multi-issuer trust set (roadmap 7.3) ---
	type issuerRow struct {
		ID       string `json:"id"`
		Issuer   string `json:"issuer"`
		Audience string `json:"audience"`
		Preset   string `json:"preset"`
	}
	const issuersPath = "/v1/sys/oidc/federation/issuers"
	gh := `{"issuer":"https://token.actions.githubusercontent.com","audience":"janus","preset":"github","enabled":true}`
	k8s := `{"issuer":"https://oidc.eks.eu-west-1.amazonaws.com/id/EXAMPLE","audience":"janus","preset":"kubernetes","enabled":true}`
	if code := doAuthed(t, "POST", ts.URL+issuersPath, viewer, "", k8s, nil); code != 403 {
		t.Fatalf("viewer POST issuer: want 403, got %d", code)
	}
	for _, body := range []string{gh, k8s} {
		if code := doAuthed(t, "POST", ts.URL+issuersPath, owner, "", body, nil); code != 200 {
			t.Fatalf("owner POST issuer: %d", code)
		}
	}
	// Bad preset and missing issuer are rejected.
	if code := doAuthed(t, "POST", ts.URL+issuersPath, owner, "",
		`{"issuer":"https://x.example","audience":"janus","preset":"nope","enabled":true}`, nil); code != 400 {
		t.Fatalf("bad preset: want 400, got %d", code)
	}
	if code := doAuthed(t, "POST", ts.URL+issuersPath, owner, "",
		`{"issuer":"","audience":"janus","enabled":true}`, nil); code != 400 {
		t.Fatalf("empty issuer: want 400, got %d", code)
	}
	var list []issuerRow
	if code := doAuthed(t, "GET", ts.URL+issuersPath, owner, "", "", &list); code != 200 || len(list) != 2 {
		t.Fatalf("list issuers: %d len=%d", code, len(list))
	}
	// The legacy single-issuer PUT refuses to silently drop the other issuer.
	if code := doAuthed(t, "PUT", ts.URL+"/v1/sys/oidc/federation", owner, "", body, nil); code != 409 {
		t.Fatalf("legacy PUT with two issuers: want 409, got %d", code)
	}
	if code := doAuthed(t, "DELETE", ts.URL+issuersPath+"/"+list[0].ID, viewer, "", "", nil); code != 403 {
		t.Fatalf("viewer DELETE issuer: want 403, got %d", code)
	}
	if code := doAuthed(t, "DELETE", ts.URL+issuersPath+"/"+list[0].ID, owner, "", "", nil); code != 204 {
		t.Fatalf("owner DELETE issuer: %d", code)
	}
	if code := doAuthed(t, "DELETE", ts.URL+issuersPath+"/"+list[0].ID, owner, "", "", nil); code != 404 {
		t.Fatalf("second DELETE issuer: want 404, got %d", code)
	}
}
