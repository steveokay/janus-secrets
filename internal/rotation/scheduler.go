package rotation

import (
	"context"
	"time"
)

// RunDue rotates every currently-due policy once. It is a no-op while sealed.
// Per-policy errors are handled inside attempt (logged + backoff) and never
// abort the pass.
func (s *Service) RunDue(ctx context.Context) {
	if s.tickHook != nil {
		s.tickHook()
	}
	if s.kr.Sealed() {
		return
	}
	policies, err := s.repo.ClaimDue(ctx, s.now(), defaultBatch)
	if err != nil {
		s.logger.Warn("rotation claim-due failed", "err", err)
		return
	}
	for _, p := range policies {
		if ctx.Err() != nil {
			return
		}
		_ = s.attempt(ctx, p)
	}
}

// RunScheduler ticks every `tick` and rotates due policies until ctx is done.
// tick <= 0 disables the scheduler (returns immediately). Ties to the server
// shutdown context so it stops cleanly on SIGTERM.
func (s *Service) RunScheduler(ctx context.Context, tick time.Duration) {
	if tick <= 0 {
		return
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	s.logger.Info("rotation scheduler started", "tick", tick.String())
	for {
		select {
		case <-ctx.Done():
			s.logger.Info("rotation scheduler stopping")
			return
		case <-t.C:
			s.RunDue(ctx)
		}
	}
}
