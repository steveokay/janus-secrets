package api

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	"github.com/steveokay/janus-secrets/internal/secrets"
	"github.com/steveokay/janus-secrets/internal/store"
)

// TestRollbackProtectedConfigRoutesThroughApproval verifies that rolling back a
// PROTECTED config (require_approval) does NOT commit directly — it must become a
// pending edit request that a DIFFERENT user approves (four-eyes), and the
// approved result reproduces the target version's exact state (values restored,
// keys added after the target dropped). Regression for the rollback bypass of
// the require-approval control.
func TestRollbackProtectedConfigRoutesThroughApproval(t *testing.T) {
	ts, srv, ownerEmail, ownerPassword, _ := authStackFull(t)
	ctx := context.Background()
	ownerCookie := login(t, ts.URL, ownerEmail, ownerPassword)

	p, err := srv.service.CreateProject(ctx, "rbprotect", "Rollback Protect")
	if err != nil {
		t.Fatal(err)
	}
	prod, err := srv.service.CreateEnvironment(ctx, p.ID, "prod", "Prod")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := srv.service.CreateConfig(ctx, prod.ID, "root", nil)
	if err != nil {
		t.Fatal(err)
	}

	// v1: A=1. v2: A=2 and a new key C.
	if _, err := srv.service.SetSecrets(ctx, cfg.ID, []secrets.SecretChange{{Key: "A", Value: []byte("1")}}, "v1", "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.service.SetSecrets(ctx, cfg.ID, []secrets.SecretChange{
		{Key: "A", Value: []byte("2")}, {Key: "C", Value: []byte("cval")},
	}, "v2", "test"); err != nil {
		t.Fatal(err)
	}

	// A second admin (project admin => secret:write) who can approve.
	approverID, approverPass, err := srv.auth.CreateUser(ctx, "rb-approver@corp.io")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.authz.Grant(ctx, store.RoleBindingInput{
		SubjectUserID: approverID, ScopeLevel: "project", ProjectID: &p.ID, Role: "admin",
	}); err != nil {
		t.Fatal(err)
	}
	approverCookie := login(t, ts.URL, "rb-approver@corp.io", approverPass)

	// Enable protection on the config.
	if code := doAuthed(t, "PUT", ts.URL+"/v1/configs/"+cfg.ID+"/require-approval", ownerCookie, "",
		`{"enabled":true}`, nil); code != 200 {
		t.Fatalf("enable require_approval: want 200, got %d", code)
	}

	// Rollback to v1 on a PROTECTED config -> 202 pending edit request (NO commit).
	var er struct {
		ID     string   `json:"edit_request_id"`
		Status string   `json:"status"`
		Keys   []string `json:"keys"`
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/configs/"+cfg.ID+"/rollback", ownerCookie, "",
		`{"target_version":1}`, &er); code != http.StatusAccepted {
		t.Fatalf("protected rollback: want 202, got %d", code)
	}
	if er.ID == "" || er.Status != "pending" {
		t.Fatalf("protected rollback: want a pending request, got %+v", er)
	}

	// Nothing committed yet: A is still 2 and C still present.
	var reveal struct {
		Value string `json:"value"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/configs/"+cfg.ID+"/secrets/A", ownerCookie, "", "", &reveal); code != 200 || reveal.Value != "2" {
		t.Fatalf("A before approval: want 2 (rollback not committed), got code=%d val=%q", code, reveal.Value)
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/configs/"+cfg.ID+"/secrets/C", ownerCookie, "", "", nil); code != 200 {
		t.Fatalf("C before approval: want present (200), got %d", code)
	}

	// The rolled-back plaintext must not leak in the persisted request row.
	raw, err := store.NewConfigEditRequestRepo(srv.st).Get(ctx, er.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, blob := range [][]byte{raw.ProposedCiphertext, raw.WrappedDEK, raw.Nonce} {
		if containsSub(blob, []byte("cval")) {
			t.Fatalf("rolled-back value leaked in edit request row (plaintext at rest)")
		}
	}

	// A DIFFERENT user approves -> commit; state now equals v1 (A=1, C gone).
	if code := doAuthed(t, "POST", ts.URL+"/v1/configs/"+cfg.ID+"/edit-requests/"+er.ID+"/approve", approverCookie, "", "", nil); code != 200 {
		t.Fatalf("approve rollback request: want 200, got %d", code)
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/configs/"+cfg.ID+"/secrets/A", ownerCookie, "", "", &reveal); code != 200 || reveal.Value != "1" {
		t.Fatalf("A after approval: want 1, got code=%d val=%q", code, reveal.Value)
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/configs/"+cfg.ID+"/secrets/C", ownerCookie, "", "", nil); code == 200 {
		t.Fatalf("C after rollback-approval: want removed (non-200), got 200")
	}
}

// TestPromoteProtectedTargetRoutesThroughApproval verifies that promoting into a
// PROTECTED existing target config does NOT commit directly — it becomes a
// pending edit request approved by a DIFFERENT user (four-eyes). Regression for
// the promote-apply bypass of the require-approval control.
func TestPromoteProtectedTargetRoutesThroughApproval(t *testing.T) {
	ts, srv, ownerEmail, ownerPassword, _ := authStackFull(t)
	ctx := context.Background()
	ownerCookie := login(t, ts.URL, ownerEmail, ownerPassword)

	p, err := srv.service.CreateProject(ctx, "promoprotect", "Promote Protect")
	if err != nil {
		t.Fatal(err)
	}
	dev, err := srv.service.CreateEnvironment(ctx, p.ID, "dev", "Dev")
	if err != nil {
		t.Fatal(err)
	}
	prod, err := srv.service.CreateEnvironment(ctx, p.ID, "prod", "Prod")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.NewPipelineRepo(srv.st).Set(ctx, p.ID, []string{dev.ID, prod.ID}); err != nil {
		t.Fatal(err)
	}
	devCfg, err := srv.service.CreateConfig(ctx, dev.ID, "root", nil)
	if err != nil {
		t.Fatal(err)
	}
	prodCfg, err := srv.service.CreateConfig(ctx, prod.ID, "root", nil)
	if err != nil {
		t.Fatal(err)
	}
	cv, err := srv.service.SetSecrets(ctx, devCfg.ID, []secrets.SecretChange{{Key: "B", Value: []byte("bval")}}, "seed", "test")
	if err != nil {
		t.Fatal(err)
	}

	approverID, approverPass, err := srv.auth.CreateUser(ctx, "promo-approver@corp.io")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.authz.Grant(ctx, store.RoleBindingInput{
		SubjectUserID: approverID, ScopeLevel: "project", ProjectID: &p.ID, Role: "admin",
	}); err != nil {
		t.Fatal(err)
	}
	approverCookie := login(t, ts.URL, "promo-approver@corp.io", approverPass)

	// Protect the prod (target) config.
	if code := doAuthed(t, "PUT", ts.URL+"/v1/configs/"+prodCfg.ID+"/require-approval", ownerCookie, "",
		`{"enabled":true}`, nil); code != 200 {
		t.Fatalf("enable require_approval on target: want 200, got %d", code)
	}

	// Promote B into the PROTECTED target -> 202 pending edit request (NO commit).
	var er struct {
		ID     string   `json:"edit_request_id"`
		Status string   `json:"status"`
		Keys   []string `json:"keys"`
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/promote", ownerCookie, "",
		`{"from_config":"`+devCfg.ID+`","to_config":"`+prodCfg.ID+`","source_version":`+strconv.Itoa(cv.Version)+`,"selections":[{"key":"B","action":"set"}]}`, &er); code != http.StatusAccepted {
		t.Fatalf("promote into protected target: want 202, got %d", code)
	}
	if er.ID == "" || er.Status != "pending" {
		t.Fatalf("promote into protected target: want a pending request, got %+v", er)
	}

	// B is NOT committed to the target yet.
	if code := doAuthed(t, "GET", ts.URL+"/v1/configs/"+prodCfg.ID+"/secrets/B", ownerCookie, "", "", nil); code == 200 {
		t.Fatalf("B should not be committed to the protected target before approval")
	}

	// The promoted plaintext must not leak in the persisted request row.
	raw, err := store.NewConfigEditRequestRepo(srv.st).Get(ctx, er.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, blob := range [][]byte{raw.ProposedCiphertext, raw.WrappedDEK, raw.Nonce} {
		if containsSub(blob, []byte("bval")) {
			t.Fatalf("promoted value leaked in edit request row (plaintext at rest)")
		}
	}

	// A DIFFERENT user approves -> B is now committed to the target with its value.
	if code := doAuthed(t, "POST", ts.URL+"/v1/configs/"+prodCfg.ID+"/edit-requests/"+er.ID+"/approve", approverCookie, "", "", nil); code != 200 {
		t.Fatalf("approve promote request: want 200, got %d", code)
	}
	var reveal struct {
		Value string `json:"value"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/configs/"+prodCfg.ID+"/secrets/B", ownerCookie, "", "", &reveal); code != 200 || reveal.Value != "bval" {
		t.Fatalf("B after approval: want bval, got code=%d val=%q", code, reveal.Value)
	}
}
