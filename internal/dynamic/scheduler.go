package dynamic

import (
	"context"
	"time"
)

// RunDue revokes every lease the lease-manager must act on: active leases past
// expiry, prior revoke failures, and crash-orphaned 'creating' rows older than
// creatingGrace. No-op while sealed. Per-lease errors are logged and never abort
// the pass.
func (s *Service) RunDue(ctx context.Context) {
	if s.kr.Sealed() {
		return
	}
	now := s.now()
	leases, err := s.leases.ClaimDue(ctx, now, now.Add(-creatingGrace), defaultBatch)
	if err != nil {
		// A cancelled context is orderly shutdown (or a detached post-unseal
		// sweep racing process exit), not a fault — don't cry wolf in the log.
		if ctx.Err() == nil {
			s.logger.Warn("dynamic claim-due failed", "err", err)
		}
		return
	}
	for _, l := range leases {
		if ctx.Err() != nil {
			return
		}
		// All swept leases terminate as 'expired' (system-completed), including a
		// 'revoke_failed' lease that a manual RevokeLease started: the security
		// outcome is identical and provenance (manual vs automatic) is preserved
		// in the audit log, so the status column need not distinguish them.
		if err := s.revoke(ctx, l, "expired"); err != nil {
			s.logger.Warn("dynamic lease revoke failed", "lease", l.ID, "err", sanitize(err))
			continue
		}
		s.recordLease(ctx, l, "dynamic.lease.expire")
	}
}

// SweepOrphanedLeases runs one immediate RunDue pass. It is invoked right after
// the keyring transitions sealed->unsealed (see unsealNow in the api package),
// so leases orphaned by a crash — including in-flight 'creating' rows and leases
// that expired while the server was down — are revoked promptly rather than
// waiting a full tick.
func (s *Service) SweepOrphanedLeases(ctx context.Context) {
	s.RunDue(ctx)
}

// RunScheduler ticks every `tick` and revokes due leases until ctx is done.
// tick <= 0 disables it (tests). Ties to the server shutdown context.
func (s *Service) RunScheduler(ctx context.Context, tick time.Duration) {
	if tick <= 0 {
		return
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	s.logger.Info("dynamic lease scheduler started", "tick", tick.String())
	for {
		select {
		case <-ctx.Done():
			s.logger.Info("dynamic lease scheduler stopping")
			return
		case <-t.C:
			s.RunDue(ctx)
		}
	}
}
