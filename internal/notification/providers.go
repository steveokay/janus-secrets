package notification

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"strings"
	"time"
)

// send dispatches one rendered event to a channel by type. body is the
// value-free JSON payload; for Slack it is re-shaped into Slack's message
// format. A non-2xx response or transport error is returned as an error.
func (s *Service) send(ctx context.Context, chanType string, cfg channelConfig, p eventPayload, body []byte) error {
	switch chanType {
	case "webhook":
		return s.postWebhook(ctx, cfg, body)
	case "slack":
		return s.postSlack(ctx, cfg, p)
	case "smtp":
		return s.sendSMTP(ctx, cfg, p)
	default:
		return fmt.Errorf("unknown channel type %q", chanType)
	}
}

// postWebhook POSTs the raw JSON payload, optionally signed with an HMAC-SHA256
// header (`X-Janus-Signature: sha256=<hex>`) so the receiver can verify origin.
func (s *Service) postWebhook(ctx context.Context, cfg channelConfig, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "janus-notifications")
	if cfg.HMACKey != "" {
		mac := hmac.New(sha256.New, []byte(cfg.HMACKey))
		mac.Write(body)
		req.Header.Set("X-Janus-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	return s.do(req)
}

// postSlack POSTs a Slack incoming-webhook message ({"text": ...}) rendered
// from the event.
func (s *Service) postSlack(ctx context.Context, cfg channelConfig, p eventPayload) error {
	msg := map[string]string{"text": slackText(p)}
	body, _ := json.Marshal(msg)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "janus-notifications")
	return s.do(req)
}

