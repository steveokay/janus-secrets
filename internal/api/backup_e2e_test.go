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

// drillStack boots a stack like authStackFull but returns the unseal share too.
func drillStack(t *testing.T) (tsURL string, srv *Server, share, email, password, cid string) {
	t.Helper()
	dsn := bootPostgres(t)
	ctx := context.Background()
	s, st, err := Boot(ctx, BootConfig{DatabaseURL: dsn, SealType: crypto.SealTypeShamir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	hts := httptest.NewServer(s.Handler())
	t.Cleanup(hts.Close)
	var ir struct {
		Shares []string `json:"shares"`
		Admin  *struct{ Email, Password string } `json:"admin"`
	}
	if code := doJSON(t, "POST", hts.URL+"/v1/sys/init",
		`{"shares":1,"threshold":1,"admin_email":"dr@corp.io"}`, &ir); code != 200 {
		t.Fatalf("init: %d", code)
	}
	if code := doJSON(t, "POST", hts.URL+"/v1/sys/unseal",
		fmt.Sprintf(`{"share":%q}`, ir.Shares[0]), nil); code != 200 {
		t.Fatal("unseal failed")
	}
	p, err := s.service.CreateProject(ctx, "drproj", "DR Project")
	if err != nil {
		t.Fatal(err)
	}
	e, err := s.service.CreateEnvironment(ctx, p.ID, "prod", "Prod")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.service.CreateConfig(ctx, e.ID, "root", nil)
	if err != nil {
		t.Fatal(err)
	}
	return hts.URL, s, ir.Shares[0], ir.Admin.Email, ir.Admin.Password, c.ID
}

func TestBackupRestoreRoundTrip(t *testing.T) {
	// --- Instance A: populate, back up. ---
	aURL, aSrv, share, email, password, cid := drillStack(t)
	cookie := login(t, aURL, email, password)
	if _, err := aSrv.service.SetSecrets(context.Background(), cid, []secrets.SecretChange{
		{Key: "DB_URL", Value: []byte("postgres://dr-secret")},
		{Key: "API_KEY", Value: []byte("sk-dr-42")},
	}, "seed", "test"); err != nil {
		t.Fatal(err)
	}

	// Seed a transit key and encrypt a value on A.
	const transitPT = "ZHItdHJhbnNpdC1wbGFpbnRleHQ=" // base64("dr-transit-plaintext")
	if code := doAuthed(t, "POST", aURL+"/v1/transit/keys",
		cookie, "", `{"name":"drkey","type":"aes256-gcm"}`, nil); code != 200 && code != 201 {
		t.Fatalf("transit key create: %d", code)
	}
	var enc struct {
		Ciphertext string `json:"ciphertext"`
	}
	if code := doAuthed(t, "POST", aURL+"/v1/transit/encrypt/drkey",
		cookie, "", `{"plaintext":"`+transitPT+`"}`, &enc); code != 200 || enc.Ciphertext == "" {
		t.Fatalf("transit encrypt: %d %+v", code, enc)
	}
	transitCT := enc.Ciphertext

	code, dump := backupRaw(t, aURL, cookie)
	if code != 200 {
		t.Fatalf("backup: %d", code)
	}

	// --- Instance B: fresh DB, restore, unseal with A's share. ---
	bDSN := bootPostgres(t)
	ctx := context.Background()
	bSrv, bSt, err := Boot(ctx, BootConfig{DatabaseURL: bDSN, SealType: crypto.SealTypeShamir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bSt.Close)
	bTS := httptest.NewServer(bSrv.Handler())
	t.Cleanup(bTS.Close)

	req, err := http.NewRequest("POST", bTS.URL+"/v1/sys/restore", strings.NewReader(dump))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("restore: %d", resp.StatusCode)
	}
	if code := doJSON(t, "POST", bTS.URL+"/v1/sys/unseal",
		fmt.Sprintf(`{"share":%q}`, share), nil); code != 200 {
		t.Fatal("unseal of restored instance with ORIGINAL share failed")
	}

	// Same credentials work; same plaintext comes back.
	bCookie := login(t, bTS.URL, email, password)
	var all struct {
		Secrets map[string]string `json:"secrets"`
	}
	if code := doAuthed(t, "GET", bTS.URL+"/v1/configs/"+cid+"/secrets?reveal=true",
		bCookie, "", "", &all); code != 200 {
		t.Fatalf("reveal on restored instance: %d", code)
	}
	if all.Secrets["DB_URL"] != "postgres://dr-secret" || all.Secrets["API_KEY"] != "sk-dr-42" {
		t.Fatalf("restored secrets differ: %+v", all.Secrets)
	}

	// Audit chain verifies ACROSS the restore boundary (sys.restore appended).
	var vr struct {
		Valid bool `json:"valid"`
	}
	if code := doAuthed(t, "GET", bTS.URL+"/v1/audit/verify", bCookie, "", "", &vr); code != 200 || !vr.Valid {
		t.Fatalf("audit verify after restore: code=%d valid=%v", code, vr.Valid)
	}

	// Transit survives too: ciphertext produced on A decrypts on B.
	var dec struct {
		Plaintext string `json:"plaintext"`
	}
	if code := doAuthed(t, "POST", bTS.URL+"/v1/transit/decrypt/drkey",
		bCookie, "", `{"ciphertext":"`+transitCT+`"}`, &dec); code != 200 || dec.Plaintext != transitPT {
		t.Fatalf("transit decrypt after restore: code=%d plaintext=%q", code, dec.Plaintext)
	}
}

func TestRestoreTruncatedStreamRollsBack(t *testing.T) {
	aURL, aSrv, _, email, password, cid := drillStack(t)
	cookie := login(t, aURL, email, password)
	if _, err := aSrv.service.SetSecrets(context.Background(), cid, []secrets.SecretChange{
		{Key: "K", Value: []byte("v")},
	}, "seed", "test"); err != nil {
		t.Fatal(err)
	}
	code, dump := backupRaw(t, aURL, cookie)
	if code != 200 {
		t.Fatalf("backup: %d", code)
	}
	truncated := dump[:len(dump)*2/3] // chop mid-stream

	bDSN := bootPostgres(t)
	bSrv, bSt, err := Boot(context.Background(), BootConfig{DatabaseURL: bDSN, SealType: crypto.SealTypeShamir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bSt.Close)
	bTS := httptest.NewServer(bSrv.Handler())
	t.Cleanup(bTS.Close)

	req, _ := http.NewRequest("POST", bTS.URL+"/v1/sys/restore", strings.NewReader(truncated))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 422 {
		t.Fatalf("truncated restore = %d, want 422", resp.StatusCode)
	}
	// Instance stayed empty → a full, valid restore still succeeds afterwards.
	req2, _ := http.NewRequest("POST", bTS.URL+"/v1/sys/restore", strings.NewReader(dump))
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("restore after rollback = %d, want 200", resp2.StatusCode)
	}
}

func TestRestoreSchemaVersionMismatch422(t *testing.T) {
	bDSN := bootPostgres(t)
	bSrv, bSt, err := Boot(context.Background(), BootConfig{DatabaseURL: bDSN, SealType: crypto.SealTypeShamir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bSt.Close)
	bTS := httptest.NewServer(bSrv.Handler())
	t.Cleanup(bTS.Close)
	var env errEnvelope
	if code := doJSON(t, "POST", bTS.URL+"/v1/sys/restore",
		`{"janus_backup":1,"migration_version":99999}`, &env); code != 422 || env.Error.Code != CodeValidation {
		t.Fatalf("mismatch = %d %+v (want 422 validation)", code, env)
	}
}
