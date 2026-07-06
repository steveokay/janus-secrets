package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

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

// TestAuditEventsPagination pins GET /v1/audit/events: seq-descending keyset
// pagination, filter validation, auth, wire shape parity with export rows,
// and — critically — that reading the events endpoint does NOT itself append
// an audit event (precedent: /verify).
func TestAuditEventsPagination(t *testing.T) {
	ts, srv, email, password, configID := authStackFull(t)
	adminCookie := login(t, ts.URL, email, password) // records auth.login

	// --- Grow the log with a few more audited actions. ---
	var minted struct {
		ID string `json:"id"`
	}
	mintBody := fmt.Sprintf(`{"name":"ci","scope":{"kind":"config","id":%q},"access":"read"}`, configID)
	if code := doAuthed(t, "POST", ts.URL+"/v1/tokens", adminCookie, "", mintBody, &minted); code != 200 || minted.ID == "" {
		t.Fatalf("mint: %d %+v", code, minted)
	}
	var member struct{ ID, Password string }
	if code := doAuthed(t, "POST", ts.URL+"/v1/users", adminCookie, "", `{"email":"events-dev@corp.io"}`, &member); code != 200 || member.ID == "" {
		t.Fatalf("create user: %d %+v", code, member)
	}
	if code := doAuthed(t, "PUT", ts.URL+"/v1/instance/members/"+member.ID, adminCookie, "", `{"role":"developer"}`, nil); code != 204 {
		t.Fatalf("grant member: %d", code)
	}

	repo := store.NewAuditRepo(srv.st)
	headSeq := func() int64 {
		t.Helper()
		rows, err := repo.ListPage(context.Background(), store.AuditFilter{}, 0, 1)
		if err != nil {
			t.Fatalf("head seq lookup: %v", err)
		}
		if len(rows) == 0 {
			t.Fatal("expected at least one audit row seeded already")
		}
		return rows[0].Seq
	}

	// --- Unauthenticated -> 401. ---
	if code := doAuthed(t, "GET", ts.URL+"/v1/audit/events", "", "", "", nil); code != 401 {
		t.Fatalf("unauthenticated events: want 401, got %d", code)
	}

	// --- Validation errors -> 400. ---
	for _, q := range []string{"limit=0", "limit=201", "limit=abc", "cursor=abc", "result=bogus"} {
		code, body := rawGet(t, ts.URL+"/v1/audit/events?"+q, adminCookie)
		if code != 400 {
			t.Fatalf("query %q: want 400, got %d body=%s", q, code, body)
		}
	}

	type wireEvent struct {
		Seq        int64   `json:"seq"`
		OccurredAt string  `json:"occurred_at"`
		ActorKind  string  `json:"actor_kind"`
		ActorName  string  `json:"actor_name"`
		Action     string  `json:"action"`
		Resource   string  `json:"resource"`
		Result     string  `json:"result"`
		PrevHash   string  `json:"prev_hash"`
		Hash       string  `json:"hash"`
	}
	type wirePage struct {
		Events     []wireEvent `json:"events"`
		NextCursor *int64      `json:"next_cursor"`
	}

	// --- CRITICAL: GET /events must not self-audit. ---
	before := headSeq()
	var page1 wirePage
	if code := doAuthed(t, "GET", ts.URL+"/v1/audit/events?limit=2", adminCookie, "", "", &page1); code != 200 {
		t.Fatalf("events page1: %d", code)
	}
	after := headSeq()
	if after != before {
		t.Fatalf("GET /v1/audit/events must not self-audit: head seq before=%d after=%d", before, after)
	}

	// --- Shape + ordering assertions on page 1. ---
	if len(page1.Events) != 2 {
		t.Fatalf("want 2 events, got %d: %+v", len(page1.Events), page1.Events)
	}
	if page1.Events[0].Seq <= page1.Events[1].Seq {
		t.Fatalf("expected newest-first ordering, got seqs %d, %d", page1.Events[0].Seq, page1.Events[1].Seq)
	}
	for _, e := range page1.Events {
		if _, err := hex.DecodeString(e.PrevHash); err != nil {
			t.Fatalf("prev_hash not hex: %q (%v)", e.PrevHash, err)
		}
		if _, err := hex.DecodeString(e.Hash); err != nil {
			t.Fatalf("hash not hex: %q (%v)", e.Hash, err)
		}
		if _, err := time.Parse(time.RFC3339Nano, e.OccurredAt); err != nil {
			t.Fatalf("occurred_at not RFC3339Nano: %q (%v)", e.OccurredAt, err)
		}
	}
	if page1.NextCursor == nil || *page1.NextCursor != page1.Events[1].Seq {
		t.Fatalf("next_cursor = %v, want %d", page1.NextCursor, page1.Events[1].Seq)
	}

	// --- Walk pages to exhaustion; every page strictly lower than the last. ---
	seen := map[int64]bool{}
	for _, e := range page1.Events {
		seen[e.Seq] = true
	}
	minSeqSoFar := page1.Events[len(page1.Events)-1].Seq
	cursor := *page1.NextCursor
	reachedEnd := false
	for i := 0; i < 1000; i++ {
		var page wirePage
		url := fmt.Sprintf("%s/v1/audit/events?limit=2&cursor=%d", ts.URL, cursor)
		if code := doAuthed(t, "GET", url, adminCookie, "", "", &page); code != 200 {
			t.Fatalf("events page walk: %d", code)
		}
		for _, e := range page.Events {
			if e.Seq >= minSeqSoFar {
				t.Fatalf("expected strictly decreasing seqs, got %d after min %d", e.Seq, minSeqSoFar)
			}
			if seen[e.Seq] {
				t.Fatalf("seq %d returned twice across pages", e.Seq)
			}
			seen[e.Seq] = true
			minSeqSoFar = e.Seq
		}
		if page.NextCursor == nil {
			reachedEnd = true
			break
		}
		cursor = *page.NextCursor
	}
	if !reachedEnd {
		t.Fatal("did not reach a final page (next_cursor null) within 1000 iterations")
	}
	if len(seen) < 4 { // at least auth.login, token.mint, user.create, member.grant
		t.Fatalf("expected to walk at least 4 events, saw %d", len(seen))
	}
}
