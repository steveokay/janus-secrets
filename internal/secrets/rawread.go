package secrets

import (
	"context"
	"errors"

	"github.com/steveokay/janus-secrets/internal/resolve"
	"github.com/steveokay/janus-secrets/internal/store"
)

// ReadRaw implements resolve.RawReader: it resolves a coordinate (project slug →
// env slug → config name) to a config and returns its raw decrypted values.
func (s *Service) ReadRaw(ctx context.Context, coord resolve.Coord) (resolve.RawConfig, error) {
	proj, err := s.projects.GetBySlug(ctx, coord.Project)
	if err != nil {
		return resolve.RawConfig{}, mapStoreErr(err)
	}
	env, err := s.envs.GetBySlug(ctx, proj.ID, coord.Env)
	if err != nil {
		return resolve.RawConfig{}, mapStoreErr(err)
	}
	cfg, err := s.configs.GetByName(ctx, env.ID, coord.Config)
	if err != nil {
		return resolve.RawConfig{}, mapStoreErr(err)
	}
	return s.rawFor(ctx, proj, env, cfg)
}

// ReadRawByID implements resolve.RawReader by config id (used to walk an
// inheritance chain, whose links are stored as config ids).
func (s *Service) ReadRawByID(ctx context.Context, configID string) (resolve.RawConfig, error) {
	cfg, err := s.configs.Get(ctx, configID)
	if err != nil {
		return resolve.RawConfig{}, mapStoreErr(err)
	}
	env, err := s.envs.Get(ctx, cfg.EnvironmentID)
	if err != nil {
		return resolve.RawConfig{}, mapStoreErr(err)
	}
	proj, err := s.projects.Get(ctx, env.ProjectID)
	if err != nil {
		return resolve.RawConfig{}, mapStoreErr(err)
	}
	return s.rawFor(ctx, proj, env, cfg)
}

// rawFor decrypts every live secret in cfg's latest version into a raw map. A
// config with no version of its own contributes an empty own-value set (its
// effective values come entirely from its inheritance base), so it is still
// readable — a branch config that exists only to inherit + override is valid.
func (s *Service) rawFor(ctx context.Context, proj *store.Project, env *store.Environment, cfg *store.Config) (resolve.RawConfig, error) {
	_, state, err := s.secrets.GetLatest(ctx, cfg.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return resolve.RawConfig{}, mapStoreErr(err)
	}
	values := make(map[string][]byte, len(state))
	if len(state) > 0 {
		kek, err := s.unwrapProjectKEK(proj)
		if err != nil {
			return resolve.RawConfig{}, err
		}
		defer zeroize(kek)
		for key, sv := range state {
			pt, err := s.decryptValue(proj, cfg.ID, sv, kek)
			if err != nil {
				for _, v := range values {
					zeroize(v)
				}
				return resolve.RawConfig{}, err
			}
			values[key] = pt
		}
	}
	return resolve.RawConfig{
		ProjectID: proj.ID, EnvID: env.ID, ConfigID: cfg.ID,
		Project: proj.Slug, Env: env.Slug, Config: cfg.Name,
		InheritsFrom: cfg.InheritsFrom, Values: values,
	}, nil
}
