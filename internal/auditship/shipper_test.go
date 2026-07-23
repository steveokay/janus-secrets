package auditship

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/steveokay/janus-secrets/internal/store"
)

// --- fakes -------------------------------------------------------------------

type fakeTailer struct {
	rows []store.AuditRow
	err  error
	// calls records the afterSeq of each ListSince call.
	calls []int64
}

func (f *fakeTailer) ListSince(_ context.Context, afterSeq int64, limit int) ([]store.AuditRow, error) {
	f.calls = append(f.calls, afterSeq)
	if f.err != nil {
		return nil, f.err
	}
	var out []store.AuditRow
	for _, r := range f.rows {
		if r.Seq > afterSeq {
			out = append(out, r)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

type fakeMarks struct {
	mu   sync.Mutex
	seq  int64
	adv  []int64 // AdvanceHighWater calls
	gErr error
	aErr error
}

func (m *fakeMarks) GetHighWater(context.Context) (int64, error) {
	if m.gErr != nil {
		return 0, m.gErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.seq, nil
}

func (m *fakeMarks) AdvanceHighWater(_ context.Context, newSeq int64) error {
	if m.aErr != nil {
		return m.aErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.adv = append(m.adv, newSeq)
	if newSeq > m.seq {
		m.seq = newSeq
	}
	return nil
}

// captureDest records the last batch it received and can be told to fail.
type captureDest struct {
	fail  error
	calls int
	last  []batchItem
}

func (d *captureDest) Describe() string { return "capture" }
func (d *captureDest) Send(_ context.Context, batch []batchItem) error {
	d.calls++
	if d.fail != nil {
		return d.fail
	}
	d.last = batch
	return nil
}

func ptr(s string) *string { return &s }

func sampleRows() []store.AuditRow {
	t := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	return []store.AuditRow{
		{Seq: 1, OccurredAt: t, ActorKind: "user", ActorID: ptr("u-1"), ActorName: "alice@example.test",
			Action: "secret.reveal", Resource: "projects/demo/dev/API_KEY", Result: "success",
			IP: "203.0.113.7", PrevHash: []byte{0x00}, Hash: []byte{0xab, 0xcd}},
		{Seq: 2, OccurredAt: t.Add(time.Second), ActorKind: "service_token", ActorName: "ci",
			Action: "secret.read", Resource: "projects/demo/prod/DB_URL", Result: "denied",
			Detail: ptr("role=viewer"), ResultCode: ptr("forbidden"), IP: "198.51.100.9",
			PrevHash: []byte{0xab, 0xcd}, Hash: []byte{0xef}},
	}
}

// newTestService builds a Service with the given dest and fakes, bypassing env.
func newTestService(dest Destination, aud auditTailer, marks markStore) *Service {
	s := &Service{cfg: Config{Mode: ModeWebhook}, dest: dest, audit: aud, marks: marks,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)), now: time.Now}
	return s
}

// --- tests -------------------------------------------------------------------

func TestJSONLShape(t *testing.T) {
	batch, err := encodeBatch([]shippedEvent{toShipped(sampleRows()[0]), toShipped(sampleRows()[1])})
	if err != nil {
		t.Fatal(err)
	}
	if len(batch) != 2 {
		t.Fatalf("want 2 items, got %d", len(batch))
	}
	// Each line must be valid single-line JSON with the canonical fields.
	var got map[string]any
	if err := json.Unmarshal(batch[0].line, &got); err != nil {
		t.Fatalf("line not valid JSON: %v", err)
	}
	for _, f := range []string{"seq", "occurred_at", "actor_kind", "action", "result", "prev_hash", "hash"} {
		if _, ok := got[f]; !ok {
			t.Errorf("missing field %q in %s", f, batch[0].line)
		}
	}
	if got["action"] != "secret.reveal" {
		t.Errorf("action=%v", got["action"])
	}
	if got["hash"] != "abcd" { // hex of 0xab 0xcd
		t.Errorf("hash not hex-encoded: %v", got["hash"])
	}
	if strings.ContainsAny(string(batch[0].line), "\n\r") {
		t.Errorf("JSONL line must be single-line: %q", batch[0].line)
	}
	// Denied event carries detail + result_code; omitted-when-empty holds for seq 1.
	var d map[string]any
	_ = json.Unmarshal(batch[1].line, &d)
	if d["detail"] != "role=viewer" || d["result_code"] != "forbidden" {
		t.Errorf("seq2 detail/code wrong: %v", d)
	}
	if _, ok := got["detail"]; ok {
		t.Errorf("seq1 should omit empty detail")
	}
}

func TestHighWaterAdvancesOnlyOnSuccess(t *testing.T) {
	aud := &fakeTailer{rows: sampleRows()}
	marks := &fakeMarks{seq: 0}
	dest := &captureDest{}
	s := newTestService(dest, aud, marks)

	s.RunDue(context.Background())
	if len(marks.adv) != 1 || marks.adv[0] != 2 {
		t.Fatalf("mark should advance to max seq 2, got %v", marks.adv)
	}
	if len(dest.last) != 2 {
		t.Fatalf("dest should receive 2 events, got %d", len(dest.last))
	}
	// Second pass: nothing new past seq 2 → no send, no advance.
	dest.calls = 0
	s.RunDue(context.Background())
	if dest.calls != 0 {
		t.Fatalf("no new events should mean no send, got %d calls", dest.calls)
	}
}

func TestFailedSendDoesNotAdvance(t *testing.T) {
	aud := &fakeTailer{rows: sampleRows()}
	marks := &fakeMarks{seq: 0}
	dest := &captureDest{fail: errors.New("connection refused")}
	s := newTestService(dest, aud, marks)

	s.RunDue(context.Background())
	if len(marks.adv) != 0 {
		t.Fatalf("failed send must NOT advance mark, got %v", marks.adv)
	}
	if marks.seq != 0 {
		t.Fatalf("mark moved on failure: %d", marks.seq)
	}
	// Recovery: dest now succeeds, same events re-shipped from the same mark (no gap).
	dest.fail = nil
	s.RunDue(context.Background())
	if len(marks.adv) != 1 || marks.adv[0] != 2 {
		t.Fatalf("recovery should advance to 2, got %v", marks.adv)
	}
	// The tailer was queried from seq 0 both times (no gap, at-least-once).
	if aud.calls[0] != 0 || aud.calls[1] != 0 {
		t.Fatalf("both passes should tail from seq 0, got %v", aud.calls)
	}
}

func TestWebhookFramingAndSignature(t *testing.T) {
	var (
		gotBody []byte
		gotCT   string
		gotSig  string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotCT = r.Header.Get("Content-Type")
		gotSig = r.Header.Get("X-Janus-Signature")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	// obviously-fake low-entropy hmac key
	dest := newWebhookDest(srv.URL, []byte("test-hmac-key"), 5*time.Second)
	batch, _ := encodeBatch([]shippedEvent{toShipped(sampleRows()[0]), toShipped(sampleRows()[1])})
	if err := dest.Send(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	if gotCT != "application/x-ndjson" {
		t.Errorf("content-type=%q", gotCT)
	}
	// NDJSON: two non-empty lines, each valid JSON, trailing newline.
	lines := strings.Split(strings.TrimRight(string(gotBody), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 ndjson lines, got %d: %q", len(lines), gotBody)
	}
	for _, ln := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(ln), &m); err != nil {
			t.Errorf("bad ndjson line %q: %v", ln, err)
		}
	}
	if !strings.HasPrefix(gotSig, "sha256=") {
		t.Errorf("missing/invalid signature: %q", gotSig)
	}
}

func TestWebhookNon2xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	dest := newWebhookDest(srv.URL, nil, 5*time.Second)
	batch, _ := encodeBatch([]shippedEvent{toShipped(sampleRows()[0])})
	err := dest.Send(context.Background(), batch)
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("want HTTP 500 error, got %v", err)
	}
}

func TestSyslogFramingTCP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	type result struct {
		lines []string
		err   error
	}
	resCh := make(chan result, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			resCh <- result{err: err}
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		data, _ := io.ReadAll(conn)
		resCh <- result{lines: parseOctetFrames(string(data))}
	}()

	dest := newSyslogDest("tcp", ln.Addr().String(), 3*time.Second)
	batch, _ := encodeBatch([]shippedEvent{toShipped(sampleRows()[0]), toShipped(sampleRows()[1])})
	if err := dest.Send(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	// Give the reader a moment then close by relying on the client's conn close.
	res := <-resCh
	if res.err != nil {
		t.Fatal(res.err)
	}
	if len(res.lines) != 2 {
		t.Fatalf("want 2 syslog frames, got %d: %v", len(res.lines), res.lines)
	}
	// RFC 5424 header: <109>1 <ts> <host> janus <seq> audit - <json>
	msg := res.lines[0]
	if !strings.HasPrefix(msg, fmt.Sprintf("<%d>1 ", syslogPriority)) {
		t.Errorf("bad RFC5424 prefix: %q", msg)
	}
	if !strings.Contains(msg, " janus ") || !strings.Contains(msg, " audit - ") {
		t.Errorf("missing app-name/msgid: %q", msg)
	}
	// The MSG must be the JSON event.
	i := strings.Index(msg, "{")
	if i < 0 {
		t.Fatalf("no JSON MSG in frame: %q", msg)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(msg[i:]), &m); err != nil {
		t.Errorf("MSG not JSON: %v (%q)", err, msg[i:])
	}
	if m["seq"].(float64) != 1 {
		t.Errorf("first frame should be seq 1, got %v", m["seq"])
	}
}

func TestSyslogFramingUDP(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()

	got := make(chan string, 4)
	go func() {
		buf := make([]byte, 4096)
		for {
			_ = pc.SetReadDeadline(time.Now().Add(2 * time.Second))
			n, _, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			got <- string(buf[:n])
		}
	}()

	dest := newSyslogDest("udp", pc.LocalAddr().String(), 2*time.Second)
	batch, _ := encodeBatch([]shippedEvent{toShipped(sampleRows()[0])})
	if err := dest.Send(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-got:
		if !strings.HasPrefix(msg, fmt.Sprintf("<%d>1 ", syslogPriority)) {
			t.Errorf("bad UDP RFC5424 frame: %q", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no UDP datagram received")
	}
}

// parseOctetFrames splits an RFC 6587 octet-counted stream ("<len> <msg>...")
// into its messages.
func parseOctetFrames(s string) []string {
	var out []string
	rd := bufio.NewReader(strings.NewReader(s))
	for {
		var n int
		if _, err := fmt.Fscanf(rd, "%d ", &n); err != nil {
			break
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(rd, buf); err != nil {
			break
		}
		out = append(out, string(buf))
	}
	return out
}

func TestConfigFromEnv(t *testing.T) {
	t.Setenv("JANUS_AUDIT_SHIP_MODE", "")
	if c, err := ConfigFromEnv(); err != nil || c.Enabled() {
		t.Fatalf("empty mode should be disabled: %+v %v", c, err)
	}

	t.Setenv("JANUS_AUDIT_SHIP_MODE", "webhook")
	t.Setenv("JANUS_AUDIT_SHIP_WEBHOOK_URL", "https://siem.example.test/ingest")
	c, err := ConfigFromEnv()
	if err != nil || c.Mode != ModeWebhook || !c.Enabled() {
		t.Fatalf("webhook config: %+v %v", c, err)
	}

	t.Setenv("JANUS_AUDIT_SHIP_WEBHOOK_URL", "file:///etc/passwd")
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("non-http webhook url should be rejected")
	}

	t.Setenv("JANUS_AUDIT_SHIP_MODE", "syslog")
	t.Setenv("JANUS_AUDIT_SHIP_SYSLOG_ADDR", "logs.example.test:514")
	c, err = ConfigFromEnv()
	if err != nil || c.Mode != ModeSyslog || c.SyslogNetwork != "udp" {
		t.Fatalf("syslog config: %+v %v", c, err)
	}
	t.Setenv("JANUS_AUDIT_SHIP_SYSLOG_ADDR", "")
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("empty syslog addr should be rejected")
	}

	t.Setenv("JANUS_AUDIT_SHIP_MODE", "bogus")
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("bogus mode should be rejected")
	}
}

func TestStatusSnapshot(t *testing.T) {
	aud := &fakeTailer{rows: sampleRows()}
	marks := &fakeMarks{}
	dest := &captureDest{}
	s := newTestService(dest, aud, marks)
	s.RunDue(context.Background())
	st := s.Status()
	if st.HighWaterSeq != 2 || st.LastShipCount != 2 || st.LastShipAt == nil || st.LastError != "" {
		t.Fatalf("status after success: %+v", st)
	}
	// A failure records a sanitized error and leaves the mark. Reset the mark so
	// there are new events for the failing send to act on.
	marks.mu.Lock()
	marks.seq = 0
	marks.mu.Unlock()
	dest.fail = errors.New("connection refused")
	s.RunDue(context.Background())
	st = s.Status()
	if st.LastError != "connection failed" {
		t.Fatalf("expected sanitized error, got %q", st.LastError)
	}
}

func TestSanitizeNeverLeaksURL(t *testing.T) {
	err := fmt.Errorf(`Post "https://siem.example.test/ingest?token=SECRET": dial tcp: connection refused`)
	got := sanitize(err)
	if strings.Contains(got, "siem.example.test") || strings.Contains(got, "SECRET") {
		t.Fatalf("sanitize leaked destination detail: %q", got)
	}
	if got != "connection failed" {
		t.Fatalf("want coarse category, got %q", got)
	}
}
