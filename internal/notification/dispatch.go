package notification

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/steveokay/janus-secrets/internal/store"
)

const (
	fanoutBatch   = 200 // audit rows scanned per tick
	deliverBatch  = 50  // deliveries attempted per tick
	maxAttempts   = 6   // give up after this many failed sends
	backoffBase   = time.Minute
	backoffCapDur = time.Hour
)

// RunScheduler ticks the dispatcher until ctx is cancelled. A zero tick
// disables it (tests, or JANUS_NOTIFY_TICK=0).
func (s *Service) RunScheduler(ctx context.Context, tick time.Duration) {
	if tick <= 0 {
		return
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	s.logger.Info("notification dispatcher started", "tick", tick.String())
	for {
		select {
		case <-ctx.Done():
			s.logger.Info("notification dispatcher stopping")
			return
		case <-t.C:
			s.RunDue(ctx)
		}
	}
}

// RunDue runs one dispatcher pass: fan matched audit events into the outbox,
// then attempt due deliveries. Sealed = clean no-op (config decryption and the
// audit tail both need an unsealed keyring / running instance).
func (s *Service) RunDue(ctx context.Context) {
	if s.kr.Sealed() {
		return
	}
	s.fanOut(ctx)
	s.deliver(ctx)
}

// fanOut tails the audit log from the cursor and enqueues a delivery per
// (matched event × subscribing enabled channel), advancing the cursor in the
// same transaction as the inserts.
func (s *Service) fanOut(ctx context.Context) {
	cursor, err := s.repo.GetCursor(ctx)
	if err != nil {
		s.logger.Warn("notification cursor read failed", "err", err)
		return
	}
	events, err := s.audit.ListSince(ctx, cursor, fanoutBatch)
	if err != nil {
		s.logger.Warn("notification audit tail failed", "err", err)
		return
	}
	if len(events) == 0 {
		return
	}
	channels, err := s.repo.ListEnabledChannels(ctx)
	if err != nil {
		s.logger.Warn("notification channel list failed", "err", err)
		return
	}

	var deliveries []store.NotificationDelivery
	maxSeq := cursor
	for _, ev := range events {
		if ev.Seq > maxSeq {
			maxSeq = ev.Seq
		}
		kind := classify(ev)
		if kind == "" {
			continue
		}
		for _, ch := range channels {
			if !slices.Contains(ch.Events, kind) {
				continue
			}
			body, err := json.Marshal(payloadFor(kind, ev))
			if err != nil {
				continue
			}
			deliveries = append(deliveries, store.NotificationDelivery{
				ChannelID: ch.ID, AuditSeq: ev.Seq, EventKind: kind, Payload: body,
			})
		}
	}
	if err := s.repo.FanOut(ctx, deliveries, maxSeq); err != nil {
		s.logger.Warn("notification fan-out failed", "err", err)
	}
}

// deliver attempts each due delivery, marking it delivered or rescheduling with
// exponential backoff (permanently failing after maxAttempts).
func (s *Service) deliver(ctx context.Context) {
	due, err := s.repo.ClaimDueDeliveries(ctx, s.now(), deliverBatch)
	if err != nil {
		s.logger.Warn("notification claim-due failed", "err", err)
		return
	}
	channels := map[string]*store.NotificationChannel{}
	configs := map[string]channelConfig{}
	for _, d := range due {
		if ctx.Err() != nil {
			return
		}
		ch, ok := channels[d.ChannelID]
		if !ok {
			ch, err = s.repo.GetChannel(ctx, d.ChannelID)
			if err != nil {
				// Channel gone (deleted): the CASCADE removes deliveries too, so
				// this is a race; skip.
				continue
			}
			channels[d.ChannelID] = ch
			cfg, cErr := s.unwrapConfig(ch)
			if cErr != nil {
				s.reschedule(ctx, d, "config decrypt failed")
				continue
			}
			configs[d.ChannelID] = cfg
		}
		if !ch.Enabled {
			_ = s.repo.FailDelivery(ctx, d.ID, "channel disabled")
			continue
		}
		// The stored payload is a marshalled eventPayload (see fanOut). Decode it so
		// the SMTP/Slack renderers — which render exclusively from p — produce a
		// populated message; a zero-value eventPayload{} would render an empty
		// subject and body. (The raw bytes are still passed as body for webhook.)
		var p eventPayload
		if err := json.Unmarshal(d.Payload, &p); err != nil {
			s.reschedule(ctx, d, "payload decode failed")
			continue
		}
		if err := s.send(ctx, ch.Type, configs[d.ChannelID], p, d.Payload); err != nil {
			s.reschedule(ctx, d, sanitize(err))
			continue
		}
		if err := s.repo.MarkDelivered(ctx, d.ID); err != nil {
			s.logger.Warn("notification mark-delivered failed", "err", err)
		}
	}
}

// reschedule records a failed attempt, backing off, or fails permanently once
// attempts are exhausted.
func (s *Service) reschedule(ctx context.Context, d *store.NotificationDelivery, reason string) {
	if d.Attempts+1 >= maxAttempts {
		_ = s.repo.FailDelivery(ctx, d.ID, reason)
		return
	}
	_ = s.repo.RescheduleDelivery(ctx, d.ID, s.now().Add(backoff(d.Attempts+1)), reason)
}

// backoff returns 1m, 2m, 4m, … capped at 1h.
func backoff(attempt int) time.Duration {
	d := backoffBase << (attempt - 1)
	if d <= 0 || d > backoffCapDur {
		return backoffCapDur
	}
	return d
}

// sanitize reduces a delivery error to a coarse category so a stored/displayed
// last_error can never carry a channel URL (which may embed a bearer token) or
// other host detail. HTTP-status messages we produce ourselves are safe to keep.
func sanitize(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "HTTP "):
		return msg // "channel returned HTTP 500" — no URL, safe
	case strings.Contains(msg, "context deadline") || strings.Contains(msg, "Timeout"):
		return "delivery timed out"
	default:
		return "connection failed"
	}
}
