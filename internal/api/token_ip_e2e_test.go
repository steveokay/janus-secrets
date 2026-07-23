package api

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// TestTokenIPAllowlistE2E exercises the full path: mint a token with an
// ip_allowlist, confirm it surfaces on GET /v1/tokens, and that a request from
// the loopback client IP is allowed when 127.0.0.0/8 is listed and rejected
// (403) when only a foreign CIDR is listed. The e2e server runs on loopback, so
// the client IP is 127.0.0.1.
func TestTokenIPAllowlistE2E(t *testing.T) {
	ts, email, password, configID := authStack(t)
	cookie := login(t, ts.URL, email, password)

	mint := func(name string, allowlistJSON string) string {
		var minted struct {
			Token       string   `json:"token"`
			ID          string   `json:"id"`
			IPAllowlist []string `json:"ip_allowlist"`
		}
		body := fmt.Sprintf(`{"name":%q,"scope":{"kind":"config","id":%q},"access":"read","ip_allowlist":%s}`,
			name, configID, allowlistJSON)
		if code := doAuthed(t, "POST", ts.URL+"/v1/tokens", cookie, "", body, &minted); code != 200 {
			t.Fatalf("mint %s: %d", name, code)
		}
		return minted.Token
	}

	// Allowed: loopback is inside 127.0.0.0/8.
	okTok := mint("ci-ok", `["127.0.0.0/8"]`)
	if code := doAuthed(t, "GET", ts.URL+"/v1/auth/me", "", okTok, "", nil); code != 200 {
		t.Fatalf("token in allowlist should authenticate, got %d", code)
	}

	// Denied: loopback is NOT inside 10.0.0.0/8.
	denyTok := mint("ci-deny", `["10.0.0.0/8"]`)
	if code := doAuthed(t, "GET", ts.URL+"/v1/auth/me", "", denyTok, "", nil); code != 403 {
		t.Fatalf("token outside allowlist should be 403, got %d", code)
	}

	// Empty allowlist authenticates from any IP.
	anyTok := mint("ci-any", `[]`)
	if code := doAuthed(t, "GET", ts.URL+"/v1/auth/me", "", anyTok, "", nil); code != 200 {
		t.Fatalf("empty allowlist should authenticate, got %d", code)
	}

	// GET /v1/tokens surfaces the allowlist.
	var out struct {
		Tokens []struct {
			ID          string   `json:"id"`
			Name        string   `json:"name"`
			IPAllowlist []string `json:"ip_allowlist"`
		} `json:"tokens"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/tokens", cookie, "", "", &out); code != 200 {
		t.Fatalf("list tokens: %d", code)
	}
	byName := map[string][]string{}
	for _, tk := range out.Tokens {
		byName[tk.Name] = tk.IPAllowlist
	}
	if got := byName["ci-ok"]; len(got) != 1 || got[0] != "127.0.0.0/8" {
		t.Fatalf("ci-ok allowlist = %v", got)
	}
	if got := byName["ci-any"]; len(got) != 0 {
		t.Fatalf("ci-any allowlist = %v, want empty", got)
	}

	// Invalid CIDR at the boundary → 400.
	badBody := fmt.Sprintf(`{"name":"bad","scope":{"kind":"config","id":%q},"access":"read","ip_allowlist":["not-a-cidr"]}`, configID)
	if code := doAuthed(t, "POST", ts.URL+"/v1/tokens", cookie, "", badBody, nil); code != 400 {
		t.Fatalf("invalid CIDR should be 400, got %d", code)
	}
}

// TestTokenNewIPAuditE2E verifies that authenticating a service token from a new
// IP emits exactly one value-free token.new_ip audit event (throttled — a second
// auth from the same IP does not re-fire), carrying the IP but no token value.
func TestTokenNewIPAuditE2E(t *testing.T) {
	ts, email, password, configID := authStack(t)
	cookie := login(t, ts.URL, email, password)

	var minted struct {
		Token string `json:"token"`
		ID    string `json:"id"`
	}
	body := fmt.Sprintf(`{"name":"ci","scope":{"kind":"config","id":%q},"access":"read"}`, configID)
	if code := doAuthed(t, "POST", ts.URL+"/v1/tokens", cookie, "", body, &minted); code != 200 {
		t.Fatalf("mint: %d", code)
	}

	// Authenticate twice from the same (loopback) IP.
	for i := 0; i < 2; i++ {
		if code := doAuthed(t, "GET", ts.URL+"/v1/auth/me", "", minted.Token, "", nil); code != 200 {
			t.Fatalf("token auth %d: %d", i, code)
		}
	}

	// Export audit and count token.new_ip events — must be exactly one.
	code, exBody := rawGet(t, ts.URL+"/v1/audit/export?format=jsonl", cookie)
	if code != 200 {
		t.Fatalf("export: %d %s", code, exBody)
	}
	newIP := 0
	sc := bufio.NewScanner(bytes.NewReader([]byte(exBody)))
	for sc.Scan() {
		var row struct {
			Action   string `json:"action"`
			Resource string `json:"resource"`
			Detail   string `json:"detail"`
		}
		if err := json.Unmarshal(sc.Bytes(), &row); err != nil {
			t.Fatalf("bad export line %q: %v", sc.Text(), err)
		}
		if row.Action == "token.new_ip" {
			newIP++
			if row.Resource != "tokens/"+minted.ID {
				t.Fatalf("new_ip resource = %q", row.Resource)
			}
			// Value-free: detail is an ip=... note, never the raw token.
			if !strings.HasPrefix(row.Detail, "ip=") {
				t.Fatalf("new_ip detail = %q, want ip=...", row.Detail)
			}
			if strings.Contains(exBody, minted.Token) {
				t.Fatal("audit export leaked the raw token value")
			}
		}
	}
	if newIP != 1 {
		t.Fatalf("token.new_ip events = %d, want exactly 1 (throttled)", newIP)
	}

	// The in-tray aggregate reports the recent new-IP count.
	var agg struct {
		Count       int `json:"count"`
		WindowHours int `json:"window_hours"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/tokens/new-ips", cookie, "", "", &agg); code != 200 {
		t.Fatalf("new-ips aggregate: %d", code)
	}
	if agg.Count < 1 || agg.WindowHours != 24 {
		t.Fatalf("new-ips aggregate = %+v", agg)
	}
}

