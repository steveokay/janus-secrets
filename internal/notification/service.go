package notification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/store"
)

// Sentinel errors mapped to HTTP status by the API layer.
var (
	ErrValidation = errors.New("notification: validation")
	ErrNotFound   = errors.New("notification: not found")
	ErrConflict   = errors.New("notification: name already in use")
)

// Service manages notification channels and runs the delivery dispatcher.
type Service struct {
	kr     *crypto.Keyring
	repo   *store.NotificationRepo
	audit  *store.AuditRepo // read-only tail of the audit log
	st     *store.Store
	logger *slog.Logger
	hc     *http.Client
	now    func() time.Time
}

// New constructs a notification service.
func New(kr *crypto.Keyring, st *store.Store, aud *store.AuditRepo, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		kr:     kr,
		repo:   store.NewNotificationRepo(st),
		audit:  aud,
		st:     st,
		logger: logger,
		hc:     &http.Client{Timeout: 15 * time.Second},
		now:    time.Now,
	}
}

// channelConfig is the secret part of a channel, encrypted at rest.
type channelConfig struct {
	URL     string `json:"url"`
	HMACKey string `json:"hmac_key,omitempty"` // webhook signing key (optional)
}

// ChannelInput is the create/update payload from the API/CLI. On update, URL and
// HMACKey are only applied when non-nil (a nil pointer leaves the stored secret).
type ChannelInput struct {
	Name      string
	Type      string
	Events    []string
	URL       string
	HMACKey   string
	CreatedBy string
}

// ChannelView is the masked, value-free projection returned to callers — never
// the URL or HMAC key.
type ChannelView struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	Enabled   bool      `json:"enabled"`
	Events    []string  `json:"events"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func view(c *store.NotificationChannel) *ChannelView {
	ev := c.Events
	if ev == nil {
		ev = []string{}
	}
	return &ChannelView{
		ID: c.ID, Name: c.Name, Type: c.Type, Enabled: c.Enabled, Events: ev,
		CreatedBy: c.CreatedBy, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
}

// validateType, validateEvents, validateURL enforce the write contract.
func validateChannel(typ string, events []string, u string) error {
	if typ != "webhook" && typ != "slack" {
		return fmt.Errorf("%w: type must be webhook or slack", ErrValidation)
	}
	if len(events) == 0 {
		return fmt.Errorf("%w: at least one event kind is required", ErrValidation)
	}
	for _, e := range events {
		if !isKnownKind(e) {
			return fmt.Errorf("%w: unknown event kind %q", ErrValidation, e)
		}
	}
	return validateURL(u)
}

func validateURL(u string) error {
	parsed, err := url.Parse(u)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("%w: url must be an absolute http(s) URL", ErrValidation)
	}
	return nil
}

// CreateChannel validates, encrypts the config under the master key (AAD-bound
// to the new id), and stores the channel.
func (s *Service) CreateChannel(ctx context.Context, in ChannelInput) (*ChannelView, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, fmt.Errorf("%w: name is required", ErrValidation)
	}
	if err := validateChannel(in.Type, in.Events, in.URL); err != nil {
		return nil, err
	}
	if in.Type == "slack" && in.HMACKey != "" {
		return nil, fmt.Errorf("%w: hmac_key applies to webhook channels only", ErrValidation)
	}
	id, err := s.st.NewID(ctx)
	if err != nil {
		return nil, err
	}
	ct, err := s.wrapConfig(id, channelConfig{URL: in.URL, HMACKey: in.HMACKey})
	if err != nil {
		return nil, err
	}
	c, err := s.repo.CreateChannel(ctx, &store.NotificationChannel{
		ID: id, Name: in.Name, Type: in.Type, Enabled: true,
		Events: in.Events, ConfigCT: ct, CreatedBy: in.CreatedBy,
	})
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return view(c), nil
}

// GetChannel returns the masked view of one channel.
func (s *Service) GetChannel(ctx context.Context, id string) (*ChannelView, error) {
	c, err := s.repo.GetChannel(ctx, id)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return view(c), nil
}

// ListChannels returns all channels, masked.
func (s *Service) ListChannels(ctx context.Context) ([]*ChannelView, error) {
	cs, err := s.repo.ListChannels(ctx)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	out := make([]*ChannelView, 0, len(cs))
	for _, c := range cs {
		out = append(out, view(c))
	}
	return out, nil
}

// UpdateChannel applies selective changes. enabled/events are replaced when
// non-nil; url/hmac (when urlSet) re-encrypt the config blob.
func (s *Service) UpdateChannel(ctx context.Context, id string, enabled *bool, events *[]string, urlSet bool, u, hmacKey string) (*ChannelView, error) {
	existing, err := s.repo.GetChannel(ctx, id)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	if events != nil {
		if err := validateChannel(existing.Type, *events, urlOrPlaceholder(u, urlSet)); err != nil {
			return nil, err
		}
	}
	var ct []byte
	if urlSet {
		if err := validateURL(u); err != nil {
			return nil, err
		}
		if existing.Type == "slack" && hmacKey != "" {
			return nil, fmt.Errorf("%w: hmac_key applies to webhook channels only", ErrValidation)
		}
		if ct, err = s.wrapConfig(id, channelConfig{URL: u, HMACKey: hmacKey}); err != nil {
			return nil, err
		}
	}
	if err := s.repo.UpdateChannel(ctx, id, enabled, events, ct); err != nil {
		return nil, mapStoreErr(err)
	}
	c, err := s.repo.GetChannel(ctx, id)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return view(c), nil
}

// urlOrPlaceholder lets event-only updates skip URL validation (a valid stored
// URL already exists) while still validating when the URL is being changed.
func urlOrPlaceholder(u string, urlSet bool) string {
	if urlSet {
		return u
	}
	return "https://placeholder.invalid/kept"
}

// DeleteChannel removes a channel and its queued deliveries.
func (s *Service) DeleteChannel(ctx context.Context, id string) error {
	if err := s.repo.DeleteChannel(ctx, id); err != nil {
		return mapStoreErr(err)
	}
	return nil
}

// TestChannel delivers a synthetic notification synchronously so the operator
// gets immediate feedback (it bypasses the outbox). Returns a delivery error
// verbatim-sanitized.
func (s *Service) TestChannel(ctx context.Context, id string) error {
	c, err := s.repo.GetChannel(ctx, id)
	if err != nil {
		return mapStoreErr(err)
	}
	cfg, err := s.unwrapConfig(c)
	if err != nil {
		return err
	}
	p := eventPayload{
		Event: "test", Seq: 0, OccurredAt: s.now().UTC(),
		Action: "notification.test", Result: "success",
		Resource: "notification/channels/" + c.ID, Actor: c.CreatedBy,
		Detail: "test notification from Janus",
	}
	body, _ := json.Marshal(p)
	return s.send(ctx, c.Type, cfg, p, body)
}

// DeliveryView is the value-free history projection.
type DeliveryView struct {
	ID            string     `json:"id"`
	EventKind     string     `json:"event_kind"`
	Status        string     `json:"status"`
	Attempts      int        `json:"attempts"`
	NextAttemptAt time.Time  `json:"next_attempt_at"`
	LastError     string     `json:"last_error,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	DeliveredAt   *time.Time `json:"delivered_at,omitempty"`
}

