// Package rotation is Janus's static-rotation engine: scheduled rotation of an
// existing secret's value via a Postgres single-role reset or a generic
// HMAC-signed webhook, with crash-safe apply and optional notify webhooks.
package rotation

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/steveokay/janus-secrets/internal/audit"
	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/secrets"
	"github.com/steveokay/janus-secrets/internal/store"
)

const (
	TypePostgres = "postgres"
	TypeWebhook  = "webhook"

	failureThreshold = 5  // consecutive failures → status='failed'
	defaultBatch     = 50 // policies claimed per tick
	defaultPasswdLen = 32 // generated value length
)

// PolicyConfig is the decrypted rotator-config blob (never logged/persisted in clear).
type PolicyConfig struct {
	// postgres
	AdminDSN    string `json:"admin_dsn,omitempty"`
	Role        string `json:"role,omitempty"`
	PasswordLen int    `json:"password_len,omitempty"`
	// webhook
	URL     string `json:"url,omitempty"`
	HMACKey string `json:"hmac_key,omitempty"`
	// optional notify (either type)
	NotifyURL     string `json:"notify_url,omitempty"`
	NotifyHMACKey string `json:"notify_hmac_key,omitempty"`
}

// keyring is the subset of *crypto.Keyring the engine needs (fakeable in tests).
type keyring interface {
	UnwrapProjectKEK(ct crypto.Ciphertext, projectID string) ([]byte, error)
	NewDEK(projectKEK, aad []byte) ([]byte, crypto.Ciphertext, error)
	Sealed() bool
}

// Service is the rotation engine.
type Service struct {
	kr       keyring
	repo     *store.RotationRepo
	projects *store.ProjectRepo
	secrets  *secrets.Service
	audit    *audit.Recorder
	logger   *slog.Logger
	st       *store.Store
	hc       *http.Client
	now      func() time.Time // injectable clock (tests)
}

// New wires the engine.
func New(kr *crypto.Keyring, st *store.Store, sec *secrets.Service, aud *audit.Recorder, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		kr: kr, repo: store.NewRotationRepo(st), projects: store.NewProjectRepo(st),
		secrets: sec, audit: aud, logger: logger, st: st,
		hc:  &http.Client{Timeout: 15 * time.Second},
		now: time.Now,
	}
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// sealConfig encrypts a PolicyConfig blob under proj's KEK, bound to policyID.
func (s *Service) sealConfig(proj *store.Project, policyID string, cfg PolicyConfig) (ct, nonce, wrapped []byte, kekVer int, err error) {
	return s.sealBlob(proj, crypto.RotationConfigAAD(policyID), mustJSON(cfg))
}

// openConfig decrypts the stored PolicyConfig blob.
func (s *Service) openConfig(proj *store.Project, p *store.RotationPolicy) (PolicyConfig, error) {
	pt, err := s.openBlob(proj, crypto.RotationConfigAAD(p.ID), p.ConfigWrappedDEK, p.ConfigNonce, p.ConfigCT)
	if err != nil {
		return PolicyConfig{}, err
	}
	defer zero(pt)
	var cfg PolicyConfig
	if err := json.Unmarshal(pt, &cfg); err != nil {
		return PolicyConfig{}, ErrInvalidConfig
	}
	return cfg, nil
}

// sealPending / openPending store the in-flight generated value (distinct AAD).
func (s *Service) sealPending(proj *store.Project, policyID, value string) (ct, nonce, wrapped []byte, err error) {
	c, n, w, _, e := s.sealBlob(proj, crypto.RotationPendingAAD(policyID), []byte(value))
	return c, n, w, e
}

func (s *Service) openPending(proj *store.Project, p *store.RotationPolicy) (string, error) {
	pt, err := s.openBlob(proj, crypto.RotationPendingAAD(p.ID), p.PendingWrappedDEK, p.PendingNonce, p.PendingCT)
	if err != nil {
		return "", err
	}
	defer zero(pt)
	return string(pt), nil
}

// sealBlob mints a DEK under proj's KEK and GCM-encrypts plaintext with aad.
func (s *Service) sealBlob(proj *store.Project, aad, plaintext []byte) (ct, nonce, wrapped []byte, kekVer int, err error) {
	kek, err := s.unwrapProjectKEK(proj)
	if err != nil {
		return nil, nil, nil, 0, err
	}
	defer zero(kek)
	dek, wrappedDEK, err := s.kr.NewDEK(kek, aad)
	if err != nil {
		return nil, nil, nil, 0, err
	}
	defer zero(dek)
	c, err := crypto.Encrypt(dek, plaintext, aad)
	if err != nil {
		return nil, nil, nil, 0, err
	}
	return c.Data, c.Nonce, wrappedDEK.Marshal(), proj.KEKVersion, nil
}

// openBlob unwraps the DEK and GCM-decrypts the stored ciphertext.
func (s *Service) openBlob(proj *store.Project, aad, wrappedDEK, nonce, ct []byte) ([]byte, error) {
	kek, err := s.unwrapProjectKEK(proj)
	if err != nil {
		return nil, err
	}
	defer zero(kek)
	dekCT, err := crypto.ParseCiphertext(wrappedDEK)
	if err != nil {
		return nil, ErrInvalidConfig
	}
	dek, err := crypto.UnwrapKey(kek, dekCT, aad)
	if err != nil {
		return nil, err
	}
	defer zero(dek)
	return crypto.Decrypt(dek, crypto.Ciphertext{Nonce: nonce, Data: ct}, aad)
}

func (s *Service) unwrapProjectKEK(proj *store.Project) ([]byte, error) {
	if s.kr.Sealed() {
		return nil, ErrSealed
	}
	ct, err := crypto.ParseCiphertext(proj.WrappedKEK)
	if err != nil {
		return nil, ErrInvalidConfig
	}
	return s.kr.UnwrapProjectKEK(ct, proj.ID)
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

// mapStoreErr translates store sentinels to rotation sentinels.
func mapStoreErr(err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, store.ErrAlreadyExists):
		return ErrExists
	default:
		return err
	}
}
