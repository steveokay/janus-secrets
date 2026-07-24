package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/steveokay/janus-secrets/internal/secrets"
	"github.com/steveokay/janus-secrets/internal/store"
)

// TestEditRequestE2E exercises the protected-config (four-eyes) edit-request
// flow end to end: toggling require_approval, a protected save becoming a
// pending edit request (no commit), self-approval rejected, a DIFFERENT user
// approving (commit + applied), reject/cancel, and that an unprotected config
// still commits directly.
func TestEditRequestE2E(t *testing.T) {
	ts, srv, ownerEmail, ownerPassword, _ := authStackFull(t)
	ctx := context.Background()
	ownerCookie := login(t, ts.URL, ownerEmail, ownerPassword)

	p, err := srv.service.CreateProject(ctx, "protcfg", "Protected Config Project")
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

	// A second admin who can both toggle the flag and approve (secret:write).
	approverID, approverPass, err := srv.auth.CreateUser(ctx, "approver@corp.io")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.authz.Grant(ctx, store.RoleBindingInput{
		SubjectUserID: approverID, ScopeLevel: "project", ProjectID: &p.ID, Role: "admin",
	}); err != nil {
		t.Fatal(err)
	}
	approverCookie := login(t, ts.URL, "approver@corp.io", approverPass)

	// --- Unprotected save commits directly (200 with a version) ---
	var vresp struct {
		Version int `json:"version"`
	}
	if code := doAuthed(t, "PUT", ts.URL+"/v1/configs/"+cfg.ID+"/secrets", ownerCookie, "",
		`{"message":"seed","changes":[{"key":"A","value":"aval"}]}`, &vresp); code != 200 {
		t.Fatalf("unprotected save: want 200, got %d", code)
	}
	if vresp.Version == 0 {
		t.Fatalf("unprotected save: want a committed version, got %d", vresp.Version)
	}

	// --- Toggle require_approval on (admin+) ---
	if code := doAuthed(t, "PUT", ts.URL+"/v1/configs/"+cfg.ID+"/require-approval", ownerCookie, "",
		`{"enabled":true}`, nil); code != 200 {
		t.Fatalf("enable require_approval: want 200, got %d", code)
	}
	// Config view reflects the flag.
	var cview struct {
		RequireApproval bool `json:"require_approval"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/configs/"+cfg.ID, ownerCookie, "", "", &cview); code != 200 {
		t.Fatalf("config get: want 200, got %d", code)
	}
	if !cview.RequireApproval {
		t.Fatalf("config get: want require_approval=true")
	}

	// --- Protected save becomes a pending edit request (202, no commit) ---
	var er struct {
		ID     string   `json:"edit_request_id"`
		Status string   `json:"status"`
		Keys   []string `json:"keys"`
	}
	if code := doAuthed(t, "PUT", ts.URL+"/v1/configs/"+cfg.ID+"/secrets", ownerCookie, "",
		`{"message":"bump B","changes":[{"key":"B","value":"proposed-b"}]}`, &er); code != http.StatusAccepted {
		t.Fatalf("protected save: want 202, got %d", code)
	}
	if er.ID == "" || er.Status != "pending" {
		t.Fatalf("protected save: want pending request id, got %+v", er)
	}
	// B is NOT committed yet.
	if code := doAuthed(t, "GET", ts.URL+"/v1/configs/"+cfg.ID+"/secrets/B", ownerCookie, "", "", nil); code == 200 {
		t.Fatalf("B should not be committed yet, but reveal returned 200")
	}

	// The proposed VALUE never appears in the persisted row (envelope-encrypted).
	raw, err := store.NewConfigEditRequestRepo(srv.st).Get(ctx, er.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, blob := range [][]byte{raw.ProposedCiphertext, raw.WrappedDEK, raw.Nonce} {
		if containsSub(blob, []byte("proposed-b")) {
			t.Fatalf("proposed value leaked in edit request row (plaintext at rest)")
		}
	}
	// And the changed keys are recorded (value-free).
	if len(raw.ChangedKeys) != 1 || raw.ChangedKeys[0] != "B" {
		t.Fatalf("changed_keys: want [B], got %+v", raw.ChangedKeys)
	}

	// --- Self-approval is rejected (four-eyes) ---
	if code := doAuthed(t, "POST", ts.URL+"/v1/configs/"+cfg.ID+"/edit-requests/"+er.ID+"/approve", ownerCookie, "", "", nil); code != http.StatusForbidden {
		t.Fatalf("self-approval: want 403, got %d", code)
	}

	// --- A DIFFERENT user approves -> commit + applied ---
	var appResp struct {
		Version int      `json:"version"`
		Keys    []string `json:"keys"`
		Status  string   `json:"status"`
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/configs/"+cfg.ID+"/edit-requests/"+er.ID+"/approve", approverCookie, "", "", &appResp); code != 200 {
		t.Fatalf("approve by different user: want 200, got %d", code)
	}
	if appResp.Status != "applied" || appResp.Version == 0 {
		t.Fatalf("approve: want applied with a version, got %+v", appResp)
	}
	// Now B is committed with the proposed value.
	var reveal struct {
		Value string `json:"value"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/configs/"+cfg.ID+"/secrets/B", ownerCookie, "", "", &reveal); code != 200 {
		t.Fatalf("reveal B after approval: want 200, got %d", code)
	}
	if reveal.Value != "proposed-b" {
		t.Fatalf("reveal B: want proposed-b, got %q", reveal.Value)
	}

	// --- Re-approving an already-applied request -> 409 ---
	if code := doAuthed(t, "POST", ts.URL+"/v1/configs/"+cfg.ID+"/edit-requests/"+er.ID+"/approve", approverCookie, "", "", nil); code != http.StatusConflict {
		t.Fatalf("re-approve applied request: want 409, got %d", code)
	}

	// --- Reject flow: new request, rejected by a different user ---
	var er2 struct {
		ID string `json:"edit_request_id"`
	}
	if code := doAuthed(t, "PUT", ts.URL+"/v1/configs/"+cfg.ID+"/secrets", ownerCookie, "",
		`{"message":"C","changes":[{"key":"C","value":"cval"}]}`, &er2); code != http.StatusAccepted {
		t.Fatalf("second protected save: want 202, got %d", code)
	}
	// Requester cannot reject their own.
	if code := doAuthed(t, "POST", ts.URL+"/v1/configs/"+cfg.ID+"/edit-requests/"+er2.ID+"/reject", ownerCookie, "", "{}", nil); code != http.StatusForbidden {
		t.Fatalf("self-reject: want 403, got %d", code)
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/configs/"+cfg.ID+"/edit-requests/"+er2.ID+"/reject", approverCookie, "", "{}", nil); code != 200 {
		t.Fatalf("reject by different user: want 200, got %d", code)
	}
	// C never committed.
	if code := doAuthed(t, "GET", ts.URL+"/v1/configs/"+cfg.ID+"/secrets/C", ownerCookie, "", "", nil); code == 200 {
		t.Fatalf("C should not be committed after reject")
	}

	// --- Cancel flow: requester cancels their own pending request ---
	var er3 struct {
		ID string `json:"edit_request_id"`
	}
	if code := doAuthed(t, "PUT", ts.URL+"/v1/configs/"+cfg.ID+"/secrets", ownerCookie, "",
		`{"message":"D","changes":[{"key":"D","value":"dval"}]}`, &er3); code != http.StatusAccepted {
		t.Fatalf("third protected save: want 202, got %d", code)
	}
	// A non-requester cannot cancel.
	if code := doAuthed(t, "DELETE", ts.URL+"/v1/configs/"+cfg.ID+"/edit-requests/"+er3.ID, approverCookie, "", "", nil); code != http.StatusForbidden {
		t.Fatalf("non-requester cancel: want 403, got %d", code)
	}
	if code := doAuthed(t, "DELETE", ts.URL+"/v1/configs/"+cfg.ID+"/edit-requests/"+er3.ID, ownerCookie, "", "", nil); code != 200 {
		t.Fatalf("requester cancel: want 200, got %d", code)
	}

	// --- List shows the requests value-free (key names only) ---
	var list struct {
		Requests []struct {
			ID     string   `json:"id"`
			Status string   `json:"status"`
			Keys   []string `json:"keys"`
		} `json:"requests"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/configs/"+cfg.ID+"/edit-requests", ownerCookie, "", "", &list); code != 200 {
		t.Fatalf("list edit requests: want 200, got %d", code)
	}
	if len(list.Requests) < 3 {
		t.Fatalf("list: want >=3 requests, got %d", len(list.Requests))
	}

	// --- Toggle off; saves commit directly again ---
	if code := doAuthed(t, "PUT", ts.URL+"/v1/configs/"+cfg.ID+"/require-approval", ownerCookie, "",
		`{"enabled":false}`, nil); code != 200 {
		t.Fatalf("disable require_approval: want 200, got %d", code)
	}
	if code := doAuthed(t, "PUT", ts.URL+"/v1/configs/"+cfg.ID+"/secrets", ownerCookie, "",
		`{"message":"E","changes":[{"key":"E","value":"eval"}]}`, &vresp); code != 200 {
		t.Fatalf("save after disabling protection: want 200, got %d", code)
	}
}

// containsSub reports whether b contains sub.
func containsSub(b, sub []byte) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(b); i++ {
		match := true
		for j := range sub {
			if b[i+j] != sub[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// TestEditRequestRoundTripCrypto verifies the proposed changes envelope round
// trips (encrypt then decrypt yields the original) directly through the secrets
// service, independent of the HTTP layer.
func TestEditRequestRoundTripCrypto(t *testing.T) {
	_, srv, _, _, _ := authStackFull(t)
	ctx := context.Background()

	p, err := srv.service.CreateProject(ctx, "cryptort", "Crypto RT")
	if err != nil {
		t.Fatal(err)
	}
	env, err := srv.service.CreateEnvironment(ctx, p.ID, "prod", "Prod")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := srv.service.CreateConfig(ctx, env.ID, "root", nil)
	if err != nil {
		t.Fatal(err)
	}

	plain := []byte(`[{"key":"K","value":"topsecretvalue"}]`)
	blob, err := srv.service.EncryptConfigBlob(ctx, cfg.ID, append([]byte(nil), plain...))
	if err != nil {
		t.Fatal(err)
	}
	if containsSub(blob.Ciphertext, []byte("topsecretvalue")) {
		t.Fatalf("plaintext leaked in ciphertext")
	}
	back, err := srv.service.DecryptConfigBlob(ctx, cfg.ID, secrets.EditRequestBlob{
		Ciphertext: blob.Ciphertext, WrappedDEK: blob.WrappedDEK, Nonce: blob.Nonce, DEKKeyVersion: blob.DEKKeyVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(back) != string(plain) {
		t.Fatalf("round trip mismatch: want %q, got %q", plain, back)
	}
}
