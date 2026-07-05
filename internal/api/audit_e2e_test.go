package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/steveokay/janus-secrets/internal/store"
)

// rawGet issues an authenticated (session cookie) GET and returns the raw
// response body + status — used for the audit export whose body is JSONL, not a
// single JSON object doAuthed could decode.
func rawGet(t *testing.T, url, cookie string) (int, string) {
	t.Helper()
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// TestAuditE2E drives the retrofit flow against a REAL recorder (authStackFull
// boots via Boot, which wires audit.New(store.NewAuditRepo(st))) and asserts the
// chain verifies, the expected success actions are exported, a masked read is
// NOT audited, a denied attempt is recorded, and sys.seal is recorded.
func TestAuditE2E(t *testing.T) {
	ts, srv, email, password, configID := authStackFull(t)
	adminCookie := login(t, ts.URL, email, password) // records auth.login success

	// --- Denied attempt: a read-only service token cannot mint a token. ---
	var ro struct {
		Token string `json:"token"`
	}
	roBody := fmt.Sprintf(`{"name":"ro","scope":{"kind":"config","id":%q},"access":"read"}`, configID)
	if code := doAuthed(t, "POST", ts.URL+"/v1/tokens", adminCookie, "", roBody, &ro); code != 200 {
		t.Fatalf("mint ro token: %d", code)
	}
	// The read-only token attempts a mint → 403, recorded as a denied token.mint.
	if code := doAuthed(t, "POST", ts.URL+"/v1/tokens", "", ro.Token,
		fmt.Sprintf(`{"name":"x","scope":{"kind":"config","id":%q},"access":"read"}`, configID), nil); code != 403 {
		t.Fatalf("expected denied mint 403, got %d", code)
	}

	// --- Masked read: token LIST + /v1/auth/me must NOT be audited. ---
	if code := doAuthed(t, "GET", ts.URL+"/v1/tokens", adminCookie, "", "", nil); code != 200 {
		t.Fatalf("token list: %d", code)
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/auth/me", adminCookie, "", "", nil); code != 200 {
		t.Fatalf("me: %d", code)
	}

	// --- Mint a token (success → token.mint). ---
	var minted struct {
		ID string `json:"id"`
	}
	mintBody := fmt.Sprintf(`{"name":"ci","scope":{"kind":"config","id":%q},"access":"read"}`, configID)
	if code := doAuthed(t, "POST", ts.URL+"/v1/tokens", adminCookie, "", mintBody, &minted); code != 200 || minted.ID == "" {
		t.Fatalf("mint: %d %+v", code, minted)
	}

	// --- Grant a member (success → member.grant). Create a second user first. ---
	var member struct{ ID, Password string }
	if code := doAuthed(t, "POST", ts.URL+"/v1/users", adminCookie, "", `{"email":"dev@corp.io"}`, &member); code != 200 || member.ID == "" {
		t.Fatalf("create user: %d %+v", code, member)
	}
	if code := doAuthed(t, "PUT", ts.URL+"/v1/instance/members/"+member.ID, adminCookie, "", `{"role":"developer"}`, nil); code != 204 {
		t.Fatalf("grant member: %d", code)
	}

	// --- Verify the chain. ---
	var verify struct {
		Valid bool  `json:"valid"`
		Count int64 `json:"count"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/audit/verify", adminCookie, "", "", &verify); code != 200 {
		t.Fatalf("verify: %d", code)
	}
	if !verify.Valid || verify.Count == 0 {
		t.Fatalf("verify = %+v (want valid, count>0)", verify)
	}

	// --- Export JSONL and inspect actions/results. ---
	code, exBody := rawGet(t, ts.URL+"/v1/audit/export?format=jsonl", adminCookie)
	if code != 200 {
		t.Fatalf("export: %d %s", code, exBody)
	}
	actions := map[string]int{}
	sawDenied := false
	sc := bufio.NewScanner(bytes.NewReader([]byte(exBody)))
	for sc.Scan() {
		var row struct {
			Action string `json:"action"`
			Result string `json:"result"`
		}
		if err := json.Unmarshal(sc.Bytes(), &row); err != nil {
			t.Fatalf("bad export line %q: %v", sc.Text(), err)
		}
		actions[row.Action]++
		if row.Result == "denied" {
			sawDenied = true
		}
	}
	for _, want := range []string{"auth.login", "token.mint", "member.grant", "audit.export"} {
		if actions[want] == 0 {
			t.Fatalf("expected audit action %q in export, got %v", want, actions)
		}
	}
	// Masked reads (token list / me) are not audited.
	if actions["token.list"] != 0 || actions["auth.me"] != 0 {
		t.Fatalf("masked reads must not be audited: %v", actions)
	}
	if !sawDenied {
		t.Fatalf("expected at least one denied row in export, got %v", actions)
	}

	// --- Seal (success → sys.seal). Sealing blocks the RequireAuth-gated audit
	// endpoints (503 while sealed), so confirm the recorded event via the store
	// directly rather than a post-seal export. ---
	var sealed struct{ Sealed bool }
	if code := doAuthed(t, "POST", ts.URL+"/v1/sys/seal", adminCookie, "", "", &sealed); code != 200 || !sealed.Sealed {
		t.Fatalf("seal: %d %+v", code, sealed)
	}
	repo := store.NewAuditRepo(srv.st)
	sawSeal := false
	if err := repo.Iterate(context.Background(), func(a store.AuditRow) error {
		if a.Action == "sys.seal" && a.Result == "success" {
			sawSeal = true
		}
		return nil
	}); err != nil {
		t.Fatalf("iterate audit rows: %v", err)
	}
	if !sawSeal {
		t.Fatal("expected a sys.seal success row after sealing")
	}

	_ = strings.TrimSpace
}
