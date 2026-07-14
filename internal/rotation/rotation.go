// Package rotation is Janus's static-rotation engine: scheduled rotation of an
// existing secret's value via a Postgres single-role reset or a generic
// HMAC-signed webhook, with crash-safe apply and optional notify webhooks.
package rotation

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"runtime"
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
	runtime.KeepAlive(b)
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
	// Wipe the freshly-allocated plaintext blob (JSON config or generated
	// value) now that it's encrypted; the ciphertext carries it from here.
	zero(plaintext)
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

// PolicyInput is the create/update payload (plaintext config; encrypted here).
type PolicyInput struct {
	ConfigID        string
	SecretKey       string
	Type            string
	IntervalSeconds int64
	Config          PolicyConfig
}

// PolicyView is the masked, safe-to-return projection (no secrets/DSN/keys).
type PolicyView struct {
	ID                string
	ProjectID         string
	ConfigID          string
	SecretKey         string
	Type              string
	IntervalSeconds   int64
	Status            string
	FailureCount      int
	LastError         *string
	NextRotationAt    time.Time
	LastRotatedAt     *time.Time
	LastConfigVersion *int
	CreatedAt         time.Time
}

func view(p *store.RotationPolicy) PolicyView {
	return PolicyView{
		ID: p.ID, ProjectID: p.ProjectID, ConfigID: p.ConfigID, SecretKey: p.SecretKey,
		Type: p.Type, IntervalSeconds: p.IntervalSeconds, Status: p.Status,
		FailureCount: p.FailureCount, LastError: p.LastError, NextRotationAt: p.NextRotationAt,
		LastRotatedAt: p.LastRotatedAt, LastConfigVersion: p.LastConfigVersion, CreatedAt: p.CreatedAt,
	}
}

// projectForConfig resolves the owning project of a config (for KEK + scope).
func (s *Service) projectForConfig(ctx context.Context, configID string) (*store.Project, error) {
	cfg, err := store.NewConfigRepo(s.st).Get(ctx, configID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	env, err := store.NewEnvironmentRepo(s.st).Get(ctx, cfg.EnvironmentID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	proj, err := s.projects.Get(ctx, env.ProjectID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return proj, nil
}

// Create validates, encrypts the config blob, and inserts a policy.
func (s *Service) Create(ctx context.Context, in PolicyInput, createdBy string) (PolicyView, error) {
	if in.Type != TypePostgres && in.Type != TypeWebhook {
		return PolicyView{}, ErrInvalidType
	}
	if in.SecretKey == "" || in.IntervalSeconds <= 0 {
		return PolicyView{}, ErrInvalidConfig
	}
	if in.Type == TypePostgres && (in.Config.AdminDSN == "" || !roleRe.MatchString(in.Config.Role)) {
		return PolicyView{}, ErrInvalidConfig
	}
	if in.Type == TypeWebhook && in.Config.URL == "" {
		return PolicyView{}, ErrInvalidConfig
	}
	proj, err := s.projectForConfig(ctx, in.ConfigID)
	if err != nil {
		return PolicyView{}, err
	}
	id, err := s.st.NewID(ctx)
	if err != nil {
		return PolicyView{}, err
	}
	ct, nonce, wrapped, kekVer, err := s.sealConfig(proj, id, in.Config)
	if err != nil {
		return PolicyView{}, err
	}
	p := &store.RotationPolicy{
		ID: id, ProjectID: proj.ID, ConfigID: in.ConfigID, SecretKey: in.SecretKey,
		Type: in.Type, IntervalSeconds: in.IntervalSeconds,
		NextRotationAt:      s.now().Add(time.Duration(in.IntervalSeconds) * time.Second),
		ConfigCT:            ct,
		ConfigNonce:         nonce,
		ConfigWrappedDEK:    wrapped,
		ConfigDEKKEKVersion: kekVer,
		CreatedBy:           createdBy,
	}
	saved, err := s.repo.Create(ctx, p)
	if err != nil {
		return PolicyView{}, mapStoreErr(err)
	}
	return view(saved), nil
}

func (s *Service) Get(ctx context.Context, id string) (PolicyView, error) {
	p, err := s.repo.Get(ctx, id)
	if err != nil {
		return PolicyView{}, mapStoreErr(err)
	}
	return view(p), nil
}

// ListRuns returns recorded run history for a policy, newest-first,
// keyset-paginated (cursor=0 starts at the newest; pass the last id to page older).
func (s *Service) ListRuns(ctx context.Context, policyID string, cursor int64, limit int) ([]store.RotationRun, error) {
	return s.repo.ListRuns(ctx, policyID, cursor, limit)
}

func (s *Service) ListByProject(ctx context.Context, projectID string) ([]PolicyView, error) {
	ps, err := s.repo.ListByProject(ctx, projectID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	out := make([]PolicyView, 0, len(ps))
	for _, p := range ps {
		out = append(out, view(p))
	}
	return out, nil
}

// Update changes interval, status, and/or the config blob. nil Config leaves
// the stored blob unchanged. Callers may set status only to active/paused.
func (s *Service) Update(ctx context.Context, id string, intervalSeconds *int64, status *string, cfg *PolicyConfig) (PolicyView, error) {
	if status != nil && *status != "active" && *status != "paused" {
		return PolicyView{}, ErrInvalidConfig // callers cannot set 'failed' directly
	}
	p, err := s.repo.Get(ctx, id)
	if err != nil {
		return PolicyView{}, mapStoreErr(err)
	}
	var ct, nonce, wrapped []byte
	var kekVer *int
	if cfg != nil {
		proj, err := s.projects.Get(ctx, p.ProjectID)
		if err != nil {
			return PolicyView{}, mapStoreErr(err)
		}
		c, n, w, v, err := s.sealConfig(proj, id, *cfg)
		if err != nil {
			return PolicyView{}, err
		}
		ct, nonce, wrapped, kekVer = c, n, w, &v
	}
	if err := s.repo.Update(ctx, id, intervalSeconds, status, ct, nonce, wrapped, kekVer); err != nil {
		return PolicyView{}, mapStoreErr(err)
	}
	return s.Get(ctx, id)
}

func (s *Service) Delete(ctx context.Context, id string) error {
	return mapStoreErr(s.repo.Delete(ctx, id))
}

// RotateNow runs an immediate rotation. It first marks the policy due (so a
// crash mid-apply is recovered by the scheduler) and clears a 'failed' status
// so a manual trigger always attempts. Returns the produced config version.
func (s *Service) RotateNow(ctx context.Context, id string) (int, error) {
	if _, err := s.repo.Get(ctx, id); err != nil {
		return 0, mapStoreErr(err)
	}
	if s.kr.Sealed() {
		return 0, ErrSealed
	}
	if err := s.repo.PrepareRotateNow(ctx, id, s.now()); err != nil {
		return 0, mapStoreErr(err)
	}
	p, err := s.repo.Get(ctx, id)
	if err != nil {
		return 0, mapStoreErr(err)
	}
	if err := s.attempt(ctx, p); err != nil {
		return 0, err
	}
	np, err := s.repo.Get(ctx, id)
	if err != nil {
		return 0, mapStoreErr(err)
	}
	if np.LastConfigVersion == nil {
		return 0, nil
	}
	return *np.LastConfigVersion, nil
}
