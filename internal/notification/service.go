package notification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/mail"
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
	URL     string `json:"url,omitempty"`
	HMACKey string `json:"hmac_key,omitempty"` // webhook signing key (optional)

	// SMTP fields (type == "smtp"). Password is write-only. All omitempty so a
	// webhook/slack config round-trips unchanged.
	Host               string   `json:"host,omitempty"`
	Port               int      `json:"port,omitempty"`
	From               string   `json:"from,omitempty"`
	To                 []string `json:"to,omitempty"`
	Username           string   `json:"username,omitempty"`
	Password           string   `json:"password,omitempty"`
	TLSMode            string   `json:"tls_mode,omitempty"` // "starttls" | "implicit" | "none"
	InsecureSkipVerify bool     `json:"insecure_skip_verify,omitempty"`
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

	// SMTP inputs (type == "smtp").
	SMTPHost               string
	SMTPPort               int
	SMTPFrom               string
	SMTPTo                 []string
	SMTPUsername           string
	SMTPPassword           string
	SMTPTLSMode            string
	SMTPInsecureSkipVerify bool
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

// validateType enforces the allowed channel types.
func validateType(typ string) error {
	switch typ {
	case "webhook", "slack", "smtp":
		return nil
	default:
		return fmt.Errorf("%w: type must be webhook, slack or smtp", ErrValidation)
	}
}

// validateEvents requires at least one known event kind.
func validateEvents(events []string) error {
	if len(events) == 0 {
		return fmt.Errorf("%w: at least one event kind is required", ErrValidation)
	}
	for _, e := range events {
		if !isKnownKind(e) {
			return fmt.Errorf("%w: unknown event kind %q", ErrValidation, e)
		}
	}
	return nil
}

