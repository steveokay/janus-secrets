package notification

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/store"
)

// sentinelSecret is a value that must NEVER appear in a rendered email, because
// notifications are value-free by construction.
const sentinelSecret = "s3cr3t-value-DO-NOT-LEAK"
const sentinelPassword = "smtp-p@ssw0rd-DO-NOT-LEAK"

func testPayload() eventPayload {
	return eventPayload{
		Event: EventRotationFailed, Seq: 42, OccurredAt: time.Unix(1_700_000_000, 0).UTC(),
		Action: "rotation.rotate", Result: "failure",
		Resource: "configs/x/secrets/STRIPE_KEY", Actor: "rotation:policy-1",
		Detail: "apply failed",
	}
}

func TestBuildMessageValueFree(t *testing.T) {
	cfg := channelConfig{
		From: "janus@corp.io", To: []string{"ops@corp.io", "sec@corp.io"},
		Password: sentinelPassword,
	}
	// Even if a payload detail somehow carried a value (it can't structurally),
	// the renderer only pulls audit metadata; assert the password never appears.
	msg := string(buildMessage(cfg, testPayload()))
	if strings.Contains(msg, sentinelSecret) {
		t.Fatalf("message leaked a secret value:\n%s", msg)
	}
	if strings.Contains(msg, sentinelPassword) {
		t.Fatalf("message leaked the SMTP password:\n%s", msg)
	}
}

func TestBuildMessageHeaders(t *testing.T) {
	cfg := channelConfig{From: "janus@corp.io", To: []string{"a@corp.io", "b@corp.io"}}
	msg := string(buildMessage(cfg, testPayload()))

	// CRLF line endings and a header/body separator.
	if !strings.Contains(msg, "\r\n\r\n") {
		t.Fatalf("missing CRLF header/body separator:\n%q", msg)
	}
	if strings.Contains(strings.ReplaceAll(msg, "\r\n", ""), "\n") {
		t.Fatalf("found a bare LF (non-CRLF line ending):\n%q", msg)
	}
	want := []string{
		"From: janus@corp.io\r\n",
		"To: a@corp.io, b@corp.io\r\n",
		"Subject: Janus: rotation failed\r\n",
		"MIME-Version: 1.0\r\n",
		"Content-Type: text/plain; charset=utf-8\r\n",
		"Date: ",
	}
	for _, w := range want {
		if !strings.Contains(msg, w) {
			t.Fatalf("missing header %q in:\n%s", w, msg)
		}
	}
	// All recipients present in the To header.
	for _, r := range cfg.To {
		if !strings.Contains(msg, r) {
			t.Fatalf("recipient %q missing from To header", r)
		}
	}
	// Value-free body carries the metadata.
	if !strings.Contains(msg, "resource: configs/x/secrets/STRIPE_KEY") {
		t.Fatalf("body missing resource line:\n%s", msg)
	}
}

func TestBuildMessageHeaderInjectionGuard(t *testing.T) {
	cfg := channelConfig{
		From: "evil@corp.io\r\nBcc: attacker@evil.io",
		To:   []string{"ops@corp.io\r\nSubject: injected"},
	}
	msg := string(buildMessage(cfg, testPayload()))
	// The injected header names must not appear at the START of any line (CR/LF
	// stripped so they collapse into the From/To header values instead).
	for _, line := range strings.Split(msg, "\r\n") {
		if strings.HasPrefix(line, "Bcc:") {
			t.Fatalf("header injection via From produced a Bcc header:\n%s", msg)
		}
		if strings.HasPrefix(line, "Subject: injected") {
			t.Fatalf("header injection via To produced an injected Subject:\n%s", msg)
		}
	}
	// From line collapses onto one header line (CR/LF removed).
	if !strings.Contains(msg, "From: evil@corp.ioBcc: attacker@evil.io\r\n") {
		t.Fatalf("expected CR/LF-stripped From:\n%s", msg)
	}
}

