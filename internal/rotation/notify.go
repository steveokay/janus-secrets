package rotation

import (
	"context"
	"encoding/json"
	"time"

	"github.com/steveokay/janus-secrets/internal/store"
)

// notify fires a best-effort, value-free post-rotation event. A failure is
// logged and swallowed — the rotation already committed.
func (s *Service) notify(ctx context.Context, cfg PolicyConfig, p *store.RotationPolicy, newVersion int) {
	if cfg.NotifyURL == "" {
		return
	}
	body, _ := json.Marshal(map[string]any{
		"policy_id":   p.ID,
		"project_id":  p.ProjectID,
		"config_id":   p.ConfigID,
		"secret_key":  p.SecretKey,
		"new_version": newVersion,
		"rotated_at":  s.now().UTC().Format(time.RFC3339),
	})
	if err := signedPost(ctx, s.hc, cfg.NotifyURL, cfg.NotifyHMACKey, body); err != nil {
		s.logger.Warn("rotation notify webhook failed", "policy", p.ID, "err", err)
	}
}
