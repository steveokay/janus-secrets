package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// NotificationChannel is an outbound alerting destination. ConfigCT is the
// master-wrapped config blob (destination URL + optional HMAC key); the store
// stays crypto-blind and never sees the plaintext.
type NotificationChannel struct {
	ID        string
	Name      string
	Type      string // "webhook" | "slack"
	Enabled   bool
	Events    []string // subscribed event kinds
	ConfigCT  []byte
	CreatedBy string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NotificationDelivery is one queued fan-out of a matched audit event to a
// channel. Payload is a value-free rendering of the source event.
type NotificationDelivery struct {
	ID            string
	ChannelID     string
	AuditSeq      int64
	EventKind     string
	Payload       []byte // jsonb
	Status        string // "pending" | "delivered" | "failed"
	Attempts      int
	NextAttemptAt time.Time
	LastError     *string
	CreatedAt     time.Time
	DeliveredAt   *time.Time
}

// NotificationRepo persists channels, the delivery outbox, and the fan-out
// cursor. Crypto-blind: it stores the wrapped config blob verbatim.
type NotificationRepo struct{ s *Store }

// NewNotificationRepo returns a notification repository.
func NewNotificationRepo(s *Store) *NotificationRepo { return &NotificationRepo{s: s} }

const notifChannelCols = `id::text, name, type, enabled, events, config_ct, created_by, created_at, updated_at`

func scanChannel(row interface{ Scan(...any) error }) (*NotificationChannel, error) {
	var c NotificationChannel
	if err := row.Scan(&c.ID, &c.Name, &c.Type, &c.Enabled, &c.Events, &c.ConfigCT,
		&c.CreatedBy, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, mapError(err)
	}
	return &c, nil
}

// CreateChannel inserts a channel with a caller-supplied id (needed so the
// config blob's AAD can bind the id before insert).
func (r *NotificationRepo) CreateChannel(ctx context.Context, c *NotificationChannel) (*NotificationChannel, error) {
	row := r.s.pool.QueryRow(ctx,
		`INSERT INTO notification_channels (id, name, type, enabled, events, config_ct, created_by)
		 VALUES ($1::uuid,$2,$3,$4,$5,$6,$7) RETURNING `+notifChannelCols,
		c.ID, c.Name, c.Type, c.Enabled, c.Events, c.ConfigCT, c.CreatedBy)
	return scanChannel(row)
}

// GetChannel returns one channel by id.
func (r *NotificationRepo) GetChannel(ctx context.Context, id string) (*NotificationChannel, error) {
	return scanChannel(r.s.pool.QueryRow(ctx,
		`SELECT `+notifChannelCols+` FROM notification_channels WHERE id = $1::uuid`, id))
}

// ListChannels returns all channels, newest first.
func (r *NotificationRepo) ListChannels(ctx context.Context) ([]*NotificationChannel, error) {
	rows, err := r.s.pool.Query(ctx,
		`SELECT `+notifChannelCols+` FROM notification_channels ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []*NotificationChannel
	for rows.Next() {
		c, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, mapError(rows.Err())
}

// ListEnabledChannels returns only enabled channels (the dispatcher fan-out set).
func (r *NotificationRepo) ListEnabledChannels(ctx context.Context) ([]*NotificationChannel, error) {
	rows, err := r.s.pool.Query(ctx,
		`SELECT `+notifChannelCols+` FROM notification_channels WHERE enabled ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []*NotificationChannel
	for rows.Next() {
		c, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, mapError(rows.Err())
}

// UpdateChannel applies selective updates. Nil args leave a column unchanged;
// events is replaced when non-nil (including an explicit empty slice via a
// non-nil pointer).
func (r *NotificationRepo) UpdateChannel(ctx context.Context, id string, enabled *bool, events *[]string, configCT []byte) error {
	var ev any
	if events != nil {
		ev = *events
	}
	return r.s.execAffectingOne(ctx,
		`UPDATE notification_channels SET
		   enabled    = COALESCE($2, enabled),
		   events     = COALESCE($3, events),
		   config_ct  = COALESCE($4, config_ct),
		   updated_at = now()
		 WHERE id = $1::uuid`,
		id, enabled, ev, configCT)
}

// DeleteChannel removes a channel (its queued deliveries cascade).
func (r *NotificationRepo) DeleteChannel(ctx context.Context, id string) error {
	return r.s.execAffectingOne(ctx, `DELETE FROM notification_channels WHERE id = $1::uuid`, id)
}

// GetCursor returns the highest audit seq already scanned for fan-out.
func (r *NotificationRepo) GetCursor(ctx context.Context) (int64, error) {
	var seq int64
	err := r.s.pool.QueryRow(ctx, `SELECT last_seq FROM notification_cursor WHERE id = true`).Scan(&seq)
	if err != nil {
		return 0, mapError(err)
	}
	return seq, nil
}

// FanOut enqueues delivery rows for matched events and advances the cursor in
// one transaction, so a crash can never lose an event nor advance past an
// un-enqueued one. Delivery inserts are idempotent (ON CONFLICT DO NOTHING on
// the (channel_id, audit_seq) unique key).
func (r *NotificationRepo) FanOut(ctx context.Context, deliveries []NotificationDelivery, newCursor int64) error {
	return r.s.withTx(ctx, func(tx pgx.Tx) error {
		for _, d := range deliveries {
			if _, err := tx.Exec(ctx,
				`INSERT INTO notification_deliveries (channel_id, audit_seq, event_kind, payload)
				 VALUES ($1::uuid,$2,$3,$4)
				 ON CONFLICT (channel_id, audit_seq) DO NOTHING`,
				d.ChannelID, d.AuditSeq, d.EventKind, d.Payload); err != nil {
				return mapError(err)
			}
		}
		_, err := tx.Exec(ctx,
			`UPDATE notification_cursor SET last_seq = $1 WHERE id = true AND last_seq < $1`, newCursor)
		return mapError(err)
	})
}

const notifDeliveryCols = `id::text, channel_id::text, audit_seq, event_kind, payload, status, attempts, next_attempt_at, last_error, created_at, delivered_at`

func scanDelivery(row interface{ Scan(...any) error }) (*NotificationDelivery, error) {
	var d NotificationDelivery
	if err := row.Scan(&d.ID, &d.ChannelID, &d.AuditSeq, &d.EventKind, &d.Payload,
		&d.Status, &d.Attempts, &d.NextAttemptAt, &d.LastError, &d.CreatedAt, &d.DeliveredAt); err != nil {
		return nil, mapError(err)
	}
	return &d, nil
}

// ClaimDueDeliveries returns pending deliveries whose next_attempt_at has
// arrived, oldest-scheduled first.
func (r *NotificationRepo) ClaimDueDeliveries(ctx context.Context, now time.Time, limit int) ([]*NotificationDelivery, error) {
	rows, err := r.s.pool.Query(ctx,
		`SELECT `+notifDeliveryCols+` FROM notification_deliveries
		 WHERE status = 'pending' AND next_attempt_at <= $1
		 ORDER BY next_attempt_at ASC LIMIT $2`, now, limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []*NotificationDelivery
	for rows.Next() {
		d, err := scanDelivery(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, mapError(rows.Err())
}

// MarkDelivered marks a delivery successfully sent.
func (r *NotificationRepo) MarkDelivered(ctx context.Context, id string) error {
	return r.s.execAffectingOne(ctx,
		`UPDATE notification_deliveries
		    SET status='delivered', delivered_at=now(), attempts=attempts+1, last_error=NULL
		  WHERE id=$1::uuid`, id)
}

// RescheduleDelivery records a failed attempt and schedules a retry.
func (r *NotificationRepo) RescheduleDelivery(ctx context.Context, id string, nextAttemptAt time.Time, lastErr string) error {
	return r.s.execAffectingOne(ctx,
		`UPDATE notification_deliveries
		    SET attempts=attempts+1, next_attempt_at=$2, last_error=$3
		  WHERE id=$1::uuid`, id, nextAttemptAt, lastErr)
}

// FailDelivery marks a delivery permanently failed (retries exhausted).
func (r *NotificationRepo) FailDelivery(ctx context.Context, id string, lastErr string) error {
	return r.s.execAffectingOne(ctx,
		`UPDATE notification_deliveries
		    SET status='failed', attempts=attempts+1, last_error=$2
		  WHERE id=$1::uuid`, id, lastErr)
}

// ListDeliveriesByChannel returns a channel's recent deliveries, newest first.
func (r *NotificationRepo) ListDeliveriesByChannel(ctx context.Context, channelID string, limit int) ([]*NotificationDelivery, error) {
	rows, err := r.s.pool.Query(ctx,
		`SELECT `+notifDeliveryCols+` FROM notification_deliveries
		 WHERE channel_id = $1::uuid ORDER BY created_at DESC LIMIT $2`, channelID, limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []*NotificationDelivery
	for rows.Next() {
		d, err := scanDelivery(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, mapError(rows.Err())
}
