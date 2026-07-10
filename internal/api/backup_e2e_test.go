package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/secrets"
)

// backupRaw GETs /v1/sys/backup with a session cookie and returns status+body.
func backupRaw(t *testing.T, base, cookie string) (int, string) {
	t.Helper()
	req, err := http.NewRequest("GET", base+"/v1/sys/backup", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var sb strings.Builder
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		sb.WriteString(sc.Text())
		sb.WriteByte('\n')
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, sb.String()
}

func TestBackupE2E(t *testing.T) {
	ts, srv, email, password, cid := authStackFull(t)
	cookie := login(t, ts.URL, email, password)
	if _, err := srv.service.SetSecrets(context.Background(), cid, []secrets.SecretChange{
		{Key: "CANARY", Value: []byte("plaintext-canary-8d1f")},
	}, "seed", "test"); err != nil {
		t.Fatal(err)
	}

	code, body := backupRaw(t, ts.URL, cookie)
	if code != 200 {
		t.Fatalf("backup: %d", code)
	}
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")

	// Header first: format 1 + a positive migration version.
	var hdr struct {
		JanusBackup      int   `json:"janus_backup"`
		MigrationVersion int64 `json:"migration_version"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &hdr); err != nil {
		t.Fatalf("header: %v (%s)", err, lines[0])
	}
	if hdr.JanusBackup != 1 || hdr.MigrationVersion < 1 {
		t.Fatalf("header = %+v", hdr)
	}
	// Contains the seeded structure and, critically, NO plaintext.
	if !strings.Contains(body, `"table":"projects"`) || !strings.Contains(body, `"table":"secret_values"`) {
		t.Fatalf("dump missing tables")
	}
	if strings.Contains(body, "plaintext-canary-8d1f") {
		t.Fatal("backup leaked a plaintext secret value")
	}
	// Audited.
	_, exp := rawGet(t, ts.URL+"/v1/audit/export?format=jsonl&action=sys.backup", cookie)
	if !strings.Contains(exp, "sys.backup") {
		t.Fatal("sys.backup audit event missing")
	}
}

func TestBackupForbiddenForNonAdminToken(t *testing.T) {
	ts, _, email, password, cid := authStackFull(t)
	cookie := login(t, ts.URL, email, password)
	// Mint a read-only config-scoped token (wire shape per handleTokenMint:
	// nested scope object, 200 on success).
	var mint struct {
		Token string `json:"token"`
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/tokens",
		cookie, "", `{"name":"ci","scope":{"kind":"config","id":"`+cid+`"},"access":"read"}`, &mint); code != 200 {
		t.Fatalf("mint: %d", code)
	}
	if mint.Token == "" {
		t.Fatal("mint returned no token")
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/sys/backup", "", mint.Token, "", nil); code != 403 {
		t.Fatalf("backup with scoped token = %d, want 403", code)
	}
}

func TestRestoreRefusedWithZeroRecords(t *testing.T) {
	// Fresh EMPTY stack: boot without init, so the emptiness gate passes and
	// the zero-record guard is what rejects the request. A header-only body
	// must NOT commit (and must not append sys.restore, which would poison
	// the still-empty instance at audit seq 1).
	dsn := bootPostgres(t)
	ctx := context.Background()
	srv, st, err := Boot(ctx, BootConfig{DatabaseURL: dsn, SealType: crypto.SealTypeShamir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// Header must pass the format + schema-version checks to reach the guard.
	ver, err := st.SchemaVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var env errEnvelope
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/restore",
		fmt.Sprintf(`{"janus_backup":1,"migration_version":%d}`, ver), &env); code != 422 || env.Error.Code != CodeValidation {
		t.Fatalf("zero-record restore = %d %+v (want 422 validation)", code, env)
	}
}

func TestRestoreRefusedOnNonEmptyInstance(t *testing.T) {
	ts, _, _, _, _ := authStackFull(t) // initialized == not empty
	var env errEnvelope
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/restore",
		`{"janus_backup":1,"migration_version":1}`, &env); code != 409 || env.Error.Code != "not_empty" {
		t.Fatalf("restore on live instance = %d %+v (want 409 not_empty)", code, env)
	}
}
