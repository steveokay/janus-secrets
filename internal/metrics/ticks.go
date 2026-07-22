package metrics

import (
	"sync"
	"time"
)

// TickTracker records the last time each scheduler engine ran a tick. It is the
// single source feeding both janus_scheduler_last_tick_seconds and the
// /v1/sys/status health snapshot. Safe for concurrent use.
type TickTracker struct {
	mu    sync.Mutex
	ticks map[string]time.Time
	now   func() time.Time
}

// NewTickTracker returns an empty tracker using the wall clock.
func NewTickTracker() *TickTracker {
	return &TickTracker{ticks: map[string]time.Time{}, now: time.Now}
}

// MarkTick stamps engine's last-tick time to now. Called at the top of each
// scheduler's tick loop.
func (t *TickTracker) MarkTick(engine string) {
	t.mu.Lock()
	t.ticks[engine] = t.now()
	t.mu.Unlock()
}

// LastTick returns the last recorded tick time for engine and whether one has
// ever been recorded.
func (t *TickTracker) LastTick(engine string) (time.Time, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	ts, ok := t.ticks[engine]
	return ts, ok
}

// Engines returns the set of engines that have recorded at least one tick.
func (t *TickTracker) Engines() map[string]time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[string]time.Time, len(t.ticks))
	for k, v := range t.ticks {
		out[k] = v
	}
	return out
}
