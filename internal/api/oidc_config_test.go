package api

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestOIDCConfigRBACAndSecret(t *testing.T) {
	ts, srv, adminEmail, adminPassword, _ := authStackFull(t)
	ctx := context.Background()
	owner := login(t, ts.URL, adminEmail, adminPassword) // bootstrap admin == instance owner

	// A viewer user (instance-scoped viewer role) must NOT manage OIDC.
	vid, vpw, err := srv.auth.CreateUser(ctx, "viewer@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if code := doAuthed(t, "PUT", ts.URL+"/v1/instance/members/"+vid, owner, "", `{"role":"viewer"}`, nil); code != 204 {
		t.Fatalf("grant viewer: %d", code)
	}
	viewer := login(t, ts.URL, "viewer@example.com", vpw)

	body := `{"name":"default","issuer":"https://issuer.example","client_id":"cid","client_secret":"top-secret","redirect_url":"https://app/cb","enabled":true}`

	// Owner can configure.
	if code := doAuthed(t, "PUT", ts.URL+"/v1/sys/oidc", owner, "", body, nil); code != 200 {
		t.Fatalf("owner PUT: %d", code)
	}
	// Viewer cannot.
	var env errEnvelope
	if code := doAuthed(t, "PUT", ts.URL+"/v1/sys/oidc", viewer, "", body, &env); code != 403 {
		t.Fatalf("viewer PUT: want 403, got %d (%+v)", code, env)
	}
	// GET returns secret_set true and NEVER the secret.
	var raw map[string]any
	rec := doAuthedRaw(t, "GET", ts.URL+"/v1/sys/oidc", owner)
	if rec.status != 200 {
		t.Fatalf("owner GET: %d", rec.status)
	}
	if !strings.Contains(rec.body, `"secret_set":true`) || strings.Contains(rec.body, "top-secret") {
		t.Fatalf("GET leaked or missing secret_set: %s", rec.body)
	}
	_ = raw
	// DELETE by owner works.
	if code := doAuthed(t, "DELETE", ts.URL+"/v1/sys/oidc", owner, "", "", nil); code != 204 {
		t.Fatalf("owner DELETE: %d", code)
	}
}

// doAuthedRaw is a tiny helper returning the raw body string (to scan for a leaked secret).
func doAuthedRaw(t *testing.T, method, url, cookie string) struct {
	status int
	body   string
} {
	t.Helper()
	req, _ := http.NewRequest(method, url, nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b := new(bytes.Buffer)
	_, _ = b.ReadFrom(resp.Body)
	return struct {
		status int
		body   string
	}{resp.StatusCode, b.String()}
}
