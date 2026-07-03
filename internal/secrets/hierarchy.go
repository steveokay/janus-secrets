package secrets

import (
	"context"

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

// CreateConfig creates a config under an environment. inheritsFrom is carried
// unresolved (inheritance resolution is a later milestone). No crypto.
func (s *Service) CreateConfig(ctx context.Context, environmentID, name string, inheritsFrom *string) (*store.Config, error) {
	c, err := s.configs.Create(ctx, environmentID, name, inheritsFrom)
	return c, mapStoreErr(err)
}
