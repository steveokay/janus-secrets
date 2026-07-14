package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/steveokay/janus-secrets/internal/store"
)

// TestKEKRotateE2E exercises the owner-only project-KEK rotate/rewrap/status
// endpoints: an owner may rotate/rewrap/status, a non-owner (project admin) is
// forbidden, and a sealed server refuses.
func TestKEKRotateE2E(t *testing.T) {
	ts, srv, ownerEmail, ownerPassword, _ := authStackFull(t)
	ctx := context.Background()
	ownerCookie := login(t, ts.URL, ownerEmail, ownerPassword)

	// A fresh project (KEK version 1) to rotate.
	p, err := srv.service.CreateProject(ctx, "kekproj", "KEK Project")
	if err != nil {
		t.Fatal(err)
	}

	// A non-owner: project-scoped admin on this project. admin lacks kek:manage.
	adminID, adminPassword, err := srv.auth.CreateUser(ctx, "kek-admin@corp.io")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.authz.Grant(ctx, store.RoleBindingInput{
		SubjectUserID: adminID, ScopeLevel: "project", ProjectID: &p.ID, Role: "admin",
	}); err != nil {
		t.Fatal(err)
	}
	adminCookie := login(t, ts.URL, "kek-admin@corp.io", adminPassword)

	// Non-owner rotate → 403.
	if code := doAuthed(t, "POST", ts.URL+"/v1/projects/"+p.ID+"/kek/rotate", adminCookie, "", "", nil); code != http.StatusForbidden {
		t.Fatalf("non-owner rotate: want 403, got %d", code)
	}
	// Non-owner status (a read gated on kek:manage) → 403.
	if code := doAuthed(t, "GET", ts.URL+"/v1/projects/"+p.ID+"/kek", adminCookie, "", "", nil); code != http.StatusForbidden {
		t.Fatalf("non-owner status: want 403, got %d", code)
	}

	// Owner rotate → 200, new version 2.
	var rot struct {
		KEKVersion int `json:"kek_version"`
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/projects/"+p.ID+"/kek/rotate", ownerCookie, "", "", &rot); code != 200 {
		t.Fatalf("owner rotate: want 200, got %d", code)
	}
	if rot.KEKVersion != 2 {
		t.Fatalf("kek_version: want 2, got %d", rot.KEKVersion)
	}

	// Owner status → current_version 2.
	var st struct {
		CurrentVersion int `json:"current_version"`
		Pending        []struct {
			Version  int `json:"version"`
			DEKCount int `json:"dek_count"`
		} `json:"pending"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/projects/"+p.ID+"/kek", ownerCookie, "", "", &st); code != 200 {
		t.Fatalf("owner status: want 200, got %d", code)
	}
	if st.CurrentVersion != 2 {
		t.Fatalf("current_version: want 2, got %d", st.CurrentVersion)
	}

	// Owner rewrap → 200, remaining 0.
	var rw struct {
		Rewrapped       int   `json:"rewrapped"`
		RetiredVersions []int `json:"retired_versions"`
		Remaining       int   `json:"remaining"`
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/projects/"+p.ID+"/kek/rewrap", ownerCookie, "", "", &rw); code != 200 {
		t.Fatalf("owner rewrap: want 200, got %d", code)
	}
	if rw.Remaining != 0 {
		t.Fatalf("remaining: want 0, got %d", rw.Remaining)
	}

	// Sealed server → 503 on rotate (global unseal gate).
	var sealResp struct{ Sealed bool }
	if code := doAuthed(t, "POST", ts.URL+"/v1/sys/seal", ownerCookie, "", "", &sealResp); code != 200 || !sealResp.Sealed {
		t.Fatalf("seal: %d %+v", code, sealResp)
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/projects/"+p.ID+"/kek/rotate", ownerCookie, "", "", nil); code != http.StatusServiceUnavailable {
		t.Fatalf("sealed rotate: want 503, got %d", code)
	}
}
