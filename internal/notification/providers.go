package notification

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
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
	if p.Resource != "" {
		fmt.Fprintf(&b, "\n• resource: `%s`", p.Resource)
	}
	if p.Actor != "" {
		fmt.Fprintf(&b, "\n• actor: `%s`", p.Actor)
	}
	if p.Detail != "" {
		fmt.Fprintf(&b, "\n• detail: %s", p.Detail)
	}
	return b.String()
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
