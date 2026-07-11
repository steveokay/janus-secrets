package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/steveokay/janus-secrets/internal/crypto"
)

// dynamicStack boots a real server against a throwaway Postgres and returns the
// test server, the admin credentials, a config id, and the container DSN. The
// DSN doubles as a usable admin_dsn for the dynamic engine: the testcontainer's
// superuser can CREATE/DROP ROLE, so the full issue path can run hermetically.
func dynamicStack(t *testing.T) (ts *httptest.Server, adminEmail, adminPass, cid, dsn string) {
	t.Helper()
	dsn = bootPostgres(t)
	ctx := context.Background()
	srv, st, err := Boot(ctx, BootConfig{DatabaseURL: dsn, SealType: crypto.SealTypeShamir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	ts = httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	var ir struct {
		Shares []string `json:"shares"`
		Admin  *struct{ Email, Password string } `json:"admin"`
	}
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/init",
		`{"shares":1,"threshold":1,"admin_email":"root@corp.io"}`, &ir); code != 200 {
		t.Fatalf("init: %d", code)
	}
	if ir.Admin == nil || ir.Admin.Password == "" {
		t.Fatalf("admin credential missing: %+v", ir.Admin)
	}
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/unseal",
		fmt.Sprintf(`{"share":%q}`, ir.Shares[0]), nil); code != 200 {
		t.Fatalf("unseal failed")
	}

	p, err := srv.service.CreateProject(ctx, "dyn", "Dyn")
	if err != nil {
		t.Fatal(err)
	}
	e, err := srv.service.CreateEnvironment(ctx, p.ID, "prod", "Prod")
	if err != nil {
		t.Fatal(err)
	}
	c, err := srv.service.CreateConfig(ctx, e.ID, "root", nil)
	if err != nil {
		t.Fatal(err)
	}
	return ts, ir.Admin.Email, ir.Admin.Password, c.ID, dsn
}

// TestDynamicAuthzAndMasking covers the /v1/dynamic HTTP surface: the authz
// matrix (DynamicManage is admin+ only; DynamicIssue is developer+), masked
// role views (admin_dsn / SQL templates never surface), and validation.
func TestDynamicAuthzAndMasking(t *testing.T) {
	ts, adminEmail, adminPass, cid, _ := dynamicStack(t)
	admin := login(t, ts.URL, adminEmail, adminPass)

	_, viewerPass := makeUser(t, ts.URL, admin, "dynviewer@corp.io", "viewer")
	_, devPass := makeUser(t, ts.URL, admin, "dyndev@corp.io", "developer")
	viewer := login(t, ts.URL, "dynviewer@corp.io", viewerPass)
	dev := login(t, ts.URL, "dyndev@corp.io", devPass)

	const rawDSN = "postgres://secretuser:secretpw@h:5432/db"
	const creation = `CREATE ROLE "{{name}}" LOGIN PASSWORD '{{password}}' VALID UNTIL '{{expiration}}';`
	createBody := fmt.Sprintf(
		`{"config_id":%q,"name":"readonly","default_ttl_seconds":3600,"max_ttl_seconds":7200,`+
			`"config":{"admin_dsn":%q,"creation_statements":%q}}`, cid, rawDSN, creation)

	// Developer and viewer are denied role creation (DynamicManage = admin+).
	if code := doAuthed(t, "POST", ts.URL+"/v1/dynamic/roles", dev, "", createBody, nil); code != http.StatusForbidden {
		t.Fatalf("developer create: want 403, got %d", code)
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/dynamic/roles", viewer, "", createBody, nil); code != http.StatusForbidden {
		t.Fatalf("viewer create: want 403, got %d", code)
	}

	// Validation: missing config_id / name -> 400.
	missingConfig := fmt.Sprintf(
		`{"name":"x","default_ttl_seconds":3600,"max_ttl_seconds":7200,"config":{"admin_dsn":"d","creation_statements":%q}}`, creation)
	if code := doAuthed(t, "POST", ts.URL+"/v1/dynamic/roles", admin, "", missingConfig, nil); code != http.StatusBadRequest {
		t.Fatalf("missing config_id: want 400, got %d", code)
	}
	missingName := fmt.Sprintf(
		`{"config_id":%q,"default_ttl_seconds":3600,"max_ttl_seconds":7200,"config":{"admin_dsn":"d","creation_statements":%q}}`, cid, creation)
	if code := doAuthed(t, "POST", ts.URL+"/v1/dynamic/roles", admin, "", missingName, nil); code != http.StatusBadRequest {
		t.Fatalf("missing name: want 400, got %d", code)
	}

	// Admin creates a role (201). Masked response must not echo the admin_dsn.
	req, err := http.NewRequest("POST", ts.URL+"/v1/dynamic/roles", strings.NewReader(createBody))
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
		t.Fatalf("admin create: %d %s", resp.StatusCode, createRaw)
	}
	if strings.Contains(createRaw, "secretpw") || strings.Contains(createRaw, "admin_dsn") ||
		strings.Contains(createRaw, "creation_statements") || strings.Contains(createRaw, "CREATE ROLE") {
		t.Fatalf("create response leaked role config: %s", createRaw)
	}
	var created struct {
		ID        string `json:"id"`
		ProjectID string `json:"project_id"`
		Name      string `json:"name"`
	}
	if err := decodeInto(createRaw, &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.ID == "" || created.Name != "readonly" {
		t.Fatalf("created = %+v", created)
	}

	// List by config as admin; masked view must not leak the DSN.
	_, listRaw := rawGet(t, ts.URL+"/v1/dynamic/roles?config_id="+cid, admin)
	if strings.Contains(listRaw, "secretpw") || strings.Contains(listRaw, "admin_dsn") {
		t.Fatalf("list leaked role config: %s", listRaw)
	}
	if !strings.Contains(listRaw, created.ID) {
		t.Fatalf("list missing created role: %s", listRaw)
	}

	// Get by id; masked view must not leak the DSN.
	_, getRaw := rawGet(t, ts.URL+"/v1/dynamic/roles/"+created.ID, admin)
	if strings.Contains(getRaw, "secretpw") || strings.Contains(getRaw, "admin_dsn") {
		t.Fatalf("get leaked role config: %s", getRaw)
	}

	// Viewer is denied on read (DynamicManage = admin+).
	if code := doAuthed(t, "GET", ts.URL+"/v1/dynamic/roles/"+created.ID, viewer, "", "", nil); code != http.StatusForbidden {
		t.Fatalf("viewer get: want 403, got %d", code)
	}

	// Delete cleans up (no live leases yet).
	var del struct {
		Deleted bool `json:"deleted"`
	}
	if code := doAuthed(t, "DELETE", ts.URL+"/v1/dynamic/roles/"+created.ID, admin, "", "", &del); code != http.StatusOK || !del.Deleted {
		t.Fatalf("delete: %d %+v", code, del)
	}
	var env errEnvelope
	if code := doAuthed(t, "GET", ts.URL+"/v1/dynamic/roles/"+created.ID, admin, "", "", &env); code != http.StatusNotFound || env.Error.Code != CodeDynamicNotFound {
		t.Fatalf("get after delete: %d %+v", code, env)
	}
}