func TestValidateSMTP(t *testing.T) {
	base := func() ChannelInput {
		return ChannelInput{
			Events: []string{EventRotationFailed}, SMTPHost: "smtp.corp.io", SMTPPort: 587,
			SMTPFrom: "janus@corp.io", SMTPTo: []string{"ops@corp.io"}, SMTPTLSMode: "starttls",
		}
	}
	cases := []struct {
		name    string
		mutate  func(*ChannelInput)
		hmacSet bool
		wantErr bool
	}{
		{"valid", func(*ChannelInput) {}, false, false},
		{"valid default tls", func(in *ChannelInput) { in.SMTPTLSMode = "" }, false, false},
		{"valid multiple recipients", func(in *ChannelInput) { in.SMTPTo = []string{"a@corp.io", "b@corp.io"} }, false, false},
		{"missing host", func(in *ChannelInput) { in.SMTPHost = "" }, false, true},
		{"missing port", func(in *ChannelInput) { in.SMTPPort = 0 }, false, true},
		{"port too high", func(in *ChannelInput) { in.SMTPPort = 70000 }, false, true},
		{"port negative", func(in *ChannelInput) { in.SMTPPort = -1 }, false, true},
		{"missing from", func(in *ChannelInput) { in.SMTPFrom = "" }, false, true},
		{"bad from", func(in *ChannelInput) { in.SMTPFrom = "not-an-email" }, false, true},
		{"no recipients", func(in *ChannelInput) { in.SMTPTo = nil }, false, true},
		{"bad recipient", func(in *ChannelInput) { in.SMTPTo = []string{"ok@corp.io", "bad"} }, false, true},
		{"bad tls_mode", func(in *ChannelInput) { in.SMTPTLSMode = "ssl" }, false, true},
		{"no events", func(in *ChannelInput) { in.Events = nil }, false, true},
		{"hmac rejected", func(*ChannelInput) {}, true, true},
		{"from header injection", func(in *ChannelInput) { in.SMTPFrom = "a@b.io\r\nBcc: x@y" }, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := base()
			c.mutate(&in)
			err := validateSMTP(in, c.hmacSet)
			if c.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !c.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestNormalizeTLSMode(t *testing.T) {
	if m, err := normalizeTLSMode(""); err != nil || m != "starttls" {
		t.Fatalf("empty => (%q,%v), want starttls", m, err)
	}
	for _, m := range []string{"starttls", "implicit", "none"} {
		if got, err := normalizeTLSMode(m); err != nil || got != m {
			t.Fatalf("%q => (%q,%v)", m, got, err)
		}
	}
	if _, err := normalizeTLSMode("bogus"); err == nil {
		t.Fatal("bogus tls_mode should error")
	}
}

func TestWrapUnwrapPreservesSMTPFields(t *testing.T) {
	kr := crypto.NewKeyring()
	master := make([]byte, 32)
	for i := range master {
		master[i] = byte(i)
	}
	if err := kr.Unseal(master); err != nil {
		t.Fatal(err)
	}
	svc := &Service{kr: kr}
	want := channelConfig{
		Host: "smtp.corp.io", Port: 465, From: "janus@corp.io",
		To: []string{"a@corp.io", "b@corp.io"}, Username: "user", Password: sentinelPassword,
		TLSMode: "implicit", InsecureSkipVerify: true,
	}
	ct, err := svc.wrapConfig("chan-1", want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.unwrapConfig(&store.NotificationChannel{ID: "chan-1", ConfigCT: ct})
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != want.Host || got.Port != want.Port || got.From != want.From ||
		got.Username != want.Username || got.Password != want.Password ||
		got.TLSMode != want.TLSMode || got.InsecureSkipVerify != want.InsecureSkipVerify ||
		len(got.To) != 2 || got.To[0] != "a@corp.io" || got.To[1] != "b@corp.io" {
		t.Fatalf("round-trip lost SMTP fields:\n got=%+v\nwant=%+v", got, want)
	}
}

// --- tiny in-process SMTP stub for the `none` send path ---

type smtpStub struct {
	ln       net.Listener
	mu       sync.Mutex
	mailFrom string
	rcpts    []string
	data     string
	failAt   string // command prefix at which to return a 5xx (e.g. "RCPT")
}

func newSMTPStub(t *testing.T, failAt string) *smtpStub {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &smtpStub{ln: ln, failAt: failAt}
	go s.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return s
}

func (s *smtpStub) addr() string { return s.ln.Addr().String() }

func (s *smtpStub) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *smtpStub) handle(conn net.Conn) {
	defer conn.Close()
	w := bufio.NewWriter(conn)
	r := bufio.NewReader(conn)
	write := func(line string) {
		fmt.Fprintf(w, "%s\r\n", line)
		_ = w.Flush()
	}
	write("220 stub ready")
	inData := false
	var dataBuf strings.Builder
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if inData {
			if line == "." {
				inData = false
				s.mu.Lock()
				s.data = dataBuf.String()
				s.mu.Unlock()
				write("250 ok")
				continue
			}
			dataBuf.WriteString(line + "\n")
			continue
		}
		up := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(up, "EHLO"), strings.HasPrefix(up, "HELO"):
			write("250 stub")
		case strings.HasPrefix(up, "MAIL"):
			if s.failAt == "MAIL" {
				write("550 no")
				continue
			}
			s.mu.Lock()
			s.mailFrom = line
			s.mu.Unlock()
			write("250 ok")
		case strings.HasPrefix(up, "RCPT"):
			if s.failAt == "RCPT" {
				write("550 no")
				continue
			}
			s.mu.Lock()
			s.rcpts = append(s.rcpts, line)
			s.mu.Unlock()
			write("250 ok")
		case strings.HasPrefix(up, "DATA"):
			write("354 go")
			inData = true
		case strings.HasPrefix(up, "QUIT"):
			write("221 bye")
			return
		default:
			write("250 ok")
		}
	}
}

func TestSendSMTPNonePath(t *testing.T) {
	stub := newSMTPStub(t, "")
	host, portStr, _ := net.SplitHostPort(stub.addr())
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	svc := &Service{now: time.Now}
	cfg := channelConfig{
		Host: host, Port: port, From: "janus@corp.io",
		To: []string{"ops@corp.io", "sec@corp.io"}, TLSMode: "none",
		Password: sentinelPassword,
	}
	if err := svc.sendSMTP(context.Background(), cfg, testPayload()); err != nil {
		t.Fatalf("sendSMTP: %v", err)
	}
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if !strings.Contains(stub.mailFrom, "janus@corp.io") {
		t.Fatalf("MAIL FROM not received: %q", stub.mailFrom)
	}
	if len(stub.rcpts) != 2 {
		t.Fatalf("expected 2 RCPT, got %d: %v", len(stub.rcpts), stub.rcpts)
	}
	if !strings.Contains(stub.data, "Subject: Janus: rotation failed") {
		t.Fatalf("delivered data missing subject: %q", stub.data)
	}
	// The delivered message never carries the password.
	if strings.Contains(stub.data, sentinelPassword) {
		t.Fatalf("delivered data leaked SMTP password: %q", stub.data)
	}
}

func TestSendSMTPServerErrorSurfaces(t *testing.T) {
	stub := newSMTPStub(t, "RCPT")
	host, portStr, _ := net.SplitHostPort(stub.addr())
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	svc := &Service{now: time.Now}
	cfg := channelConfig{
		Host: host, Port: port, From: "janus@corp.io",
		To: []string{"ops@corp.io"}, TLSMode: "none",
	}
	if err := svc.sendSMTP(context.Background(), cfg, testPayload()); err == nil {
		t.Fatal("expected a delivery error when the server rejects RCPT")
	}
}

func TestSendSMTPNoneSkipsAuth(t *testing.T) {
	// With TLSMode "none" and a username set, auth must be skipped (no encrypted
	// link). The stub does not advertise AUTH; delivery must still succeed.
	stub := newSMTPStub(t, "")
	host, portStr, _ := net.SplitHostPort(stub.addr())
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	svc := &Service{now: time.Now}
	cfg := channelConfig{
		Host: host, Port: port, From: "janus@corp.io", To: []string{"ops@corp.io"},
		TLSMode: "none", Username: "user", Password: sentinelPassword,
	}
	if err := svc.sendSMTP(context.Background(), cfg, testPayload()); err != nil {
		t.Fatalf("sendSMTP with username over none must skip auth and succeed: %v", err)
	}
}
