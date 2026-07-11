package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/store"
)

// dynamicStackWithLogger mirrors dynamicStack but threads a caller-supplied
// slog.Logger into Boot (via BootConfig.Logger) so the dynamic engine's own
// warn logs (e.g. the revoke/cleanup path on a failed CREATE ROLE, and audit
// write failures) are captured. It returns the server, admin credentials, a
// config id, and the container DSN (usable as a role's admin_dsn).
func dynamicStackWithLogger(t *testing.T, logger *slog.Logger) (ts *httptest.Server, srv *Server, adminEmail, adminPass, cid, dsn string) {
	t.Helper()
	dsn = bootPostgres(t)
	ctx := context.Background()
	srv, st, err := Boot(ctx, BootConfig{DatabaseURL: dsn, SealType: crypto.SealTypeShamir, Logger: logger})
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
	return ts, srv, ir.Admin.Email, ir.Admin.Password, c.ID, dsn
}

// TestDynamicPasswordNeverInLogsOrAudit is the capstone security proof for the
// dynamic-credentials feature: the one secret it mints — the Postgres password
// returned once from the issue endpoint — must NEVER reach log output or the
// audit trail.
//
// It boots a dynamic-capable server against a real container Postgres with a
// captured log buffer, issues creds (capturing the returned password), and also
// drives an ERROR path that exercises the engine's cleanup/revoke logging (a
// role whose creation SQL fails). It then asserts:
//   - the captured logs do NOT contain the issued password;
//   - the audit trail (both raw audit_events rows and /v1/audit/export) does NOT
//     contain the issued password;
//   - the dynamic.creds.issue event IS present with detail db_user=<user> but
//     without the password (positive control that issuance was audited).
func TestDynamicPasswordNeverInLogsOrAudit(t *testing.T) {
	var logBuf syncBuffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	ts, srv, adminEmail, adminPass, cid, dsn := dynamicStackWithLogger(t, logger)
	admin := login(t, ts.URL, adminEmail, adminPass)
	_, devPass := makeUser(t, ts.URL, admin, "leakdev@corp.io", "developer")
	dev := login(t, ts.URL, "leakdev@corp.io", devPass)

	creation := `CREATE ROLE "{{name}}" LOGIN PASSWORD '{{password}}' VALID UNTIL '{{expiration}}';`
	revocation := `DROP ROLE IF EXISTS "{{name}}";`

	// Working role: admin authors it with the container DSN so the full issue
	// path (CREATE ROLE) succeeds against a real database.
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

	// Issue creds: the password is surfaced exactly once here.
	var issued struct {
		LeaseID  string `json:"lease_id"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/dynamic/roles/"+role.ID+"/creds", dev, "", "", &issued); code != http.StatusCreated {
		t.Fatalf("issue creds: want 201, got %d", code)
	}
	if issued.Password == "" || issued.Username == "" {
		t.Fatalf("issue result missing fields: %+v", issued)
	}

	// ERROR path: a role whose creation SQL is malformed. IssueCreds reserves a
	// lease row, the CREATE fails, and the engine runs its cleanup revoke — a
	// path that logs a warn. Prove the (freshly minted) password for this attempt
	// never lands in that log line. We can't observe the failed attempt's password
	// directly (it is never returned), so this arm's value is that it forces the
	// error/logging code to run; the working arm's captured password is the
	// canary we assert on below.
	badCreation := `CREATE ROLE "{{name}}" LOGIN PASSWORD '{{password}}' THIS IS NOT VALID SQL;`
	badCreateBody := fmt.Sprintf(
		`{"config_id":%q,"name":"badrole","default_ttl_seconds":3600,"max_ttl_seconds":7200,`+
			`"config":{"admin_dsn":%q,"creation_statements":%q,"revocation_statements":%q}}`,
		cid, dsn, badCreation, revocation)
	var badRole struct {
		ID string `json:"id"`
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/dynamic/roles", admin, "", badCreateBody, &badRole); code != http.StatusCreated || badRole.ID == "" {
		t.Fatalf("create bad role: %d %+v", code, badRole)
	}
	// Expect a non-2xx: the CREATE ROLE fails, the engine returns an apply error.
	if code := doAuthed(t, "POST", ts.URL+"/v1/dynamic/roles/"+badRole.ID+"/creds", dev, "", "", nil); code == http.StatusCreated {
		t.Fatalf("bad-role issue unexpectedly succeeded (want failure)")
	}

	// --- Assertions ---

	// (1) The captured logs must never contain the issued password. Sanity-check
	// that the buffer captured *something* first, so a green result can't be an
	// artifact of an empty buffer.
	logs := logBuf.String()
	if strings.TrimSpace(logs) == "" {
		t.Fatalf("no log output captured; cannot prove absence of leak")
	}
	if strings.Contains(logs, issued.Password) {
		t.Fatalf("issued dynamic password LEAKED into log output")
	}

	// (2) Raw audit_events rows must contain neither the password nor the raw
	// admin DSN, and must include the dynamic.creds.issue positive control with
	// db_user=<username> in its detail but not the password.
	repo := store.NewAuditRepo(srv.st)
	var dump strings.Builder
	sawIssue := false
	if err := repo.Iterate(context.Background(), func(a store.AuditRow) error {
		fmt.Fprintf(&dump, "%s|%s|%s|%s\n", a.Action, a.Resource, derefStr(a.Detail), a.Result)
		if a.Action == "dynamic.creds.issue" && a.Result == "success" {
			detail := derefStr(a.Detail)
			if !strings.Contains(detail, "db_user="+issued.Username) {
				t.Fatalf("dynamic.creds.issue detail missing db_user=%s: %q", issued.Username, detail)
			}
			if strings.Contains(detail, issued.Password) {
				t.Fatalf("dynamic.creds.issue detail LEAKED the password: %q", detail)
			}
			sawIssue = true
		}
		return nil
	}); err != nil {
		t.Fatalf("iterate audit rows: %v", err)
	}
	if !sawIssue {
		t.Fatalf("positive control missing: no dynamic.creds.issue audit event was recorded")
	}
	if strings.Contains(dump.String(), issued.Password) {
		t.Fatalf("issued dynamic password LEAKED into an audit_events row")
	}
	if strings.Contains(dump.String(), "secretpw") {
		// Defensive: no admin-DSN credential material in audit rows.
		t.Fatalf("admin DSN credential leaked into an audit_events row")
	}

	// (3) The audit export stream must likewise contain no password.
	code, exBody := rawGet(t, ts.URL+"/v1/audit/export?format=jsonl", admin)
	if code != 200 {
		t.Fatalf("audit export: %d", code)
	}
	if strings.Contains(exBody, issued.Password) {
		t.Fatalf("issued dynamic password LEAKED into /v1/audit/export output")
	}
	if !strings.Contains(exBody, "dynamic.creds.issue") {
		t.Fatalf("audit export missing dynamic.creds.issue positive control")
	}
}