// TestDynamicIssueLeaseViaAPI exercises the issue -> lease-list path against the
// throwaway container (its superuser can CREATE/DROP ROLE). Asserts developer
// can issue (201 with a non-empty password surfaced once), viewer is denied,
// and the issued password never appears in a subsequent lease listing.
func TestDynamicIssueLeaseViaAPI(t *testing.T) {
	ts, adminEmail, adminPass, cid, dsn := dynamicStack(t)
	admin := login(t, ts.URL, adminEmail, adminPass)
	_, devPass := makeUser(t, ts.URL, admin, "issuedev@corp.io", "developer")
	_, viewerPass := makeUser(t, ts.URL, admin, "issueviewer@corp.io", "viewer")
	dev := login(t, ts.URL, "issuedev@corp.io", devPass)
	viewer := login(t, ts.URL, "issueviewer@corp.io", viewerPass)

	creation := `CREATE ROLE "{{name}}" LOGIN PASSWORD '{{password}}' VALID UNTIL '{{expiration}}';`
	revocation := `DROP ROLE IF EXISTS "{{name}}";`
	// admin creates the role with the container DSN so IssueCreds can reach a DB.
	createBody := fmt.Sprintf(
		`{"config_id":%q,"name":"app","default_ttl_seconds":3600,"max_ttl_seconds":7200,`+
			`"config":{"admin_dsn":%q,"creation_statements":%q,"revocation_statements":%q}}`,
		cid, dsn, creation, revocation)
	var role struct {
		ID string `json:"id"`
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/dynamic/roles", admin, "", createBody, &role); code != http.StatusCreated || role.ID == "" {
		t.Fatalf("create role: %d %+v", code, role)
	}

	// Viewer is denied issuing (DynamicIssue = developer+).
	if code := doAuthed(t, "POST", ts.URL+"/v1/dynamic/roles/"+role.ID+"/creds", viewer, "", "", nil); code != http.StatusForbidden {
		t.Fatalf("viewer issue: want 403, got %d", code)
	}

	// Developer issues creds: 201, password surfaced once.
	var issued struct {
		LeaseID  string `json:"lease_id"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/dynamic/roles/"+role.ID+"/creds", dev, "", "", &issued); code != http.StatusCreated {
		t.Fatalf("developer issue: want 201, got %d", code)
	}
	if issued.LeaseID == "" || issued.Username == "" || issued.Password == "" {
		t.Fatalf("issue result missing fields: %+v", issued)
	}

	// The issued password must NEVER appear in a lease listing.
	_, leaseRaw := rawGet(t, ts.URL+"/v1/dynamic/leases?role_id="+role.ID, dev)
	if strings.Contains(leaseRaw, issued.Password) {
		t.Fatalf("lease list leaked issued password: %s", leaseRaw)
	}
	if !strings.Contains(leaseRaw, issued.LeaseID) {
		t.Fatalf("lease list missing issued lease: %s", leaseRaw)
	}
	if strings.Contains(leaseRaw, dsn) || strings.Contains(leaseRaw, "admin_dsn") {
		t.Fatalf("lease list leaked admin DSN: %s", leaseRaw)
	}

	// Renew then revoke the lease as developer.
	if code := doAuthed(t, "POST", ts.URL+"/v1/dynamic/leases/"+issued.LeaseID+"/renew", dev, "", "", nil); code != http.StatusOK {
		t.Fatalf("renew: want 200, got %d", code)
	}
	var revoked struct {
		Revoked bool `json:"revoked"`
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/dynamic/leases/"+issued.LeaseID+"/revoke", dev, "", "", &revoked); code != http.StatusOK || !revoked.Revoked {
		t.Fatalf("revoke: %d %+v", code, revoked)
	}
}
