package secretsync

import (
	"context"
	"time"
)

// RunDue syncs every currently-due target once. No-op while sealed. Per-target
// errors are handled inside attempt (logged + backoff) and never abort the pass.
func (s *Service) RunDue(ctx context.Context) {
	if s.tickHook != nil {
		s.tickHook()
	}
	if s.kr.Sealed() {
		return
	}
	targets, err := s.repo.ClaimDue(ctx, s.now(), defaultBatch)
	if err != nil {
		s.logger.Warn("sync claim-due failed", "err", err)
		return
	}
	for _, t := range targets {
		if ctx.Err() != nil {
			return
		}
		_ = s.attempt(ctx, t, false)
	}
}

// RunScheduler ticks every `tick` and syncs due targets until ctx is done.
// tick <= 0 disables the scheduler. Tie to the server shutdown context.
func (s *Service) RunScheduler(ctx context.Context, tick time.Duration) {
	if tick <= 0 {
		return
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	s.logger.Info("sync scheduler started", "tick", tick.String())
	for {
		select {
		case <-ctx.Done():
			s.logger.Info("sync scheduler stopping")
			return
		case <-t.C:
			s.RunDue(ctx)
		}
	}
}
