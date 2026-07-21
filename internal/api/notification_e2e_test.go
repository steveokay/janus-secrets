package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// sink is a test webhook receiver that records the bodies + signature headers
// it is POSTed.
type sink struct {
	mu    sync.Mutex
	calls []sinkCall
	code  int
}

type sinkCall struct {
	body []byte
	sig  string
}

func newSink() *sink { return &sink{code: 200} }

func (s *sink) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.calls = append(s.calls, sinkCall{body: b, sig: r.Header.Get("X-Janus-Signature")})
		code := s.code
		s.mu.Unlock()
		w.WriteHeader(code)
	}
}

func (s *sink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func (s *sink) last() sinkCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[len(s.calls)-1]
}

func TestNotificationsE2E(t *testing.T) {
	ts, srv, _, ownerPw, _ := authStackFull(t)
	owner := login(t, ts.URL, "root@corp.io", ownerPw)

	recv := newSink()
	recvTS := httptest.NewServer(recv.handler())
	t.Cleanup(recvTS.Close)

	const hmacKey = "super-secret-signing-key-value"

	// Create a webhook channel subscribed to access.denied, with an HMAC key.
	var created struct {
		ID      string   `json:"id"`
		Events  []string `json:"events"`
		URL     string   `json:"url"`      // must never be populated
		HMACKey string   `json:"hmac_key"` // must never be populated
	}
	body := fmt.Sprintf(`{"name":"alerts","type":"webhook","url":%q,"hmac_key":%q,"events":["access.denied"]}`, recvTS.URL, hmacKey)
	if code := doAuthed(t, "POST", ts.URL+"/v1/notifications/channels", owner, "", body, &created); code != 201 {
		t.Fatalf("create channel: %d", code)
	}
	if created.ID == "" {
		t.Fatal("no channel id")
	}
	if created.URL != "" || created.HMACKey != "" {
		t.Fatalf("channel create response leaked config: url=%q hmac=%q", created.URL, created.HMACKey)
	}

	// Masked list never carries the URL or HMAC key.
	var listResp struct {
		Channels []map[string]any `json:"channels"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/notifications/channels", owner, "", "", &listResp); code != 200 || len(listResp.Channels) != 1 {
		t.Fatalf("list: %d %+v", code, listResp)
	}
	rawList, _ := json.Marshal(listResp)
	if strings.Contains(string(rawList), recvTS.URL) || strings.Contains(string(rawList), hmacKey) {
		t.Fatalf("masked list leaked config: %s", rawList)
	}

	// Test delivery: synchronous, signs the body, value-free.
	if code := doAuthed(t, "POST", ts.URL+"/v1/notifications/channels/"+created.ID+"/test", owner, "", "", nil); code != 200 {
		t.Fatalf("test: %d", code)
	}
	if recv.count() != 1 {
		t.Fatalf("test delivery not received: %d calls", recv.count())
	}
	tc := recv.last()
	if !strings.HasPrefix(tc.sig, "sha256=") {
		t.Fatalf("test delivery unsigned: %q", tc.sig)
	}
	if strings.Contains(string(tc.body), hmacKey) {
		t.Fatalf("delivery body leaked hmac key: %s", tc.body)
	}

	// Trigger a real denial: a brand-new user with no bindings hitting an
	// admin-only route → 403 → a `denied` audit event.
	var u struct{ ID, Email, Password string }
	if code := doAuthed(t, "POST", ts.URL+"/v1/users", owner, "", `{"email":"lowpriv@corp.io"}`, &u); code != 200 {
		t.Fatalf("create user: %d", code)
	}
	lp := login(t, ts.URL, "lowpriv@corp.io", u.Password)
	if code := doAuthed(t, "GET", ts.URL+"/v1/notifications/channels", lp, "", "", nil); code != 403 {
		t.Fatalf("expected 403 for unprivileged list, got %d", code)
	}

	// Run one dispatcher pass: it tails the audit log, fans the denial into the
	// outbox, and delivers it to the sink.
	before := recv.count()
	srv.notification.RunDue(context.Background())
	if recv.count() <= before {
		t.Fatalf("denial was not fanned out/delivered: before=%d after=%d", before, recv.count())
	}
	got := recv.last()
	if !strings.Contains(string(got.body), "access.denied") {
		t.Fatalf("delivered payload missing event kind: %s", got.body)
	}

	// Deliveries history is value-free and shows a delivered row.
	var hist struct {
		Deliveries []map[string]any `json:"deliveries"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/notifications/channels/"+created.ID+"/deliveries", owner, "", "", &hist); code != 200 {
		t.Fatalf("deliveries: %d", code)
	}
	if len(hist.Deliveries) == 0 {
		t.Fatal("no delivery history recorded")
	}

	// Config never appears in the audit export (leak surface).
	exp := auditExport(t, ts.URL, owner)
	if strings.Contains(exp, recvTS.URL) || strings.Contains(exp, hmacKey) {
		t.Fatal("notification config leaked into the audit export")
	}

	// Delete.
	if code := doAuthed(t, "DELETE", ts.URL+"/v1/notifications/channels/"+created.ID, owner, "", "", nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
}

func auditExport(t *testing.T, base, cookie string) string {
	t.Helper()
	req, _ := http.NewRequest("GET", base+"/v1/audit/export", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}