// validateChannel enforces the write contract for webhook/slack channels: type,
// events, and an absolute http(s) URL. SMTP channels validate via validateSMTP.
func validateChannel(typ string, events []string, u string) error {
	if err := validateType(typ); err != nil {
		return err
	}
	if err := validateEvents(events); err != nil {
		return err
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

// normalizeTLSMode returns the effective tls_mode, defaulting empty to
// "starttls", and an error for an unrecognized value.
func normalizeTLSMode(mode string) (string, error) {
	switch mode {
	case "":
		return "starttls", nil
	case "starttls", "implicit", "none":
		return mode, nil
	default:
		return "", fmt.Errorf("%w: tls_mode must be starttls, implicit or none", ErrValidation)
	}
}

// validateAddress ensures a single RFC 5322 address that carries no CR/LF
// (header-injection guard); it returns the parsed address unchanged.
func validateAddress(kind, addr string) error {
	if strings.ContainsAny(addr, "\r\n") {
		return fmt.Errorf("%w: %s address must not contain CR or LF", ErrValidation, kind)
	}
	if _, err := mail.ParseAddress(addr); err != nil {
		return fmt.Errorf("%w: %s is not a valid email address", ErrValidation, kind)
	}
	return nil
}

// validateSMTP enforces the write contract for smtp channels. hmacKeySet reports
// whether an hmac_key was supplied (webhook-only → rejected).
func validateSMTP(in ChannelInput, hmacKeySet bool) error {
	if err := validateEvents(in.Events); err != nil {
		return err
	}
	if hmacKeySet {
		return fmt.Errorf("%w: hmac_key applies to webhook channels only", ErrValidation)
	}
	if strings.TrimSpace(in.SMTPHost) == "" {
		return fmt.Errorf("%w: smtp host is required", ErrValidation)
	}
	if strings.ContainsAny(in.SMTPHost, "\r\n") {
		return fmt.Errorf("%w: smtp host must not contain CR or LF", ErrValidation)
	}
	if in.SMTPPort < 1 || in.SMTPPort > 65535 {
		return fmt.Errorf("%w: smtp port must be in 1..65535", ErrValidation)
	}
	if err := validateAddress("from", in.SMTPFrom); err != nil {
		return err
	}
	if len(in.SMTPTo) == 0 {
		return fmt.Errorf("%w: at least one smtp recipient is required", ErrValidation)
	}
	for _, to := range in.SMTPTo {
		if err := validateAddress("to", to); err != nil {
			return err
		}
	}
	if _, err := normalizeTLSMode(in.SMTPTLSMode); err != nil {
		return err
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
	if err := validateType(in.Type); err != nil {
		return nil, err
	}
	var cfg channelConfig
	if in.Type == "smtp" {
		if err := validateSMTP(in, in.HMACKey != ""); err != nil {
			return nil, err
		}
		mode, _ := normalizeTLSMode(in.SMTPTLSMode)
		cfg = channelConfig{
			Host: in.SMTPHost, Port: in.SMTPPort, From: in.SMTPFrom, To: in.SMTPTo,
			Username: in.SMTPUsername, Password: in.SMTPPassword,
			TLSMode: mode, InsecureSkipVerify: in.SMTPInsecureSkipVerify,
		}
	} else {
		if err := validateChannel(in.Type, in.Events, in.URL); err != nil {
			return nil, err
		}
		if in.Type == "slack" && in.HMACKey != "" {
			return nil, fmt.Errorf("%w: hmac_key applies to webhook channels only", ErrValidation)
		}
		cfg = channelConfig{URL: in.URL, HMACKey: in.HMACKey}
	}
	id, err := s.st.NewID(ctx)
	if err != nil {
		return nil, err
	}
	ct, err := s.wrapConfig(id, cfg)
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

// ChannelConfigUpdate carries an optional transport-config replacement for
// UpdateChannel. When Set is false the stored (wrapped) config is left intact.
// For webhook/slack, URL/HMACKey are used; for smtp, the SMTP* fields are used.
type ChannelConfigUpdate struct {
	Set bool

	// webhook/slack
	URL     string
	HMACKey string

	// smtp
	SMTPHost               string
	SMTPPort               int
	SMTPFrom               string
	SMTPTo                 []string
	SMTPUsername           string
	SMTPPassword           string
	SMTPTLSMode            string
	SMTPInsecureSkipVerify bool
}

// UpdateChannel applies selective changes. enabled/events are replaced when
// non-nil; when cfg.Set the config blob is re-encrypted from the supplied
// transport config (validated against the channel's existing type).
func (s *Service) UpdateChannel(ctx context.Context, id string, enabled *bool, events *[]string, cfg ChannelConfigUpdate) (*ChannelView, error) {
	existing, err := s.repo.GetChannel(ctx, id)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	if events != nil {
		if err := validateEvents(*events); err != nil {
			return nil, err
		}
	}
	var ct []byte
	if cfg.Set {
		newCfg, err := s.buildUpdateConfig(existing.Type, cfg)
		if err != nil {
			return nil, err
		}
		if ct, err = s.wrapConfig(id, newCfg); err != nil {
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

// buildUpdateConfig validates a config replacement against the channel's type
// and returns the channelConfig to wrap.
func (s *Service) buildUpdateConfig(typ string, cfg ChannelConfigUpdate) (channelConfig, error) {
	if typ == "smtp" {
		in := ChannelInput{
			Events:                 []string{KnownEventKinds[0]}, // events validated separately; satisfy validateSMTP
			SMTPHost:               cfg.SMTPHost,
			SMTPPort:               cfg.SMTPPort,
			SMTPFrom:               cfg.SMTPFrom,
			SMTPTo:                 cfg.SMTPTo,
			SMTPUsername:           cfg.SMTPUsername,
			SMTPPassword:           cfg.SMTPPassword,
			SMTPTLSMode:            cfg.SMTPTLSMode,
			SMTPInsecureSkipVerify: cfg.SMTPInsecureSkipVerify,
		}
		if err := validateSMTP(in, cfg.HMACKey != ""); err != nil {
			return channelConfig{}, err
		}
		mode, _ := normalizeTLSMode(cfg.SMTPTLSMode)
		return channelConfig{
			Host: cfg.SMTPHost, Port: cfg.SMTPPort, From: cfg.SMTPFrom, To: cfg.SMTPTo,
			Username: cfg.SMTPUsername, Password: cfg.SMTPPassword,
			TLSMode: mode, InsecureSkipVerify: cfg.SMTPInsecureSkipVerify,
		}, nil
	}
	if err := validateURL(cfg.URL); err != nil {
		return channelConfig{}, err
	}
	if typ == "slack" && cfg.HMACKey != "" {
		return channelConfig{}, fmt.Errorf("%w: hmac_key applies to webhook channels only", ErrValidation)
	}
	return channelConfig{URL: cfg.URL, HMACKey: cfg.HMACKey}, nil
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
	// #nosec G117 -- the SMTP password (and webhook HMAC key) are marshaled here
	// deliberately so they can be envelope-encrypted under the master key by
	// WrapNotificationConfig below; the plaintext JSON never leaves this function
	// and is never persisted, logged, or returned. This is the same write-only
	// credential pattern as the OIDC client secret.
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
