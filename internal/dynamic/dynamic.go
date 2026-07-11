// Package dynamic is Janus's dynamic Postgres credentials engine: on-demand,
// short-lived database roles issued from admin-authored SQL templates, with a
// lease manager that revokes them on expiry (and reclaims crash-orphaned leases
// after unseal).
package dynamic

import (
	"encoding/json"
	"errors"
	"log/slog"
	"runtime"
	"time"

	"github.com/steveokay/janus-secrets/internal/audit"
	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/store"
)

const (
	defaultBatch     = 50              // leases claimed per tick
	defaultPasswdLen = 32              // generated password length
	creatingGrace    = 5 * time.Minute // min age before a 'creating' lease is swept
)

// RoleConfig is the decrypted role-config blob (never logged/persisted in clear).
type RoleConfig struct {
	AdminDSN             string `json:"admin_dsn"`
	CreationStatements   string `json:"creation_statements"`
	RevocationStatements string `json:"revocation_statements,omitempty"`
	RenewStatements      string `json:"renew_statements,omitempty"`
}

type keyring interface {
	UnwrapProjectKEK(ct crypto.Ciphertext, projectID string) ([]byte, error)
	NewDEK(projectKEK, aad []byte) ([]byte, crypto.Ciphertext, error)
	Sealed() bool
}

type Service struct {
	kr       keyring
	roles    *store.DynamicRoleRepo
	leases   *store.DynamicLeaseRepo
	projects *store.ProjectRepo
	audit    *audit.Recorder
	logger   *slog.Logger
	st       *store.Store
	now      func() time.Time
}

func New(kr *crypto.Keyring, st *store.Store, aud *audit.Recorder, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		kr: kr, roles: store.NewDynamicRoleRepo(st), leases: store.NewDynamicLeaseRepo(st),
		projects: store.NewProjectRepo(st), audit: aud, logger: logger, st: st, now: time.Now,
	}
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
	runtime.KeepAlive(b)
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

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

// sanitize maps an error to a fixed, value-free category safe for last_error/audit.
func sanitize(err error) string {
	switch {
	case errors.Is(err, ErrSealed):
		return "sealed"
	case errors.Is(err, ErrApplyFailed):
		return "apply failed"
	case errors.Is(err, ErrInvalidConfig):
		return "invalid config"
	default:
		return "dynamic error"
	}
}

// --- envelope (ported from the rotation engine; AAD is DynamicConfigAAD) ---

func (s *Service) sealConfig(proj *store.Project, roleID string, cfg RoleConfig) (ct, nonce, wrapped []byte, kekVer int, err error) {
	return s.sealBlob(proj, crypto.DynamicConfigAAD(roleID), mustJSON(cfg))
}

func (s *Service) openConfig(proj *store.Project, r *store.DynamicRole) (RoleConfig, error) {
	pt, err := s.openBlob(proj, crypto.DynamicConfigAAD(r.ID), r.ConfigWrappedDEK, r.ConfigNonce, r.ConfigCT)
	if err != nil {
		return RoleConfig{}, err
	}
	defer zero(pt)
	var cfg RoleConfig
	if err := json.Unmarshal(pt, &cfg); err != nil {
		return RoleConfig{}, ErrInvalidConfig
	}
	return cfg, nil
}

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
	zero(plaintext)
	return c.Data, c.Nonce, wrappedDEK.Marshal(), proj.KEKVersion, nil
}

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

// --- views ---

type RoleView struct {
	ID                string
	ProjectID         string
	ConfigID          string
	Name              string
	DefaultTTLSeconds int64
	MaxTTLSeconds     int64
	CreatedAt         time.Time
}

func roleView(r *store.DynamicRole) RoleView {
	return RoleView{
		ID: r.ID, ProjectID: r.ProjectID, ConfigID: r.ConfigID, Name: r.Name,
		DefaultTTLSeconds: r.DefaultTTLSeconds, MaxTTLSeconds: r.MaxTTLSeconds, CreatedAt: r.CreatedAt,
	}
}

type LeaseView struct {
	ID           string
	RoleID       string
	ProjectID    string
	DBUsername   string
	Status       string
	IssuedAt     time.Time
	ExpiresAt    time.Time
	MaxExpiresAt time.Time
	RenewedAt    *time.Time
	CreatedAt    time.Time
}

func leaseView(l *store.DynamicLease) LeaseView {
	return LeaseView{
		ID: l.ID, RoleID: l.RoleID, ProjectID: l.ProjectID, DBUsername: l.DBUsername,
		Status: l.Status, IssuedAt: l.IssuedAt, ExpiresAt: l.ExpiresAt, MaxExpiresAt: l.MaxExpiresAt,
		RenewedAt: l.RenewedAt, CreatedAt: l.CreatedAt,
	}
}

type Creds struct {
	LeaseID   string
	Username  string
	Password  string
	ExpiresAt time.Time
}
