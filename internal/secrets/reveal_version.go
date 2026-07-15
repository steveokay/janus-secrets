package secrets

import "context"

// RevealConfigVersion decrypts and returns every secret in a specific config
// version (not just the latest). Same per-value decrypt path as RevealConfig.
func (s *Service) RevealConfigVersion(ctx context.Context, configID string, version int) (map[string]Secret, error) {
	cfg, proj, err := s.resolveProject(ctx, configID)
	if err != nil {
		return nil, err
	}
	_, state, err := s.secrets.GetVersion(ctx, cfg.ID, version)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	res := s.newKEKResolver(proj)
	defer res.zero()
	out := make(map[string]Secret, len(state))
	for key, sv := range state {
		pt, err := s.decryptValue(ctx, proj, cfg.ID, sv, res)
		if err != nil {
			for _, sec := range out {
				zeroize(sec.Value)
			}
			return nil, err
		}
		out[key] = Secret{Key: key, Value: pt, ValueVersion: sv.ValueVersion}
	}
	return out, nil
}
