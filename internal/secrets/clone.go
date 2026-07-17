package secrets

import (
	"context"

	"github.com/steveokay/janus-secrets/internal/store"
)

// CloneEnvironment creates a new environment in the same project and deep-copies
// the source environment's config tree and each config's own latest secrets.
//
// Inheritance is preserved: configs are created in topological order and each
// branch's inherits_from is remapped from the source config id to the freshly
// created one. Secret values are re-encrypted under the new config's AAD via the
// normal SetSecrets write path (the value AAD binds config_id, so blobs cannot be
// copied verbatim). No secret value is logged or audited here — the caller emits
// a single value-free env.clone event.
//
// This is NOT fully atomic — the store layer offers no cross-call transaction, so
// the config/secret copies land as separate writes. As a best-effort compensating
// action, if any step after the environment is created fails, the newly created
// environment is soft-deleted before the error propagates, so a failed clone never
// leaves a LIVE partially-populated environment behind. The cleanup is itself
// best-effort: a soft-delete failure is swallowed and the ORIGINAL clone error is
// what returns.
func (s *Service) CloneEnvironment(ctx context.Context, projectID, srcEnvID, newSlug, newName, actor string) (env *store.Environment, err error) {
	newEnv, err := s.CreateEnvironment(ctx, projectID, newSlug, newName)
	if err != nil {
		return nil, err
	}
	// From here on, any error triggers a best-effort soft-delete of the partially
	// built environment. Named returns let this run at every downstream error site
	// without duplicating the cleanup call. On the success path err is nil and the
	// closure is a no-op.
	defer func() {
		if err != nil {
			_ = s.envs.SoftDelete(ctx, newEnv.ID) // best-effort; original err wins
		}
	}()

	srcConfigs, err := s.configs.ListByEnvironment(ctx, srcEnvID)
	if err != nil {
		return nil, mapStoreErr(err)
	}

	idMap := make(map[string]string, len(srcConfigs)) // source config id -> new config id
	remaining := append([]*store.Config(nil), srcConfigs...)
	for len(remaining) > 0 {
		progressed := false
		next := remaining[:0]
		for _, c := range remaining {
			var newInherits *string
			if c.InheritsFrom != nil {
				mapped, ok := idMap[*c.InheritsFrom]
				if !ok {
					next = append(next, c) // parent not created yet — defer
					continue
				}
				newInherits = &mapped
			}
			nc, err := s.CreateConfig(ctx, newEnv.ID, c.Name, newInherits)
			if err != nil {
				return nil, err
			}
			idMap[c.ID] = nc.ID
			progressed = true
			if err := s.copyOwnSecrets(ctx, c.ID, nc.ID, actor); err != nil {
				return nil, err
			}
		}
		remaining = next
		if !progressed {
			// Cycle or inherits_from pointing outside this env: create stragglers
			// with no inheritance link rather than looping forever.
			for _, c := range remaining {
				nc, err := s.CreateConfig(ctx, newEnv.ID, c.Name, nil)
				if err != nil {
					return nil, err
				}
				idMap[c.ID] = nc.ID
				if err := s.copyOwnSecrets(ctx, c.ID, nc.ID, actor); err != nil {
					return nil, err
				}
			}
			break
		}
	}
	return newEnv, nil
}

// copyOwnSecrets reads a source config's own latest secrets and writes them into
// the destination config as one new version. Plaintext lives only transiently in
// memory; nothing is logged or audited here.
func (s *Service) copyOwnSecrets(ctx context.Context, srcConfigID, dstConfigID, actor string) error {
	_, state, err := s.RevealConfig(ctx, srcConfigID)
	if err != nil {
		return err
	}
	if len(state) == 0 {
		return nil
	}
	changes := make([]SecretChange, 0, len(state))
	for _, sec := range state {
		changes = append(changes, SecretChange{Key: sec.Key, Value: sec.Value, Type: sec.Type})
	}
	_, err = s.SetSecrets(ctx, dstConfigID, changes, "Cloned environment", actor)
	return err
}
