package api

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/store"
)

// TestNoShareMaterialInLogsOrErrors drives the full init/unseal lifecycle with
// log capture and asserts that no share hex ever appears in the logs, and that
// error-path responses never echo submitted share material.
func TestNoShareMaterialInLogsOrErrors(t *testing.T) {
	var logBuf bytes.Buffer
	seals := &memSealStore{}
	kr := crypto.NewKeyring()
	u := crypto.NewShamirUnsealer(seals, 0, 0)
	srv := New(Config{SealType: crypto.SealTypeShamir}, kr, u, seals, nil, nil, nil, nil, nil,
		nil, nil, nil, nil, slog.New(slog.NewTextHandler(&logBuf, nil)))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	var ir initResp
	doJSON(t, "POST", ts.URL+"/v1/sys/init", `{"shares":5,"threshold":3}`, &ir)
	if len(ir.Shares) != 5 {
		t.Fatalf("init shares = %d", len(ir.Shares))
	}

	// Collect error-path response bodies: duplicate share, poisoned set.
	var errBodies []string
	post := func(body string) string {
		resp, err := http.Post(ts.URL+"/v1/sys/unseal", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	post(fmt.Sprintf(`{"share":%q}`, ir.Shares[0]))
	errBodies = append(errBodies, post(fmt.Sprintf(`{"share":%q}`, ir.Shares[0]))) // duplicate
	post(fmt.Sprintf(`{"share":%q}`, ir.Shares[1]))
	corrupted := "ff" + ir.Shares[2][2:]
	errBodies = append(errBodies, post(fmt.Sprintf(`{"share":%q}`, corrupted))) // poisons the set

	logs := logBuf.String()
	for i, sh := range ir.Shares {
		if strings.Contains(logs, sh) {
			t.Fatalf("share %d leaked into logs", i)
		}
		for _, eb := range errBodies {
			if strings.Contains(eb, sh) {
				t.Fatalf("share %d echoed in error response: %s", i, eb)
			}
		}
	}
	if strings.Contains(logs, corrupted) {
		t.Fatal("submitted share material leaked into logs")
	}
	// Sanity: the logger did log the requests (method/path/status).
	if !strings.Contains(logs, "/v1/sys/unseal") {
		t.Fatalf("expected request logs, got: %q", logs)
	}
}

// TestNoSecretValueInAuditRowsOrExport runs a mutating flow that pushes known
// secret material (a chosen password, plus the mint-once raw service token)
// through audited handlers, then asserts that neither secret ever appears in
// (a) any column of any audit_events row, nor (b) the /v1/audit/export output.
// The audit design forbids a value field on Event; this is the belt-and-braces
// end-to-end proof against a real recorder.
func TestNoSecretValueInAuditRowsOrExport(t *testing.T) {
	ts, srv, email, password, configID := authStackFull(t)
	cookie := login(t, ts.URL, email, password)

	const sentinelPassword = "SENTINEL-known-secret-value-9c3f-do-not-leak"

	// Password change carries the sentinel through an audited handler
	// (auth.password_change); the subsequent login carries it again (auth.login).
	if code := doAuthed(t, "POST", ts.URL+"/v1/auth/password", cookie, "",
		fmt.Sprintf(`{"old":%q,"new":%q}`, password, sentinelPassword), nil); code != 204 {
		t.Fatalf("password change: %d", code)
	}
	cookie = login(t, ts.URL, email, sentinelPassword)

	// Mint a token — the raw token is a mint-once secret that must never be
	// audited (the handler records only "tokens/<id>").
	var minted struct{ Token, ID string }
	mintBody := fmt.Sprintf(`{"name":"leak","scope":{"kind":"config","id":%q},"access":"read"}`, configID)
	if code := doAuthed(t, "POST", ts.URL+"/v1/tokens", cookie, "", mintBody, &minted); code != 200 || minted.Token == "" {
		t.Fatalf("mint: %d %+v", code, minted)
	}

	secrets := []string{sentinelPassword, minted.Token}

	// (a) Dump every column of every audit_events row via the store repo (its
	// SELECT lists all columns) and scan each field for the secrets.
	repo := store.NewAuditRepo(srv.st)
	var dump strings.Builder
	rows := 0
	if err := repo.Iterate(context.Background(), func(a store.AuditRow) error {
		rows++
		fmt.Fprintf(&dump, "%d|%s|%s|%s|%s|%s|%s|%s|%s|%s|%s|%x|%x\n",
			a.Seq, a.OccurredAt.Format("2006-01-02T15:04:05.000000Z07:00"),
			a.ActorKind, derefStr(a.ActorID), a.ActorName, a.Action, a.Resource,
			derefStr(a.Detail), a.Result, derefStr(a.ResultCode), a.IP, a.PrevHash, a.Hash)
		return nil
	}); err != nil {
		t.Fatalf("iterate audit rows: %v", err)
	}
	if rows == 0 {
		t.Fatal("no audit rows written; flow did not exercise the recorder")
	}
	for _, sec := range secrets {
		if strings.Contains(dump.String(), sec) {
			t.Fatalf("secret value leaked into an audit_events row")
		}
	}

	// (b) The export output must likewise contain no secret material.
	code, exBody := rawGet(t, ts.URL+"/v1/audit/export?format=jsonl", cookie)
	if code != 200 {
		t.Fatalf("export: %d", code)
	}
	for _, sec := range secrets {
		if strings.Contains(exBody, sec) {
			t.Fatal("secret value leaked into /v1/audit/export output")
		}
	}
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// syncBuffer is a mutex-guarded buffer safe for the concurrent writes the
// request-logger middleware performs (its logger.Info runs in a handler
// goroutine that may still be finishing as the test issues its next request).
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestNoSecretValueInLogsOrErrorResponse writes a known sentinel secret value
// through the HTTP write route, reveals it, and proves the value never appears
// in (a) the captured request-logger output nor (b) an error response body. It
// reuses the real stack harness (authStackFull) and redirects the default slog
// logger — which Boot uses when BootConfig.Logger is nil — into a buffer so the
// request logs are captured.
func TestNoSecretValueInLogsOrErrorResponse(t *testing.T) {
	var logBuf syncBuffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	defer slog.SetDefault(prev)

	ts, _, email, password, cid := authStackFull(t)
	cookie := login(t, ts.URL, email, password)

	const sentinel = "SENTINEL-LEAK-CANARY-9f3a"

	// Write the sentinel through the HTTP per-key write route.
	if code := doAuthed(t, "PUT", ts.URL+"/v1/configs/"+cid+"/secrets/CANARY", cookie, "",
		fmt.Sprintf(`{"value":%q}`, sentinel), nil); code != 200 {
		t.Fatalf("write CANARY: %d", code)
	}

	// Reveal it — the value is expected in the client response, never in logs.
	var revealed struct{ Key, Value string }
	if code := doAuthed(t, "GET", ts.URL+"/v1/configs/"+cid+"/secrets/CANARY", cookie, "", "", &revealed); code != 200 {
		t.Fatalf("reveal CANARY: %d", code)
	}
	if revealed.Value != sentinel {
		t.Fatalf("reveal returned %q, want the sentinel", revealed.Value)
	}

	// (a) The request logger must never have written the sentinel value.
	if strings.Contains(logBuf.String(), sentinel) {
		t.Fatal("sentinel secret value leaked into captured log output")
	}
	// Sanity: the logger did capture the request (proves capture is wired).
	if !strings.Contains(logBuf.String(), "/v1/configs/"+cid+"/secrets/CANARY") {
		t.Fatalf("expected request logs to include the secret path, got: %q", logBuf.String())
	}

	// (b) An error response (nonexistent value-version -> 404) must not echo the
	// sentinel.
	code, errBody := rawGet(t, ts.URL+"/v1/configs/"+cid+"/secrets/CANARY?version=99999", cookie)
	if code != http.StatusNotFound {
		t.Fatalf("bad version: want 404, got %d (body %s)", code, errBody)
	}
	if strings.Contains(errBody, sentinel) {
		t.Fatalf("sentinel secret value leaked into error response body: %s", errBody)
	}
}
