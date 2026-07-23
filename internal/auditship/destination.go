package auditship

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// batchItem is one serialized audit event: its wire record plus the pre-encoded
// JSON line. Destinations that frame per-event (syslog) read the record; those
// that ship the raw batch (webhook JSONL) read the line.
type batchItem struct {
	ev   shippedEvent
	line []byte
}

// encodeBatch serializes each shipped event to a compact single-line JSON
// object, returning the batch items in seq order.
func encodeBatch(evs []shippedEvent) ([]batchItem, error) {
	out := make([]batchItem, 0, len(evs))
	for _, e := range evs {
		line, err := json.Marshal(e)
		if err != nil {
			return nil, err
		}
		out = append(out, batchItem{ev: e, line: line})
	}
	return out, nil
}

// Destination is a SIEM sink for a batch of serialized audit events. A send is
// all-or-nothing: on nil the shipper advances its high-water mark; on error it
// leaves the mark and retries the same batch next tick (at-least-once).
type Destination interface {
	Send(ctx context.Context, batch []batchItem) error
	// Describe returns a value-free label for status/logging (never a URL/host
	// that might carry a token).
	Describe() string
}

// --- webhook -----------------------------------------------------------------

// webhookDest POSTs the batch as newline-delimited JSON (application/x-ndjson)
// to a configured URL, optionally signing the body with HMAC-SHA256.
type webhookDest struct {
	url     string
	hmacKey []byte
	hc      *http.Client
}

func newWebhookDest(url string, hmacKey []byte, timeout time.Duration) *webhookDest {
	return &webhookDest{url: url, hmacKey: hmacKey, hc: &http.Client{Timeout: timeout}}
}

func (d *webhookDest) Describe() string { return "webhook" }

func (d *webhookDest) Send(ctx context.Context, batch []batchItem) error {
	var buf bytes.Buffer
	for _, it := range batch {
		buf.Write(it.line)
		buf.WriteByte('\n')
	}
	body := buf.Bytes()
	// #nosec G107 -- the URL is operator-supplied config, validated at startup by
	// validateWebhookURL to be an absolute http(s) URL; it is not attacker-derived.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	req.Header.Set("User-Agent", "janus-audit-shipper")
	if len(d.hmacKey) > 0 {
		mac := hmac.New(sha256.New, d.hmacKey)
		mac.Write(body)
		req.Header.Set("X-Janus-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	resp, err := d.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("destination returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// --- syslog (RFC 5424, hand-rolled over net) ---------------------------------

// syslogDest writes each event as one RFC 5424 syslog message over UDP or TCP.
// It is hand-rolled over net (not stdlib log/syslog) so it builds on every
// platform, including Windows. TCP uses RFC 6587 octet-counting framing so a
// collector can delimit messages on the stream; UDP sends one datagram each.
type syslogDest struct {
	network  string // "udp" | "tcp"
	addr     string
	hostname string
	timeout  time.Duration
}

const (
	// facility 13 (log audit) << 3 | severity 5 (notice) = 109.
	syslogPriority = 13<<3 | 5
	syslogVersion  = "1"
	syslogAppName  = "janus"
	syslogMsgID    = "audit"
)

func newSyslogDest(network, addr string, timeout time.Duration) *syslogDest {
	host, _ := os.Hostname()
	if host == "" {
		host = "-"
	}
	return &syslogDest{network: network, addr: addr, hostname: stripCtl(host), timeout: timeout}
}

func (d *syslogDest) Describe() string { return "syslog/" + d.network }

// formatMessage renders one RFC 5424 message for a single event; the JSON line
// is the MSG (value-free by construction). PROCID carries the audit seq for
// traceability; STRUCTURED-DATA is "-".
//
//	PRI VERSION TIMESTAMP HOSTNAME APP-NAME PROCID MSGID STRUCTURED-DATA MSG
func (d *syslogDest) formatMessage(it batchItem) string {
	ts := it.ev.OccurredAt.UTC().Format(time.RFC3339Nano)
	return fmt.Sprintf("<%d>%s %s %s %s %d %s - %s",
		syslogPriority, syslogVersion, ts,
		d.hostname, syslogAppName, it.ev.Seq, syslogMsgID, it.line)
}

func (d *syslogDest) Send(ctx context.Context, batch []batchItem) error {
	dialer := &net.Dialer{Timeout: d.timeout}
	if dl, ok := ctx.Deadline(); ok {
		dialer.Deadline = dl
	}
	conn, err := dialer.DialContext(ctx, d.network, d.addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	if d.timeout > 0 {
		_ = conn.SetWriteDeadline(time.Now().Add(d.timeout))
	}
	for _, it := range batch {
		msg := d.formatMessage(it)
		var frame string
		if d.network == "tcp" {
			// RFC 6587 octet-counting: "<len> <msg>".
			frame = fmt.Sprintf("%d %s", len(msg), msg)
		} else {
			frame = msg // one datagram per message
		}
		if _, err := conn.Write([]byte(frame)); err != nil {
			return err
		}
	}
	return nil
}

// stripCtl replaces spaces and control characters that would break a single
// syslog header field; the JSON MSG is already control-free (json.Marshal
// escapes control runes).
func stripCtl(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == ' ' || r < 0x20 {
			return '_'
		}
		return r
	}, s)
}
