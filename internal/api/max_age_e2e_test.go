package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/steveokay/janus-secrets/internal/secrets"
	"github.com/steveokay/janus-secrets/internal/store"
)

// TestMaxAgeAPIE2E exercises the advisory max-age endpoints: setting a policy is
// a secret:write (developers may, viewers may not); reading policy + staleness is
// a secret:read. Staleness surfaces in the masked-secrets response and never
// blocks any operation.
func TestMaxAgeAPIE2E(t *testing.T) {
	ts, srv, _, _, cid := authStackFull(t)
	ctx := context.Background()

	// Seed two secrets on the config.
	if _, err := srv.service.SetSecrets(ctx, cid, []secrets.SecretChange{
		{Key: "DATABASE_URL", Value: []byte("pg://x")},
		{Key: "API_KEY", Value: []byte("k")},
	}, "seed", "root"); err != nil {
		t.Fatal(err)
	}

	cfgProjID := configProjectID(t, srv, cid)

	// A developer (secret:write) and a viewer (secret:read only).
	devID, devPassword, err := srv.auth.CreateUser(ctx, "ma-dev@corp.io")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.authz.Grant(ctx, store.RoleBindingInput{
		SubjectUserID: devID, ScopeLevel: "project", ProjectID: &cfgProjID, Role: "developer",
	}); err != nil {
		t.Fatal(err)
	}
	devCookie := login(t, ts.URL, "ma-dev@corp.io", devPassword)

	viewerID, viewerPassword, err := srv.auth.CreateUser(ctx, "ma-viewer@corp.io")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.authz.Grant(ctx, store.RoleBindingInput{
		SubjectUserID: viewerID, ScopeLevel: "project", ProjectID: &cfgProjID, Role: "viewer",
	}); err != nil {
		t.Fatal(err)
	}
	viewerCookie := login(t, ts.URL, "ma-viewer@corp.io", viewerPassword)

	// Viewer cannot set the config default.
	if code := doAuthed(t, "PUT", ts.URL+"/v1/configs/"+cid+"/max-age", viewerCookie, "",
		`{"max_age_seconds":3600}`, nil); code != http.StatusForbidden {
		t.Fatalf("viewer config max-age PUT: want 403, got %d", code)
	}

	// Developer sets a large config default (not stale) and a 1s per-key override
	// on DATABASE_URL (immediately stale).
	if code := doAuthed(t, "PUT", ts.URL+"/v1/configs/"+cid+"/max-age", devCookie, "",
		`{"max_age_seconds":1000000}`, nil); code != 200 {
		t.Fatalf("developer config max-age PUT: want 200, got %d", code)
	}
	if code := doAuthed(t, "PUT", ts.URL+"/v1/configs/"+cid+"/secrets/DATABASE_URL/max-age", devCookie, "",
		`{"max_age_seconds":1}`, nil); code != 200 {
		t.Fatalf("developer key max-age PUT: want 200, got %d", code)
	}

	// Rejects non-positive.
	if code := doAuthed(t, "PUT", ts.URL+"/v1/configs/"+cid+"/max-age", devCookie, "",
		`{"max_age_seconds":0}`, nil); code != http.StatusBadRequest {
		t.Fatalf("zero max-age PUT: want 400, got %d", code)
	}

	// Brief pause so the 1s per-key override deterministically trips (age must
	// exceed the override).
	time.Sleep(1200 * time.Millisecond)

	// Masked list reflects staleness + effective policy.
	var masked struct {
		Secrets map[string]struct {
			Stale         bool   `json:"stale"`
			MaxAgeSeconds *int64 `json:"max_age_seconds"`
		} `json:"secrets"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/configs/"+cid+"/secrets", viewerCookie, "", "", &masked); code != 200 {
		t.Fatalf("masked GET: want 200, got %d", code)
	}
	db := masked.Secrets["DATABASE_URL"]
	if db.MaxAgeSeconds == nil || *db.MaxAgeSeconds != 1 || !db.Stale {
		t.Fatalf("DATABASE_URL masked = %+v, want effective 1 + stale", db)
	}
	api := masked.Secrets["API_KEY"]
	if api.MaxAgeSeconds == nil || *api.MaxAgeSeconds != 1000000 || api.Stale {
		t.Fatalf("API_KEY masked = %+v, want effective 1000000 + not stale", api)
	}

	// Policy list shows the default under the empty-string key.
	var pol struct {
		Policies []struct {
			Key           string `json:"key"`
			MaxAgeSeconds int64  `json:"max_age_seconds"`
		} `json:"policies"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/configs/"+cid+"/max-age", viewerCookie, "", "", &pol); code != 200 {
		t.Fatalf("policy GET: want 200, got %d", code)
	}
	if len(pol.Policies) != 2 {
		t.Fatalf("policies = %+v, want 2", pol.Policies)
	}

	// Clearing the per-key override (null body) falls back to the default → fresh.
	if code := doAuthed(t, "PUT", ts.URL+"/v1/configs/"+cid+"/secrets/DATABASE_URL/max-age", devCookie, "",
		`{"max_age_seconds":null}`, nil); code != 200 {
		t.Fatalf("clear key max-age PUT: want 200, got %d", code)
	}
	masked.Secrets = nil
	doAuthed(t, "GET", ts.URL+"/v1/configs/"+cid+"/secrets", viewerCookie, "", "", &masked)
	if masked.Secrets["DATABASE_URL"].Stale {
		t.Fatalf("DATABASE_URL should be fresh after clearing override")
	}
}
