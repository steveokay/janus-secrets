package secrets

import (
	"context"

	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/store"
)

// CreateProject generates a fresh project KEK, wraps it under the master key
// bound to the project's id, and persists the project with the wrapped KEK.
// The plaintext KEK never leaves this function.
func (s *Service) CreateProject(ctx context.Context, slug, name string) (*store.Project, error) {
	if err := validateSlug(slug); err != nil {
		return nil, err
	}
	id, err := s.st.NewID(ctx)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	kek, err := crypto.GenerateKey()
	if err != nil {
		return nil, err
	}
	defer zeroize(kek)

	wrapped, err := s.keyring.WrapProjectKEK(kek, id)
	if err != nil {
		return nil, mapCryptoErr(err)
	}
	p, err := s.projects.Create(ctx, id, slug, name, wrapped.Marshal(), 1)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return p, nil
}
