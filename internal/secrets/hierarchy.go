package secrets

import (
	"context"
	"errors"

	"github.com/steveokay/janus-secrets/internal/store"
)

// CreateEnvironment creates an environment under a project. No crypto.
func (s *Service) CreateEnvironment(ctx context.Context, projectID, slug, name string) (*store.Environment, error) {
	if err := validateSlug(slug); err != nil {
		return nil, err
	}
	e, err := s.envs.Create(ctx, projectID, slug, name)
	return e, mapStoreErr(err)
}

// CreateConfig creates a config under an environment. If inheritsFrom is set,
// the base config must be a live config in the *same* environment: inheritance
// is transparent to authorization (reading a branch needs no separate grant on
// its base), so a cross-environment or cross-project base would let a caller
// read another scope's secrets through the branch. This precondition is enforced
// here — the DB foreign key only requires the base to exist somewhere in the
// instance. No crypto.
func (s *Service) CreateConfig(ctx context.Context, environmentID, name string, inheritsFrom *string) (*store.Config, error) {
	if inheritsFrom != nil {
		base, err := s.configs.Get(ctx, *inheritsFrom)
		if err != nil {
			// Absent or soft-deleted base: the client supplied a bad reference —
			// reject as invalid input, not a not-found on the config being
			// created. (A malformed id fails the uuid cast like any other id
			// param and surfaces generically.)
			if errors.Is(err, store.ErrNotFound) {
				return nil, ErrValidation
			}
			return nil, mapStoreErr(err)
		}
		if base.EnvironmentID != environmentID {
			return nil, ErrValidation
		}
	}
	c, err := s.configs.Create(ctx, environmentID, name, inheritsFrom)
	return c, mapStoreErr(err)
}