// ListDeliveries returns a channel's recent delivery history (value-free).
func (s *Service) ListDeliveries(ctx context.Context, channelID string, limit int) ([]*DeliveryView, error) {
	if _, err := s.repo.GetChannel(ctx, channelID); err != nil {
		return nil, mapStoreErr(err)
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	ds, err := s.repo.ListDeliveriesByChannel(ctx, channelID, limit)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	out := make([]*DeliveryView, 0, len(ds))
	for _, d := range ds {
		dv := &DeliveryView{
			ID: d.ID, EventKind: d.EventKind, Status: d.Status, Attempts: d.Attempts,
			NextAttemptAt: d.NextAttemptAt.UTC(), CreatedAt: d.CreatedAt.UTC(), DeliveredAt: d.DeliveredAt,
		}
		if d.LastError != nil {
			dv.LastError = *d.LastError
		}
		out = append(out, dv)
	}
	return out, nil
}

func (s *Service) wrapConfig(id string, cfg channelConfig) ([]byte, error) {
	pt, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	ct, err := s.kr.WrapNotificationConfig(id, pt)
	if err != nil {
		return nil, err
	}
	return ct.Marshal(), nil
}

func (s *Service) unwrapConfig(c *store.NotificationChannel) (channelConfig, error) {
	ct, err := crypto.ParseCiphertext(c.ConfigCT)
	if err != nil {
		return channelConfig{}, err
	}
	pt, err := s.kr.UnwrapNotificationConfig(c.ID, ct)
	if err != nil {
		return channelConfig{}, err
	}
	var cfg channelConfig
	if err := json.Unmarshal(pt, &cfg); err != nil {
		return channelConfig{}, err
	}
	return cfg, nil
}

func mapStoreErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, store.ErrConflict):
		return ErrConflict
	default:
		return err
	}
}
