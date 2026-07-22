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
	"testing"
	"time"

	"github.com/steveokay/janus-secrets/internal/auth"
	"github.com/steveokay/janus-secrets/internal/crypto"
)

// TestLockoutNoSecretLeak drives the lock-trip and account_locked (429) paths and
// asserts no password or hash appears in logs, response bodies, or the audit
// export, and that value-free auth.lockout events are recorded.
func TestLockoutNoSecretLeak(t *testing.T) {
	dsn := bootPostgres(t)
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	ctx := context.Background()

	srv, st, err := Boot(ctx, BootConfig{
		DatabaseURL: dsn, SealType: crypto.SealTypeShamir, Logger: logger,
		Lockout: auth.LockoutPolicy{Enabled: true, Threshold: 2, Base: time.Hour, Max: 24 * time.Hour},
	})
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
	doJSON(t, "POST", ts.URL+"/v1/sys/init", `{"shares":1,"threshold":1,"admin_email":"root@corp.io"}`, &ir)
	doJSON(t, "POST", ts.URL+"/v1/sys/unseal", fmt.Sprintf(`{"share":%q}`, ir.Shares[0]), nil)

	// Create + fetch the victim's real one-time password so we can drive the
	// correct-password-while-locked reveal path.
	_, victimPW, err := srv.auth.CreateUser(ctx, "victim@corp.io")
	if err != nil {
		t.Fatal(err)
	}

	var bodies []string
	post := func(body string) *http.Response {
		resp, err := http.Post(ts.URL+"/v1/auth/login", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	read := func(resp *http.Response) {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		bodies = append(bodies, string(b))
	}

	// Two wrong logins trip the lock (threshold 2).
	read(post(fmt.Sprintf(`{"email":"victim@corp.io","password":%q}`, "wrong-a")))
	read(post(fmt.Sprintf(`{"email":"victim@corp.io","password":%q}`, "wrong-b")))
	// Correct password while locked → 429 account_locked (reveals only the lock).
	resp := post(fmt.Sprintf(`{"email":"victim@corp.io","password":%q}`, victimPW))
	if resp.StatusCode != http.StatusTooManyRequests {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("expected 429, got %d %s", resp.StatusCode, b)
	}
	read(resp)

	// Admin exports the audit log (login POST is the admin's first & only, well
	// within the per-IP budget).
	cookie := login(t, ts.URL, ir.Admin.Email, ir.Admin.Password)
	exportReq, _ := http.NewRequest("GET", ts.URL+"/v1/audit/export?format=jsonl", nil)
	exportReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	exportResp, err := http.DefaultClient.Do(exportReq)
	if err != nil {
		t.Fatal(err)
	}
	auditDump, _ := io.ReadAll(exportResp.Body)
	exportResp.Body.Close()

	// No password material anywhere.
	for _, needle := range []string{victimPW, ir.Admin.Password, cookie} {
		if strings.Contains(logBuf.String(), needle) {
			t.Fatal("credential material leaked into logs")
		}
		for _, b := range bodies {
			if strings.Contains(b, needle) {
				t.Fatalf("credential material echoed in response body: %s", b)
			}
		}
		if strings.Contains(string(auditDump), needle) {
			t.Fatal("credential material leaked into audit export")
		}
	}
	// The value-free lockout event was recorded.
	if !strings.Contains(string(auditDump), "auth.lockout") {
		t.Fatalf("expected auth.lockout audit event; export was:\n%s", auditDump)
	}
}