// TestTokenIPUpdateE2E exercises PATCH /v1/tokens/{id} to change an allowlist:
// a token minted with a deny-all-foreign CIDR is initially rejected, then
// updated to allow loopback and accepted, then cleared and still accepted.
func TestTokenIPUpdateE2E(t *testing.T) {
	ts, email, password, configID := authStack(t)
	cookie := login(t, ts.URL, email, password)

	var minted struct {
		Token string `json:"token"`
		ID    string `json:"id"`
	}
	body := fmt.Sprintf(`{"name":"ci","scope":{"kind":"config","id":%q},"access":"read","ip_allowlist":["10.0.0.0/8"]}`, configID)
	if code := doAuthed(t, "POST", ts.URL+"/v1/tokens", cookie, "", body, &minted); code != 200 {
		t.Fatalf("mint: %d", code)
	}
	// Initially denied.
	if code := doAuthed(t, "GET", ts.URL+"/v1/auth/me", "", minted.Token, "", nil); code != 403 {
		t.Fatalf("pre-update should be 403, got %d", code)
	}
	// Update allowlist to include loopback.
	if code := doAuthed(t, "PATCH", ts.URL+"/v1/tokens/"+minted.ID, cookie, "",
		`{"ip_allowlist":["127.0.0.0/8"]}`, nil); code != 204 {
		t.Fatalf("patch allowlist: %d", code)
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/auth/me", "", minted.Token, "", nil); code != 200 {
		t.Fatalf("post-update should authenticate, got %d", code)
	}
	// Clear allowlist → any IP.
	if code := doAuthed(t, "PATCH", ts.URL+"/v1/tokens/"+minted.ID, cookie, "",
		`{"ip_allowlist":[]}`, nil); code != 204 {
		t.Fatalf("clear allowlist: %d", code)
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/auth/me", "", minted.Token, "", nil); code != 200 {
		t.Fatalf("cleared allowlist should authenticate, got %d", code)
	}
	// Invalid CIDR on update → 400.
	if code := doAuthed(t, "PATCH", ts.URL+"/v1/tokens/"+minted.ID, cookie, "",
		`{"ip_allowlist":["garbage"]}`, nil); code != 400 {
		t.Fatalf("invalid CIDR update should be 400, got %d", code)
	}
}
