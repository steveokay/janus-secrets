package dynamic

import (
	"context"
	"strings"

	"github.com/steveokay/janus-secrets/internal/store"
)

// RoleInput is the create payload for a dynamic role (plaintext config,
// encrypted here before storage).
type RoleInput struct {
	ConfigID          string
	Name              string
	DefaultTTLSeconds int64
	MaxTTLSeconds     int64
	Config            RoleConfig
}

// validateConfig is a config-sanity/usability guard (non-empty admin DSN,
// required placeholders present), NOT a security control: the injection
// boundary lives in interpolate, which only ever substitutes Janus-generated,
// quote-free values into the admin-authored SQL.
func validateConfig(cfg RoleConfig) error {
	if cfg.AdminDSN == "" {
		return ErrInvalidConfig
	}
	if !strings.Contains(cfg.CreationStatements, "{{name}}") ||
		!strings.Contains(cfg.CreationStatements, "{{password}}") {
		return ErrInvalidConfig
	}
	// Revocation may be empty (engine falls back to DROP ROLE IF EXISTS); if set
	// it must reference the role name.
	if strings.TrimSpace(cfg.RevocationStatements) != "" &&
		!strings.Contains(cfg.RevocationStatements, "{{name}}") {
		return ErrInvalidConfig
	}
	if strings.TrimSpace(cfg.RenewStatements) != "" &&
		!strings.Contains(cfg.RenewStatements, "{{name}}") {
		return ErrInvalidConfig
	}
	return nil
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

func (s *Service) CreateRole(ctx context.Context, in RoleInput, createdBy string) (RoleView, error) {
	if in.Name == "" || in.DefaultTTLSeconds <= 0 || in.MaxTTLSeconds < in.DefaultTTLSeconds {
		return RoleView{}, ErrInvalidConfig
	}
	if err := validateConfig(in.Config); err != nil {
		return RoleView{}, err
	}
	proj, err := s.projectForConfig(ctx, in.ConfigID)
	if err != nil {
		return RoleView{}, err
	}
	id, err := s.st.NewID(ctx)
	if err != nil {
		return RoleView{}, err
	}
	ct, nonce, wrapped, kekVer, err := s.sealConfig(proj, id, in.Config)
	if err != nil {
		return RoleView{}, err
	}
	r := &store.DynamicRole{
		ID: id, ProjectID: proj.ID, ConfigID: in.ConfigID, Name: in.Name,
		DefaultTTLSeconds: in.DefaultTTLSeconds, MaxTTLSeconds: in.MaxTTLSeconds,
		ConfigCT: ct, ConfigNonce: nonce, ConfigWrappedDEK: wrapped, ConfigDEKKEKVersion: kekVer,
		CreatedBy: createdBy,
	}
	saved, err := s.roles.Create(ctx, r)
	if err != nil {
		return RoleView{}, mapStoreErr(err)
	}
	return roleView(saved), nil
}

func (s *Service) GetRole(ctx context.Context, id string) (RoleView, error) {
	r, err := s.roles.Get(ctx, id)
	if err != nil {
		return RoleView{}, mapStoreErr(err)
	}
	return roleView(r), nil
}

func (s *Service) ListRolesByConfig(ctx context.Context, configID string) ([]RoleView, error) {
	rs, err := s.roles.ListByConfig(ctx, configID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	out := make([]RoleView, 0, len(rs))
	for _, r := range rs {
		out = append(out, roleView(r))
	}
	return out, nil
}

// UpdateRole changes TTLs and/or the config blob. nil leaves a field unchanged.
func (s *Service) UpdateRole(ctx context.Context, id string, defaultTTL, maxTTL *int64, cfg *RoleConfig) (RoleView, error) {
	r, err := s.roles.Get(ctx, id)
	if err != nil {
		return RoleView{}, mapStoreErr(err)
	}
	dt, mt := r.DefaultTTLSeconds, r.MaxTTLSeconds
	if defaultTTL != nil {
		dt = *defaultTTL
	}
	if maxTTL != nil {
		mt = *maxTTL
	}
	if dt <= 0 || mt < dt {
		return RoleView{}, ErrInvalidConfig
	}
	var ct, nonce, wrapped []byte
	var kekVer *int
	if cfg != nil {
		if err := validateConfig(*cfg); err != nil {
			return RoleView{}, err
		}
		proj, err := s.projects.Get(ctx, r.ProjectID)
		if err != nil {
			return RoleView{}, mapStoreErr(err)
		}
		c, n, w, v, err := s.sealConfig(proj, id, *cfg)
		if err != nil {
			return RoleView{}, err
		}
		ct, nonce, wrapped, kekVer = c, n, w, &v
	}
	if err := s.roles.Update(ctx, id, defaultTTL, maxTTL, ct, nonce, wrapped, kekVer); err != nil {
		return RoleView{}, mapStoreErr(err)
	}
	return s.GetRole(ctx, id)
}

// DeleteRole revokes every still-active lease for the role, then deletes it. If
// any revocation fails the role is left in place so leases (and their live DB
// roles) are never orphaned.
func (s *Service) DeleteRole(ctx context.Context, id string) error {
	if s.kr.Sealed() {
		return ErrSealed
	}
	leases, err := s.leases.ListByRole(ctx, id)
	if err != nil {
		return mapStoreErr(err)
	}
	for _, l := range leases {
		if l.Status == "active" || l.Status == "creating" || l.Status == "revoke_failed" {
			if err := s.revoke(ctx, l, "revoked"); err != nil {
				return err
			}
		}
	}
	return mapStoreErr(s.roles.Delete(ctx, id))
}
