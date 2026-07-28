package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/steveokay/janus-secrets/internal/secrets"
	"github.com/steveokay/janus-secrets/internal/store"
)

// TestAnnotationAPIE2E exercises the per-key annotation endpoint: setting an
// owner + note is a secret:write (developers may, viewers may not); the stored
// annotation surfaces in the masked-secrets response (secret:read). It never
// blocks anything and stores no secret value. It also asserts the annotation
// TEXT (owner/note) never lands in an audit row (value-free audit).
func TestAnnotationAPIE2E(t *testing.T) {
	ts, srv, _, _, cid := authStackFull(t)
	ctx := context.Background()

	// Seed two secrets on the config. The value is an obviously-fake low-entropy
	// fixture so a secret scanner does not flag it.
	if _, err := srv.service.SetSecrets(ctx, cid, []secrets.SecretChange{
		{Key: "DATABASE_URL", Value: []byte("pg-fixture-value")},
		{Key: "API_KEY", Value: []byte("api-fixture-value")},
	}, "seed", "root"); err != nil {
		t.Fatal(err)
	}

	cfgProjID := configProjectID(t, srv, cid)

	// A developer (secret:write) and a viewer (secret:read only).
	devID, devPassword, err := srv.auth.CreateUser(ctx, "an-dev@corp.io")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.authz.Grant(ctx, store.RoleBindingInput{
		SubjectUserID: devID, ScopeLevel: "project", ProjectID: &cfgProjID, Role: "developer",
	}); err != nil {
		t.Fatal(err)
	}
	devCookie := login(t, ts.URL, "an-dev@corp.io", devPassword)

	viewerID, viewerPassword, err := srv.auth.CreateUser(ctx, "an-viewer@corp.io")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.authz.Grant(ctx, store.RoleBindingInput{
		SubjectUserID: viewerID, ScopeLevel: "project", ProjectID: &cfgProjID, Role: "viewer",
	}); err != nil {
		t.Fatal(err)
	}
	viewerCookie := login(t, ts.URL, "an-viewer@corp.io", viewerPassword)

	const ownerText = "team-data-fixture"
	const noteText = "annotation-note-fixture-do-not-leak"

	// Viewer cannot set an annotation.
	if code := doAuthed(t, "PUT", ts.URL+"/v1/configs/"+cid+"/secrets/DATABASE_URL/annotation", viewerCookie, "",
		`{"note":"`+noteText+`"}`, nil); code != http.StatusForbidden {
		t.Fatalf("viewer annotation PUT: want 403, got %d", code)
	}

	// Developer sets a note (trimmed).
	var set struct {
		Key  string  `json:"key"`
		Note *string `json:"note"`
	}
	if code := doAuthed(t, "PUT", ts.URL+"/v1/configs/"+cid+"/secrets/DATABASE_URL/annotation", devCookie, "",
		`{"note":"  `+noteText+`  "}`, &set); code != 200 {
		t.Fatalf("developer annotation PUT: want 200, got %d", code)
	}
	if set.Note == nil || *set.Note != noteText {
		t.Fatalf("set echo = %+v", set)
	}

	// A note on a second key.
	if code := doAuthed(t, "PUT", ts.URL+"/v1/configs/"+cid+"/secrets/API_KEY/annotation", devCookie, "",
		`{"note":"third-party key"}`, nil); code != 200 {
		t.Fatalf("second note PUT: want 200, got %d", code)
	}

	// owner is REJECTED here, not ignored: it moved to the project, and
	// silently dropping it would leave the caller believing it was recorded.
	if code := doAuthed(t, "PUT", ts.URL+"/v1/configs/"+cid+"/secrets/API_KEY/annotation", devCookie, "",
		`{"owner":"`+ownerText+`","note":"x"}`, nil); code != http.StatusBadRequest {
		t.Fatalf("owner on a key annotation: want 400, got %d", code)
	}

	// Masked list surfaces the note (and no value, and no owner).
	var masked struct {
		Secrets map[string]struct {
			Owner *string `json:"owner"`
			Note  *string `json:"note"`
			Value *string `json:"value"`
		} `json:"secrets"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/configs/"+cid+"/secrets", viewerCookie, "", "", &masked); code != 200 {
		t.Fatalf("masked GET: want 200, got %d", code)
	}
	db := masked.Secrets["DATABASE_URL"]
	if db.Note == nil || *db.Note != noteText {
		t.Fatalf("DATABASE_URL masked annotation = %+v", db)
	}
	if db.Owner != nil {
		t.Fatalf("masked response must no longer carry a per-key owner, got %v", *db.Owner)
	}
	if db.Value != nil {
		t.Fatalf("masked response must not carry a secret value, got %v", *db.Value)
	}

	// Clearing (empty note) removes the whole annotation → nil in masked.
	if code := doAuthed(t, "PUT", ts.URL+"/v1/configs/"+cid+"/secrets/DATABASE_URL/annotation", devCookie, "",
		`{"note":""}`, nil); code != 200 {
		t.Fatalf("clear annotation PUT: want 200, got %d", code)
	}
	masked.Secrets = nil
	doAuthed(t, "GET", ts.URL+"/v1/configs/"+cid+"/secrets", viewerCookie, "", "", &masked)
	if db := masked.Secrets["DATABASE_URL"]; db.Note != nil {
		t.Fatalf("DATABASE_URL should be cleared, got %+v", db)
	}

	// Over-length note is a 400.
	if code := doAuthed(t, "PUT", ts.URL+"/v1/configs/"+cid+"/secrets/API_KEY/annotation", devCookie, "",
		`{"note":"`+strings.Repeat("x", 2100)+`"}`, nil); code != http.StatusBadRequest {
		t.Fatalf("over-length note PUT: want 400, got %d", code)
	}

	// Value-free audit: neither the annotation text nor any secret value may
	// appear in any audit row (action/resource/detail).
	var auditDump strings.Builder
	if err := store.NewAuditRepo(srv.st).Iterate(ctx, func(a store.AuditRow) error {
		auditDump.WriteString(a.Action + "|" + a.Resource + "|" + derefStr(a.Detail) + "\n")
		return nil
	}); err != nil {
		t.Fatalf("iterate audit: %v", err)
	}
	dump := auditDump.String()
	for _, banned := range []string{ownerText, noteText, "team-api-fixture", "pg-fixture-value", "api-fixture-value"} {
		if strings.Contains(dump, banned) {
			t.Fatalf("annotation/secret text %q leaked into an audit row", banned)
		}
	}
	// Sanity: the annotation set/clear events were recorded (by action + path).
	if !strings.Contains(dump, "secret.annotation.set") || !strings.Contains(dump, "secret.annotation.clear") {
		t.Fatalf("expected annotation set + clear audit actions, got:\n%s", dump)
	}
}
