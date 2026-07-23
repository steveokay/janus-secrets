// Package auditship streams the (value-free) audit log to an external SIEM
// destination — a webhook or a syslog collector — as newline-delimited JSON.
//
// It mirrors the notification dispatcher's audit-tailing pattern
// (internal/notification/dispatch.go): each tick it reads events since its own
// durable HIGH-WATER MARK via audit.ListSince, serializes them as JSONL, sends
// the batch, and advances the mark ONLY on a successful send. A failed send
// leaves the mark so the next tick retries from the same seq — at-least-once
// delivery with no gaps. The shipper keeps its OWN cursor, separate from the
// notification cursor, so the two tail the log independently.
//
// Audit events carry no secret values by construction, so shipping them leaks
// nothing; the only secret involved (the webhook HMAC key) is env-supplied and
// never logged or persisted.
package auditship

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/steveokay/janus-secrets/internal/store"
)

// shipBatch bounds the number of audit rows read + shipped per tick.
const shipBatch = 500

// auditTailer is the read side of the audit log the shipper tails.
type auditTailer interface {
	ListSince(ctx context.Context, afterSeq int64, limit int) ([]store.AuditRow, error)
}

// markStore persists the durable high-water mark.
type markStore interface {
	GetHighWater(ctx context.Context) (int64, error)
	AdvanceHighWater(ctx context.Context, newSeq int64) error
}

// Status is the value-free operational snapshot surfaced by /v1/sys/status.
type Status struct {
	Mode          string     `json:"mode"`
	Destination   string     `json:"destination,omitempty"`
	HighWaterSeq  int64      `json:"high_water_seq"`
	LastShipAt    *time.Time `json:"last_ship_at,omitempty"`
	LastShipCount int        `json:"last_ship_count"`
	LastError     string     `json:"last_error,omitempty"`
}

// Service tails the audit log and ships new events to the configured
// destination.
type Service struct {
	cfg    Config
	dest   Destination
	audit  auditTailer
	marks  markStore
	logger *slog.Logger
	now    func() time.Time

	mu            sync.Mutex
	lastShipAt    *time.Time
	lastShipCount int
	lastErr       string
	highWater     int64
}

// New constructs a shipper. A disabled config (Mode=off) yields a nil dest; the
// service is still safe to construct and RunDue is a clean no-op.
func New(cfg Config, aud *store.AuditRepo, marks *store.AuditShipRepo, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return newService(cfg, aud, marks, logger)
}

// newService is the interface-typed constructor (unit tests supply fakes).
func newService(cfg Config, aud auditTailer, marks markStore, logger *slog.Logger) *Service {
	s := &Service{cfg: cfg, audit: aud, marks: marks, logger: logger, now: time.Now}
	switch cfg.Mode {
	case ModeWebhook:
		s.dest = newWebhookDest(cfg.WebhookURL, []byte(cfg.WebhookHMACKey), cfg.SendTimeout)
	case ModeSyslog:
		s.dest = newSyslogDest(cfg.SyslogNetwork, cfg.SyslogAddr, cfg.SendTimeout)
	}
	return s
}

// RunScheduler ticks the shipper until ctx is cancelled. A zero tick or a
// disabled config disables it.
func (s *Service) RunScheduler(ctx context.Context, tick time.Duration) {
	if tick <= 0 || !s.cfg.Enabled() {
		return
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	s.logger.Info("audit shipper started", "mode", string(s.cfg.Mode), "destination", s.dest.Describe(), "tick", tick.String())
	for {
		select {
		case <-ctx.Done():
			s.logger.Info("audit shipper stopping")
			return
		case <-t.C:
			s.RunDue(ctx)
		}
	}
}

// RunDue runs one shipping pass: read events since the high-water mark, send the
// batch, and advance the mark on success. Disabled config = clean no-op.
func (s *Service) RunDue(ctx context.Context) {
	if !s.cfg.Enabled() || s.dest == nil {
		return
	}
	mark, err := s.marks.GetHighWater(ctx)
	if err != nil {
		s.recordErr("high-water read failed")
		s.logger.Warn("audit shipper high-water read failed", "err", err)
		return
	}
	s.setHighWater(mark)
	events, err := s.audit.ListSince(ctx, mark, shipBatch)
	if err != nil {
		s.recordErr("audit tail failed")
		s.logger.Warn("audit shipper tail failed", "err", err)
		return
	}
	if len(events) == 0 {
		return
	}

	shipped := make([]shippedEvent, 0, len(events))
	maxSeq := mark
	for _, ev := range events {
		if ev.Seq > maxSeq {
			maxSeq = ev.Seq
		}
		shipped = append(shipped, toShipped(ev))
	}
	batch, err := encodeBatch(shipped)
	if err != nil {
		s.recordErr("serialize failed")
		s.logger.Warn("audit shipper serialize failed", "err", err)
		return
	}

	if err := s.dest.Send(ctx, batch); err != nil {
		// Leave the mark untouched: the same batch retries next tick (no gap).
		s.recordErr(sanitize(err))
		s.logger.Warn("audit shipper send failed", "destination", s.dest.Describe(), "err", sanitize(err))
		return
	}

	// Advance only after a successful send (at-least-once).
	if err := s.marks.AdvanceHighWater(ctx, maxSeq); err != nil {
		s.recordErr("high-water advance failed")
		s.logger.Warn("audit shipper high-water advance failed", "err", err)
		return
	}
	s.recordSuccess(maxSeq, len(shipped))
}

// Status returns the value-free operational snapshot.
func (s *Service) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{
		Mode:          string(s.cfg.Mode),
		HighWaterSeq:  s.highWater,
		LastShipAt:    s.lastShipAt,
		LastShipCount: s.lastShipCount,
		LastError:     s.lastErr,
	}
	if s.dest != nil {
		st.Destination = s.dest.Describe()
	}
	return st
}

func (s *Service) recordSuccess(seq int64, count int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	s.lastShipAt = &now
	s.lastShipCount = count
	s.lastErr = ""
	s.highWater = seq
}

func (s *Service) recordErr(msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastErr = msg
}

func (s *Service) setHighWater(seq int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.highWater = seq
}

// sanitize reduces a send error to a coarse category so a stored/displayed
// last_error can never carry a destination URL (which may embed a token) or host
// detail. HTTP-status messages we produce ourselves are safe to keep.
func sanitize(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "HTTP "):
		return msg // "destination returned HTTP 500" — no URL, safe
	case strings.Contains(msg, "context deadline") || strings.Contains(msg, "Timeout") || strings.Contains(msg, "timeout"):
		return "send timed out"
	default:
		return "connection failed"
	}
}
