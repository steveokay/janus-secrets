package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/store"
)

// shamirMasterKeyStack boots the real stack on a shamir seal with the requested
// share/threshold split, initializes with an owner admin, unseals, and returns
// the httptest server, the *Server, the owner credentials, and the raw share
// strings (the operator's only copy) for the rekey ceremony.
func shamirMasterKeyStack(t *testing.T, shares, threshold int) (*httptest.Server, *Server, string, string, []string) {
	t.Helper()
	dsn := bootPostgres(t)
	ctx := context.Background()
	srv, st, err := Boot(ctx, BootConfig{DatabaseURL: dsn, SealType: crypto.SealTypeShamir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	var ir struct {
		Shares []string `json:"shares"`
		Admin  *struct{ Email, Password string } `json:"admin"`
	}
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/init",
		fmt.Sprintf(`{"shares":%d,"threshold":%d,"admin_email":"root@corp.io"}`, shares, threshold), &ir); code != 200 {
		t.Fatalf("init: %d", code)
	}
	if ir.Admin == nil || ir.Admin.Password == "" {
		t.Fatalf("admin credential missing: %+v", ir.Admin)
	}
	// Submit threshold shares to unseal.
	for i := 0; i < threshold; i++ {
		if code := doJSON(t, "POST", ts.URL+"/v1/sys/unseal",
			fmt.Sprintf(`{"share":%q}`, ir.Shares[i]), nil); code != 200 {
			t.Fatalf("unseal share %d: %d", i, code)
		}
	}
	if srv.keyring.Sealed() {
		t.Fatal("stack must be unsealed after threshold shares")
	}
	return ts, srv, ir.Admin.Email, ir.Admin.Password, ir.Shares
}

// kmsMasterKeyStack boots the real stack on a KMS seal (fake client), inits
// (auto-unseal + owner bootstrap), and returns the server + owner credentials.
func kmsMasterKeyStack(t *testing.T) (*httptest.Server, *Server, string, string) {
	t.Helper()
	dsn := bootPostgres(t)
	ctx := context.Background()
	factory := func(context.Context) (crypto.KMSClient, error) { return &fakeKMS{}, nil }
	srv, st, err := Boot(ctx, BootConfig{
		DatabaseURL: dsn, SealType: crypto.SealTypeAWSKMS, NewKMSClient: factory,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	var ir struct {
		Admin *struct{ Email, Password string } `json:"admin"`
	}
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/init",
		`{"admin_email":"root@corp.io"}`, &ir); code != 200 {
		t.Fatalf("kms init: %d", code)
	}
	if ir.Admin == nil || ir.Admin.Password == "" {
		t.Fatalf("kms admin credential missing: %+v", ir.Admin)
	}
	if srv.keyring.Sealed() {
		t.Fatal("kms init must auto-unseal")
	}
	return ts, srv, ir.Admin.Email, ir.Admin.Password
}

// grantProjectAdmin creates a fresh user with a project-scoped admin binding
// (which lacks sys:master-key) and returns its session cookie.
func grantProjectAdmin(t *testing.T, ts *httptest.Server, srv *Server, email string) string {
	t.Helper()
	ctx := context.Background()
	p, err := srv.service.CreateProject(ctx, "mkproj", "MK Project")
	if err != nil {
		t.Fatal(err)
	}
	adminID, adminPassword, err := srv.auth.CreateUser(ctx, email)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.authz.Grant(ctx, store.RoleBindingInput{
		SubjectUserID: adminID, ScopeLevel: "project", ProjectID: &p.ID, Role: "admin",
	}); err != nil {
		t.Fatal(err)
	}
	return login(t, ts.URL, email, adminPassword)
}

// TestMasterKeyOwnerOnlyE2E: a non-owner project admin is forbidden from the
// master-key surface (rotate, rekey/init, status), while the owner may read it.
func TestMasterKeyOwnerOnlyE2E(t *testing.T) {
	ts, srv, ownerEmail, ownerPassword, _ := shamirMasterKeyStack(t, 1, 1)
	ownerCookie := login(t, ts.URL, ownerEmail, ownerPassword)
	adminCookie := grantProjectAdmin(t, ts, srv, "mk-admin@corp.io")

	// Non-owner → 403 on rotate, rekey/init, and status read.
	if code := doAuthed(t, "POST", ts.URL+"/v1/sys/master-key/rotate", adminCookie, "", "", nil); code != http.StatusForbidden {
		t.Fatalf("non-owner rotate: want 403, got %d", code)
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/sys/master-key/rekey/init", adminCookie, "", "", nil); code != http.StatusForbidden {
		t.Fatalf("non-owner rekey init: want 403, got %d", code)
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/sys/master-key", adminCookie, "", "", nil); code != http.StatusForbidden {
		t.Fatalf("non-owner status: want 403, got %d", code)
	}

	// Owner status → 200 with unseal_type present.
	var st struct {
		UnsealType       string `json:"unseal_type"`
		MasterKeyVersion int    `json:"master_key_version"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/sys/master-key", ownerCookie, "", "", &st); code != 200 {
		t.Fatalf("owner status: want 200, got %d", code)
	}
	if st.UnsealType != crypto.SealTypeShamir {
		t.Fatalf("unseal_type: want shamir, got %q", st.UnsealType)
	}
}

// TestMasterKeyRotateKMSE2E: on a KMS seal the owner rotates in a single call.
func TestMasterKeyRotateKMSE2E(t *testing.T) {
	ts, _, ownerEmail, ownerPassword := kmsMasterKeyStack(t)
	ownerCookie := login(t, ts.URL, ownerEmail, ownerPassword)

	var rot struct {
		MasterKeyVersion int `json:"master_key_version"`
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/sys/master-key/rotate", ownerCookie, "", "", &rot); code != 200 {
		t.Fatalf("kms rotate: want 200, got %d", code)
	}
	if rot.MasterKeyVersion != 2 {
		t.Fatalf("master_key_version: want 2, got %d", rot.MasterKeyVersion)
	}
}

// TestMasterKeyRekeyShamirE2E: on a shamir seal the owner cannot single-call
// rotate (400), but can drive the rekey ceremony to completion.
func TestMasterKeyRekeyShamirE2E(t *testing.T) {
	ts, _, ownerEmail, ownerPassword, shares := shamirMasterKeyStack(t, 3, 2)
	ownerCookie := login(t, ts.URL, ownerEmail, ownerPassword)

	// Single-call rotate on a shamir seal → 400 (ceremony required).
	if code := doAuthed(t, "POST", ts.URL+"/v1/sys/master-key/rotate", ownerCookie, "", "", nil); code != http.StatusBadRequest {
		t.Fatalf("shamir rotate: want 400, got %d", code)
	}

	// Init the ceremony.
	var initResp struct {
		Nonce     string `json:"nonce"`
		Required  int    `json:"required"`
		Submitted int    `json:"submitted"`
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/sys/master-key/rekey/init", ownerCookie, "", "", &initResp); code != 200 {
		t.Fatalf("rekey init: want 200, got %d", code)
	}
	if initResp.Nonce == "" || initResp.Required != 2 || initResp.Submitted != 0 {
		t.Fatalf("rekey init resp: %+v", initResp)
	}

	// Submit the threshold of current shares; the final submit completes.
	var final struct {
		Complete         bool     `json:"complete"`
		MasterKeyVersion int      `json:"master_key_version"`
		NewShares        []string `json:"new_shares"`
		Submitted        int      `json:"submitted"`
		Required         int      `json:"required"`
	}
	for i := 0; i < initResp.Required; i++ {
		final = struct {
			Complete         bool     `json:"complete"`
			MasterKeyVersion int      `json:"master_key_version"`
			NewShares        []string `json:"new_shares"`
			Submitted        int      `json:"submitted"`
			Required         int      `json:"required"`
		}{}
		body := fmt.Sprintf(`{"nonce":%q,"share":%q}`, initResp.Nonce, shares[i])
		if code := doAuthed(t, "POST", ts.URL+"/v1/sys/master-key/rekey/submit", ownerCookie, "", body, &final); code != 200 {
			t.Fatalf("rekey submit %d: want 200, got %d", i, code)
		}
	}
	if !final.Complete {
		t.Fatalf("ceremony did not complete: %+v", final)
	}
	if final.MasterKeyVersion != 2 {
		t.Fatalf("master_key_version: want 2, got %d", final.MasterKeyVersion)
	}
	if len(final.NewShares) != 3 {
		t.Fatalf("new_shares: want 3, got %d", len(final.NewShares))
	}
}

// TestMasterKeySealedE2E: a sealed server refuses rotate and rekey/init with 503.
func TestMasterKeySealedE2E(t *testing.T) {
	ts, _, ownerEmail, ownerPassword, _ := shamirMasterKeyStack(t, 1, 1)
	ownerCookie := login(t, ts.URL, ownerEmail, ownerPassword)

	// Seal the server.
	var sealResp struct{ Sealed bool }
	if code := doAuthed(t, "POST", ts.URL+"/v1/sys/seal", ownerCookie, "", "", &sealResp); code != 200 || !sealResp.Sealed {
		t.Fatalf("seal: %d %+v", code, sealResp)
	}
	// Sealed → 503. (RequireUnsealed gates the whole /v1 surface.)
	if code := doAuthed(t, "POST", ts.URL+"/v1/sys/master-key/rotate", ownerCookie, "", "", nil); code != http.StatusServiceUnavailable {
		t.Fatalf("sealed rotate: want 503, got %d", code)
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/sys/master-key/rekey/init", ownerCookie, "", "", nil); code != http.StatusServiceUnavailable {
		t.Fatalf("sealed rekey init: want 503, got %d", code)
	}
}
