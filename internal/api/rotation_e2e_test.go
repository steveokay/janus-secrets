package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/steveokay/janus-secrets/internal/secrets"
)

// readAllString drains and closes resp.Body, returning it as a string.
func readAllString(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// decodeInto JSON-decodes a raw body string into out.
func decodeInto(body string, out any) error {
	return json.Unmarshal([]byte(body), out)
}

// TestRotationCRUDViaAPI drives the full policy lifecycle as admin: create a
// webhook policy (masked response must not echo the url/hmac_key sent),
// list by project, get by id, patch the interval, trigger an immediate
// rotation, delete, then confirm the policy is gone.
func TestRotationCRUDViaAPI(t *testing.T) {
	ts, srv, adminEmail, adminPass, cid := authStackFull(t)
	admin := login(t, ts.URL, adminEmail, adminPass)

	// A receiver for the webhook rotator so RotateNow can succeed hermetically.
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer hook.Close()

	// Seed the secret the policy will rotate, via the server's own wired
	// secrets service (same pattern as backup_e2e_test.go / drillStack).
	if _, err := srv.service.SetSecrets(context.Background(), cid, []secrets.SecretChange{
		{Key: "API_KEY", Value: []byte("secret-initial")},
	}, "seed", "test"); err != nil {
		t.Fatal(err)
	}

	const rawURL = "http://sensitive-webhook.invalid/rotate"
	const rawHMAC = "super-secret-hmac-key-should-not-leak"
	createBody := `{"config_id":"` + cid + `","secret_key":"API_KEY","type":"webhook","interval_seconds":3600,` +
		`"config":{"url":"` + hook.URL + `","hmac_key":"` + rawHMAC + `"}}`

	var created struct {
		ID              string `json:"id"`
		ProjectID       string `json:"project_id"`
		ConfigID        string `json:"config_id"`
		SecretKey       string `json:"secret_key"`
		Type            string `json:"type"`
		IntervalSeconds int64  `json:"interval_seconds"`
		Status          string `json:"status"`
	}
	req, err := http.NewRequest("POST", ts.URL+"/v1/rotation/policies", strings.NewReader(createBody))
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: admin})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	rawBody := readAllString(t, resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d %s", resp.StatusCode, rawBody)
	}
	if strings.Contains(rawBody, hook.URL) || strings.Contains(rawBody, rawHMAC) || strings.Contains(rawBody, "hmac_key") || strings.Contains(rawBody, "url") {
		t.Fatalf("create response leaked webhook secrets: %s", rawBody)
	}
	if err := decodeInto(rawBody, &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.ID == "" || created.SecretKey != "API_KEY" || created.Type != "webhook" || created.Status != "active" {
		t.Fatalf("created = %+v", created)
	}

	// List by project.
	var list struct {
		Policies []struct {
			ID string `json:"id"`
		} `json:"policies"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/rotation/policies?project_id="+created.ProjectID, admin, "", "", &list); code != http.StatusOK {
		t.Fatalf("list: %d", code)
	}
	found := false
	for _, p := range list.Policies {
		if p.ID == created.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("list missing created policy: %+v", list)
	}

	// Get by id.
	var got struct {
		ID              string `json:"id"`
		IntervalSeconds int64  `json:"interval_seconds"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/rotation/policies/"+created.ID, admin, "", "", &got); code != http.StatusOK || got.ID != created.ID {
		t.Fatalf("get: %d %+v", code, got)
	}

	// Patch interval.
	var updated struct {
		IntervalSeconds int64 `json:"interval_seconds"`
	}
	if code := doAuthed(t, "PATCH", ts.URL+"/v1/rotation/policies/"+created.ID, admin, "",
		`{"interval_seconds":7200}`, &updated); code != http.StatusOK || updated.IntervalSeconds != 7200 {
		t.Fatalf("patch: %d %+v", code, updated)
	}

	// Rotate now.
	var rotated struct {
		Rotated       bool `json:"rotated"`
		ConfigVersion int  `json:"config_version"`
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/rotation/policies/"+created.ID+"/rotate", admin, "", "", &rotated); code != http.StatusOK {
		t.Fatalf("rotate: %d", code)
	}
	if !rotated.Rotated || rotated.ConfigVersion <= 0 {
		t.Fatalf("rotate result = %+v", rotated)
	}

	// Delete.
	var del struct {
		Deleted bool `json:"deleted"`
	}
	if code := doAuthed(t, "DELETE", ts.URL+"/v1/rotation/policies/"+created.ID, admin, "", "", &del); code != http.StatusOK || !del.Deleted {
		t.Fatalf("delete: %d %+v", code, del)
	}

	// Get after delete -> 404 rotation_not_found.
	var env errEnvelope
	if code := doAuthed(t, "GET", ts.URL+"/v1/rotation/policies/"+created.ID, admin, "", "", &env); code != http.StatusNotFound || env.Error.Code != CodeRotationNotFound {
		t.Fatalf("get after delete: %d %+v", code, env)
	}
}

// TestRotationForbiddenForNonAdmin confirms rotation:manage is not granted to
// lower instance roles (viewer/developer) — only admin/owner.
func TestRotationForbiddenForNonAdmin(t *testing.T) {
	ts, _, adminEmail, adminPass, cid := authStackFull(t)
	admin := login(t, ts.URL, adminEmail, adminPass)

	_, devPass := makeUser(t, ts.URL, admin, "rotdev@corp.io", "developer")
	dev := login(t, ts.URL, "rotdev@corp.io", devPass)

	createBody := `{"config_id":"` + cid + `","secret_key":"SOME_KEY","type":"webhook","interval_seconds":3600,` +
		`"config":{"url":"https://example.invalid/rotate"}}`
	if code := doAuthed(t, "POST", ts.URL+"/v1/rotation/policies", dev, "", createBody, nil); code != http.StatusForbidden {
		t.Fatalf("developer create: want 403, got %d", code)
	}
}

// TestRotationMaskingHidesSecrets creates a postgres-type policy with a
// recognizable admin_dsn and asserts the raw create+get responses never
// contain the DSN or the admin_dsn field name.
func TestRotationMaskingHidesSecrets(t *testing.T) {
	ts, _, adminEmail, adminPass, cid := authStackFull(t)
	admin := login(t, ts.URL, adminEmail, adminPass)

	const rawDSN = "postgres://secretuser:secretpw@h:5432/db"
	createBody := `{"config_id":"` + cid + `","secret_key":"PG_PASSWORD","type":"postgres","interval_seconds":3600,` +
		`"config":{"admin_dsn":"` + rawDSN + `","role":"app"}}`

	req, err := http.NewRequest("POST", ts.URL+"/v1/rotation/policies", strings.NewReader(createBody))
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: admin})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	createRaw := readAllString(t, resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d %s", resp.StatusCode, createRaw)
	}
	if strings.Contains(createRaw, "secretpw") || strings.Contains(createRaw, "admin_dsn") {
		t.Fatalf("create response leaked postgres secret: %s", createRaw)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := decodeInto(createRaw, &created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	_, getRaw := rawGet(t, ts.URL+"/v1/rotation/policies/"+created.ID, admin)
	if strings.Contains(getRaw, "secretpw") || strings.Contains(getRaw, "admin_dsn") {
		t.Fatalf("get response leaked postgres secret: %s", getRaw)
	}
}
