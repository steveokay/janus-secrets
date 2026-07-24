package secrets

import (
	"context"
	"time"

	"github.com/steveokay/janus-secrets/internal/store"
)

// SecretMeta is masked secret metadata — no value. Used by list/history views
// that must not decrypt or leak plaintext.
type SecretMeta struct {
	Key          string
	ValueVersion int
	CreatedAt    time.Time
}

// ListSecrets returns the latest config version and masked metadata for its
// live keys. It never touches the KEK or ciphertext.
func (s *Service) ListSecrets(ctx context.Context, configID string) (store.ConfigVersion, []SecretMeta, error) {
	cv, state, err := s.secrets.GetLatest(ctx, configID)
	if err != nil {
		return store.ConfigVersion{}, nil, mapStoreErr(err)
	}
	out := make([]SecretMeta, 0, len(state))
	for key, sv := range state {
		out = append(out, SecretMeta{Key: key, ValueVersion: sv.ValueVersion, CreatedAt: sv.CreatedAt})
	}
	return cv, out, nil
}

// KeyHistory returns masked metadata for every value a key has held, oldest
// first. Revealing a specific historical value is GetSecretVersion.
func (s *Service) KeyHistory(ctx context.Context, configID, key string) ([]SecretMeta, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}
	hist, err := s.secrets.GetKeyHistory(ctx, configID, key)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	out := make([]SecretMeta, 0, len(hist))
	for _, sv := range hist {
		out = append(out, SecretMeta{Key: sv.Key, ValueVersion: sv.ValueVersion, CreatedAt: sv.CreatedAt})
	}
	return out, nil
}

// ListVersions returns a config's version metadata, oldest first.
func (s *Service) ListVersions(ctx context.Context, configID string) ([]store.ConfigVersion, error) {
	v, err := s.secrets.ListVersions(ctx, configID)
	return v, mapStoreErr(err)
}

// DiffVersions compares two config versions.
func (s *Service) DiffVersions(ctx context.Context, configID string, vA, vB int) (store.Diff, error) {
	d, err := s.secrets.Diff(ctx, configID, vA, vB)
	return d, mapStoreErr(err)
}

// Rollback creates a new version whose state equals targetVersion's, reusing
// the target's ciphertext (no re-encryption).
func (s *Service) Rollback(ctx context.Context, configID string, targetVersion int, message, actor string) (store.ConfigVersion, error) {
	cv, err := s.secrets.Rollback(ctx, configID, targetVersion, message, actor)
	return cv, mapStoreErr(err)
}

// RollbackChanges computes the changeset that, applied to the config's current
// latest version, reproduces targetVersion's exact state: a SET for every key
// live at targetVersion (its value + type) and a DELETE for every key live now
// but absent at targetVersion. Because SetSecrets carries unlisted keys forward,
// the explicit deletes are required to drop keys added after targetVersion.
//
// It exists so a rollback of a PROTECTED config can be routed through the
// four-eyes edit-request flow (which stores the proposed changes
// envelope-encrypted) instead of committing directly. The returned changes
// carry plaintext values; callers must not log them and must pass them only to
// a path that zeroizes them (editreq.Create / SetSecrets).
func (s *Service) RollbackChanges(ctx context.Context, configID string, targetVersion int) ([]SecretChange, error) {
	targetVals, err := s.RevealConfigVersion(ctx, configID, targetVersion)
	if err != nil {
		return nil, err
	}
	// Best-effort wipe the revealed plaintext once we've copied what we need.
	defer func() {
		for _, sec := range targetVals {
			for i := range sec.Value {
				sec.Value[i] = 0
			}
		}
	}()
	// Live keys now (masked metadata only — no decryption of current values).
	_, metas, err := s.ListSecrets(ctx, configID)
	if err != nil {
		return nil, err
	}
	changes := make([]SecretChange, 0, len(targetVals)+len(metas))
	for k, sec := range targetVals {
		changes = append(changes, SecretChange{Key: k, Value: append([]byte(nil), sec.Value...), Type: sec.Type})
	}
	for _, m := range metas {
		if _, ok := targetVals[m.Key]; !ok {
			changes = append(changes, SecretChange{Key: m.Key, Delete: true})
		}
	}
	return changes, nil
}
