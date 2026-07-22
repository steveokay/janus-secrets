package auth

import "time"

// LockoutPolicy configures progressive per-account lockout. When Enabled is
// false the whole mechanism is bypassed and Login behaves exactly as before.
//
// Escalation: window(level) = min(Max, Base * 5^(level-1)) for level >= 1, so
// with the defaults (Base=1m, Max=1h) the windows are 1m, 5m, 25m, 1h, 1h, …
type LockoutPolicy struct {
	Enabled   bool
	Threshold int           // consecutive failures before the first lock
	Base      time.Duration // first lock window
	Max       time.Duration // cap on the window
}

// DefaultLockoutPolicy returns the built-in defaults (enabled, threshold 5,
// 1-minute base, 1-hour cap) used when env is absent or unparseable.
func DefaultLockoutPolicy() LockoutPolicy {
	return LockoutPolicy{Enabled: true, Threshold: 5, Base: time.Minute, Max: time.Hour}
}

// window returns the lock duration for the given 1-based lockout level, applying
// the geometric (×5) escalation capped at Max. A level <= 0 is treated as level
// 1 (defensive; callers pass the post-increment level).
func (p LockoutPolicy) window(level int) time.Duration {
	if level < 1 {
		level = 1
	}
	w := p.Base
	for i := 1; i < level; i++ {
		w *= 5
		// Cap early to avoid overflow on absurd levels; once at/over Max we stay.
		if w >= p.Max || w <= 0 {
			return p.Max
		}
	}
	if w > p.Max {
		return p.Max
	}
	return w
}