func (s *Service) do(req *http.Request) error {
	resp, err := s.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("channel returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// detailLine is one value-free field of the rendered message. label/value carry
// only event metadata (kind, resource path, actor, category detail) — never a
// secret value, because the source payload is value-free by construction.
type detailLine struct{ label, value string }

// messageLines returns the value-free detail lines shared by every renderer
// (Slack, email, …). Only present fields are emitted.
func messageLines(p eventPayload) []detailLine {
	var lines []detailLine
	if p.Resource != "" {
		lines = append(lines, detailLine{"resource", p.Resource})
	}
	if p.Actor != "" {
		lines = append(lines, detailLine{"actor", p.Actor})
	}
	if p.Detail != "" {
		lines = append(lines, detailLine{"detail", p.Detail})
	}
	return lines
}

// slackText renders a compact human message. Values never appear (the payload
// is value-free); only the event kind, resource path, and category detail.
func slackText(p eventPayload) string {
	icon := ":bell:"
	switch p.Event {
	case EventRotationFailed, EventSyncFailed:
		icon = ":rotating_light:"
	case EventAccessDenied:
		icon = ":no_entry:"
	case EventPromotionPending:
		icon = ":hourglass:"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s *Janus: %s*", icon, humanKind(p.Event))
	for _, l := range messageLines(p) {
		if l.label == "detail" {
			fmt.Fprintf(&b, "\n• %s: %s", l.label, l.value)
		} else {
			fmt.Fprintf(&b, "\n• %s: `%s`", l.label, l.value)
		}
	}
	return b.String()
}

// emailBody renders the plain-text email body — value-free, mirroring slackText
// without Slack markup.
func emailBody(p eventPayload) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Janus: %s\r\n", humanKind(p.Event))
	for _, l := range messageLines(p) {
		fmt.Fprintf(&b, "%s: %s\r\n", l.label, l.value)
	}
	return b.String()
}

// stripHeader removes CR/LF from a value destined for a message header — a
// defensive header-injection guard even though the event kind and configured
// addresses are already validated on write.
func stripHeader(s string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(s)
}

// buildMessage renders an RFC 5322 plain-text email for the event. PURE (no
// I/O), so it is directly unit-testable. CRLF line endings; header-injection
// guarded on the From/To/Subject inputs. The body is value-free.
func buildMessage(cfg channelConfig, p eventPayload) []byte {
	from := stripHeader(cfg.From)
	to := make([]string, 0, len(cfg.To))
	for _, r := range cfg.To {
		to = append(to, stripHeader(r))
	}
	subject := stripHeader("Janus: " + humanKind(p.Event))
	date := p.OccurredAt
	if date.IsZero() {
		date = time.Now()
	}

	var b bytes.Buffer
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(to, ", "))
	fmt.Fprintf(&b, "Subject: %s\r\n", subject)
	fmt.Fprintf(&b, "Date: %s\r\n", date.UTC().Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(emailBody(p))
	return b.Bytes()
}

// sendSMTP delivers one event by email using stdlib net/smtp. The connection is
// established per tls_mode; auth (PLAIN) is attempted only over an encrypted
// connection when a username is set. Any protocol/transport error is returned so
// the outbox retries with backoff.
func (s *Service) sendSMTP(ctx context.Context, cfg channelConfig, p eventPayload) error {
	mode := cfg.TLSMode
	if mode == "" {
		mode = "starttls"
	}
	addr := net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", cfg.Port))
	// #nosec G402 -- InsecureSkipVerify is an explicit, per-channel opt-in
	// (documented footgun) for self-hosted relays with private/self-signed CAs;
	// it is off unless the operator sets insecure_skip_verify on the channel.
	tlsCfg := &tls.Config{ServerName: cfg.Host, InsecureSkipVerify: cfg.InsecureSkipVerify}

	dialer := &net.Dialer{}
	if deadline, ok := ctx.Deadline(); ok {
		dialer.Deadline = deadline
	}
	// Bound the connect attempt even when the request ctx has no deadline (the
	// /test endpoint runs with HTTPWriteTimeout=0), so a dial to an unreachable
	// host can't block the goroutine indefinitely. Webhook/Slack are already
	// bounded by the http client timeout; this gives SMTP the same guarantee.
	if dialer.Deadline.IsZero() {
		dialer.Timeout = 15 * time.Second
	}

	var client *smtp.Client
	var err error
	switch mode {
	case "implicit":
		var conn net.Conn
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, tlsCfg)
		if err != nil {
			return err
		}
		client, err = smtp.NewClient(conn, cfg.Host)
		if err != nil {
			_ = conn.Close()
			return err
		}
	default: // "starttls" and "none"
		var conn net.Conn
		conn, err = dialer.DialContext(ctx, "tcp", addr)
		if err != nil {
			return err
		}
		client, err = smtp.NewClient(conn, cfg.Host)
		if err != nil {
			_ = conn.Close()
			return err
		}
		if mode == "starttls" {
			if err = client.StartTLS(tlsCfg); err != nil {
				_ = client.Close()
				return err
			}
		}
	}
	defer client.Close()

	// Authenticate only over an encrypted link (mode != none) when a username is
	// set. The stdlib PlainAuth also refuses PLAIN over cleartext.
	if cfg.Username != "" && mode != "none" {
		if err = client.Auth(smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)); err != nil {
			return err
		}
	}

	if err = client.Mail(cfg.From); err != nil {
		return err
	}
	for _, rcpt := range cfg.To {
		if err = client.Rcpt(rcpt); err != nil {
			return err
		}
	}
	wc, err := client.Data()
	if err != nil {
		return err
	}
	if _, err = wc.Write(buildMessage(cfg, p)); err != nil {
		_ = wc.Close()
		return err
	}
	if err = wc.Close(); err != nil {
		return err
	}
	return client.Quit()
}

func humanKind(k string) string {
	switch k {
	case EventRotationFailed:
		return "rotation failed"
	case EventSyncFailed:
		return "sync failed"
	case EventPromotionPending:
		return "promotion awaiting approval"
	case EventAccessDenied:
		return "access denied"
	case "test":
		return "test notification"
	default:
		return k
	}
}
