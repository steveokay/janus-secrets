package api

import "testing"

func TestOIDCFederationBindings(t *testing.T) {
	ts, srv, adminEmail, adminPassword, configID := authStackFull(t)
	ctx := t.Context()
	owner := login(t, ts.URL, adminEmail, adminPassword)

	vid, vpw, err := srv.auth.CreateUser(ctx, "v@example.com")
	if err != nil {
		t.Fatal(err)
	}
	doAuthed(t, "PUT", ts.URL+"/v1/instance/members/"+vid, owner, "", `{"role":"viewer"}`, nil)
	viewer := login(t, ts.URL, "v@example.com", vpw)

	create := `{"name":"prod","match_claims":{"repository":"org/app"},"scope_kind":"config","scope_id":"` + configID + `","access":"read","ttl_seconds":900,"enabled":true}`
	// Non-owner denied.
	if code := doAuthed(t, "POST", ts.URL+"/v1/sys/oidc/federation/bindings", viewer, "", create, nil); code != 403 {
		t.Fatalf("viewer POST: want 403, got %d", code)
	}
	// Missing repository → 400.
	bad := `{"name":"bad","match_claims":{"environment":"prod"},"scope_kind":"config","scope_id":"` + configID + `","access":"read","ttl_seconds":900,"enabled":true}`
	if code := doAuthed(t, "POST", ts.URL+"/v1/sys/oidc/federation/bindings", owner, "", bad, nil); code != 400 {
		t.Fatalf("missing repository: want 400, got %d", code)
	}
	// Owner create → 200, returns id.
	var made struct {
		ID string `json:"id"`
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/sys/oidc/federation/bindings", owner, "", create, &made); code != 200 || made.ID == "" {
		t.Fatalf("owner POST: %d id=%q", code, made.ID)
	}
	// List shows it.
	var list []map[string]any
	if code := doAuthed(t, "GET", ts.URL+"/v1/sys/oidc/federation/bindings", owner, "", "", &list); code != 200 || len(list) != 1 {
		t.Fatalf("list: %d len=%d", code, len(list))
	}
	// Delete.
	if code := doAuthed(t, "DELETE", ts.URL+"/v1/sys/oidc/federation/bindings/"+made.ID, owner, "", "", nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
}
