package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/steveokay/janus-secrets/internal/secrets"
	"github.com/steveokay/janus-secrets/internal/store"
)

// TestPromoteRequestE2E exercises the full approval-workflow lifecycle over a
// dev->staging pipeline: a developer without secret:promote on the target
// files a request (create), an admin approves it (apply happens, new target
// version), the requester approving their own request is forbidden, a second
// approve on an already-decided request is a conflict, and reject/cancel
// happy paths work.
func TestPromoteRequestE2E(t *testing.T) {
	ts, srv, ownerEmail, ownerPassword, _ := authStackFull(t)
	ctx := context.Background()
	ownerCookie := login(t, ts.URL, ownerEmail, ownerPassword)

	p, err := srv.service.CreateProject(ctx, "promoreq", "Promote Request Project")
	if err != nil {
		t.Fatal(err)
	}
	dev, err := srv.service.CreateEnvironment(ctx, p.ID, "dev", "Dev")
	if err != nil {
		t.Fatal(err)
	}
	stg, err := srv.service.CreateEnvironment(ctx, p.ID, "staging", "Staging")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.NewPipelineRepo(srv.st).Set(ctx, p.ID, []string{dev.ID, stg.ID}); err != nil {
		t.Fatal(err)
	}
	devCfg, err := srv.service.CreateConfig(ctx, dev.ID, "root", nil)
	if err != nil {
		t.Fatal(err)
	}
	stgCfg, err := srv.service.CreateConfig(ctx, stg.ID, "root", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Seed dev's config: A and B.
	const canary = "SENTINEL-PROMOTE-REQUEST-4f8a1c"
	cv, err := srv.service.SetSecrets(ctx, devCfg.ID, []secrets.SecretChange{
		{Key: "A", Value: []byte(canary)},
		{Key: "B", Value: []byte("bval")},
	}, "seed", "test")
	if err != nil {
		t.Fatal(err)
	}
	srcVersion := cv.Version

	// A developer with source-side rights (developer on dev) but only viewer on
	// staging (no secret:promote on target) — can still FILE a request, since
	// request rights are scoped to the source.
	devUserID, devUserPassword, err := srv.auth.CreateUser(ctx, "promo-requester@corp.io")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.authz.Grant(ctx, store.RoleBindingInput{
		SubjectUserID: devUserID, ScopeLevel: "environment", EnvironmentID: &dev.ID, Role: "developer",
	}); err != nil {
		t.Fatal(err)
	}
	if err := srv.authz.Grant(ctx, store.RoleBindingInput{
		SubjectUserID: devUserID, ScopeLevel: "environment", EnvironmentID: &stg.ID, Role: "viewer",
	}); err != nil {
		t.Fatal(err)
	}
	devCookie := login(t, ts.URL, "promo-requester@corp.io", devUserPassword)

	// (a) developer WITHOUT target secret:promote files a request -> 201.
	var createResp struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	createBody := `{"from_config":"` + devCfg.ID + `","to_config":"` + stgCfg.ID + `","source_version":` +
		strconv.Itoa(srcVersion) + `,"selections":[{"key":"A","action":"set"},{"key":"B","action":"set"}],"note":"ship it"}`
	if code := doAuthed(t, "POST", ts.URL+"/v1/promote/requests", devCookie, "", createBody, &createResp); code != http.StatusCreated {
		t.Fatalf("create request: want 201, got %d", code)
	}
	if createResp.ID == "" || createResp.Status != "pending" {
		t.Fatalf("create request: want id+pending, got %+v", createResp)
	}
	reqID := createResp.ID

	// GET by id: visible to the requester.
	var getResp struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Diff   struct {
			Entries []struct {
				Key    string `json:"key"`
				Status string `json:"status"`
			} `json:"entries"`
		} `json:"diff"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/promote/requests/"+reqID, devCookie, "", "", &getResp); code != 200 {
		t.Fatalf("requester get: want 200, got %d", code)
	}
	if len(getResp.Diff.Entries) == 0 {
		t.Fatalf("requester get: want diff entries, got %+v", getResp)
	}

	// A user with no relation to this request (neither requester nor approver)
	// cannot GET it.
	strangerID, strangerPassword, err := srv.auth.CreateUser(ctx, "promo-stranger@corp.io")
	if err != nil {
		t.Fatal(err)
	}
	_ = strangerID
	strangerCookie := login(t, ts.URL, "promo-stranger@corp.io", strangerPassword)
	if code := doAuthed(t, "GET", ts.URL+"/v1/promote/requests/"+reqID, strangerCookie, "", "", nil); code != http.StatusForbidden {
		t.Fatalf("stranger get: want 403, got %d", code)
	}

	// (c) the requester approving their own request -> 403.
	if code := doAuthed(t, "POST", ts.URL+"/v1/promote/requests/"+reqID+"/approve", devCookie, "", "", nil); code != http.StatusForbidden {
		t.Fatalf("self-approve: want 403, got %d", code)
	}

	// (b) an admin/owner (different user, has target secret:promote) approves -> 200.
	var approveResp struct {
		TargetVersion int      `json:"target_version"`
		Applied       []string `json:"applied"`
		Skipped       []string `json:"skipped"`
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/promote/requests/"+reqID+"/approve", ownerCookie, "", "", &approveResp); code != 200 {
		t.Fatalf("owner approve: want 200, got %d", code)
	}
	if len(approveResp.Applied) != 2 {
		t.Fatalf("owner approve: want 2 applied, got %+v", approveResp.Applied)
	}

	// Target config gained a new version; request status = applied.
	var afterApprove struct {
		Status                string `json:"status"`
		AppliedTargetVersion  int    `json:"applied_target_version"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/promote/requests/"+reqID, ownerCookie, "", "", &afterApprove); code != 200 {
		t.Fatalf("get after approve: want 200, got %d", code)
	}
	if afterApprove.Status != "applied" {
		t.Fatalf("get after approve: want status applied, got %q", afterApprove.Status)
	}
	if afterApprove.AppliedTargetVersion != approveResp.TargetVersion {
		t.Fatalf("get after approve: applied_target_version mismatch: %d vs %d", afterApprove.AppliedTargetVersion, approveResp.TargetVersion)
	}

	// Verify A was actually promoted to staging with the sentinel value.
	var reveal struct {
		Value string `json:"value"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/configs/"+stgCfg.ID+"/secrets/A", ownerCookie, "", "", &reveal); code != 200 {
		t.Fatalf("staging reveal A: want 200, got %d", code)
	}
	if reveal.Value != canary {
		t.Fatalf("staging reveal A: want sentinel, got %q", reveal.Value)
	}

	// (d) a second approve -> 409.
	if code := doAuthed(t, "POST", ts.URL+"/v1/promote/requests/"+reqID+"/approve", ownerCookie, "", "", nil); code != http.StatusConflict {
		t.Fatalf("second approve: want 409, got %d", code)
	}

	// --- (e) reject happy path ---
	cv2, err := srv.service.SetSecrets(ctx, devCfg.ID, []secrets.SecretChange{
		{Key: "B", Value: []byte("bval2")},
	}, "reseed", "test")
	if err != nil {
		t.Fatal(err)
	}
	var rejectCreate struct {
		ID string `json:"id"`
	}
	rejectBody := `{"from_config":"` + devCfg.ID + `","to_config":"` + stgCfg.ID + `","source_version":` +
		strconv.Itoa(cv2.Version) + `,"selections":[{"key":"B","action":"set"}]}`
	if code := doAuthed(t, "POST", ts.URL+"/v1/promote/requests", devCookie, "", rejectBody, &rejectCreate); code != http.StatusCreated {
		t.Fatalf("create reject-target request: want 201, got %d", code)
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/promote/requests/"+rejectCreate.ID+"/reject", ownerCookie, "", `{"note":"not yet"}`, nil); code != 200 {
		t.Fatalf("reject: want 200, got %d", code)
	}
	var rejected struct {
		Status string `json:"status"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/promote/requests/"+rejectCreate.ID, ownerCookie, "", "", &rejected); code != 200 {
		t.Fatalf("get after reject: want 200, got %d", code)
	}
	if rejected.Status != "rejected" {
		t.Fatalf("get after reject: want status rejected, got %q", rejected.Status)
	}
	// Rejecting again -> 409.
	if code := doAuthed(t, "POST", ts.URL+"/v1/promote/requests/"+rejectCreate.ID+"/reject", ownerCookie, "", `{}`, nil); code != http.StatusConflict {
		t.Fatalf("re-reject: want 409, got %d", code)
	}

	// --- (e) cancel happy path ---
	var cancelCreate struct {
		ID string `json:"id"`
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/promote/requests", devCookie, "", rejectBody, &cancelCreate); code != http.StatusCreated {
		t.Fatalf("create cancel-target request: want 201, got %d", code)
	}
	// A non-requester cannot cancel.
	if code := doAuthed(t, "POST", ts.URL+"/v1/promote/requests/"+cancelCreate.ID+"/cancel", ownerCookie, "", "", nil); code != http.StatusForbidden {
		t.Fatalf("non-requester cancel: want 403, got %d", code)
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/promote/requests/"+cancelCreate.ID+"/cancel", devCookie, "", "", nil); code != 200 {
		t.Fatalf("requester cancel: want 200, got %d", code)
	}
	var cancelled struct {
		Status string `json:"status"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/promote/requests/"+cancelCreate.ID, devCookie, "", "", &cancelled); code != 200 {
		t.Fatalf("get after cancel: want 200, got %d", code)
	}
	if cancelled.Status != "cancelled" {
		t.Fatalf("get after cancel: want status cancelled, got %q", cancelled.Status)
	}
	// Cancelling again -> 409.
	if code := doAuthed(t, "POST", ts.URL+"/v1/promote/requests/"+cancelCreate.ID+"/cancel", devCookie, "", "", nil); code != http.StatusConflict {
		t.Fatalf("re-cancel: want 409, got %d", code)
	}

	// --- list: mine (requester) sees their own requests ---
	var mineList struct {
		Requests []struct {
			ID string `json:"id"`
		} `json:"requests"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/promote/requests?project="+p.ID+"&mine=true", devCookie, "", "", &mineList); code != 200 {
		t.Fatalf("mine list: want 200, got %d", code)
	}
	if len(mineList.Requests) < 3 {
		t.Fatalf("mine list: want at least 3 requests, got %d", len(mineList.Requests))
	}

	// --- list: project-wide as owner (approver) sees all ---
	var projList struct {
		Requests []struct {
			ID string `json:"id"`
		} `json:"requests"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/promote/requests?project="+p.ID, ownerCookie, "", "", &projList); code != 200 {
		t.Fatalf("project list: want 200, got %d", code)
	}
	if len(projList.Requests) < 3 {
		t.Fatalf("project list: want at least 3 requests, got %d", len(projList.Requests))
	}

	// --- list: authorization is per-item (mirrors handleTokenList /
	// handleTrashList), so a caller with no visible requests gets an empty
	// list rather than a 403. A stranger with NO grant at all, and a viewer
	// who is neither requester nor an approver, both see none of THESE
	// requests. ---
	if code := doAuthed(t, "GET", ts.URL+"/v1/promote/requests?project="+p.ID, strangerCookie, "", "", &struct {
		Requests []struct {
			ID string `json:"id"`
		} `json:"requests"`
	}{}); code != 200 {
		t.Fatalf("stranger (no grant) project list: want 200 (empty), got %d", code)
	}

	viewerID, viewerPassword, err := srv.auth.CreateUser(ctx, "promo-list-viewer@corp.io")
	if err != nil {
		t.Fatal(err)
	}
	_ = viewerID
	if err := srv.authz.Grant(ctx, store.RoleBindingInput{
		SubjectUserID: viewerID, ScopeLevel: "project", ProjectID: &p.ID, Role: "viewer",
	}); err != nil {
		t.Fatal(err)
	}
	viewerCookie := login(t, ts.URL, "promo-list-viewer@corp.io", viewerPassword)
	var viewerList struct {
		Requests []struct {
			ID string `json:"id"`
		} `json:"requests"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/promote/requests?project="+p.ID, viewerCookie, "", "", &viewerList); code != 200 {
		t.Fatalf("viewer list: want 200, got %d", code)
	}
	for _, req := range viewerList.Requests {
		if req.ID == reqID || req.ID == rejectCreate.ID || req.ID == cancelCreate.ID {
			t.Fatalf("viewer list: unexpectedly saw request %s", req.ID)
		}
	}

	// --- value-safety: the sentinel value must never appear in any request
	// endpoint response body nor in the audit export. ---
	bodies := []string{}
	var raw string
	code, raw := rawGet(t, ts.URL+"/v1/promote/requests/"+reqID, ownerCookie)
	if code != 200 {
		t.Fatalf("raw get request: want 200, got %d", code)
	}
	bodies = append(bodies, raw)
	_, raw = rawGet(t, ts.URL+"/v1/promote/requests?project="+p.ID, ownerCookie)
	bodies = append(bodies, raw)
	_, raw = rawGet(t, ts.URL+"/v1/audit/export?format=jsonl", ownerCookie)
	bodies = append(bodies, raw)
	for _, b := range bodies {
		if strings.Contains(b, canary) {
			t.Fatalf("secret value leaked into a promotion-request or audit-export response: %s", b)
		}
	}
}
