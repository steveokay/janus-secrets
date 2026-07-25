package main

import (
	"fmt"
	"strconv"
	"time"
)

// versionFetcher returns the bound config's current (highest) config version.
// It is satisfied by *apiClient via currentVersion, and by fakes in tests.
type versionFetcher interface {
	currentVersion(cid string) (int, error)
}

// currentVersion returns the highest config version for cid by listing versions
// (ordered ascending server-side) and taking the last. Value-free: it reads
// only version metadata, never a secret value.
func (c *apiClient) currentVersion(cid string) (int, error) {
	var resp struct {
		Versions []struct {
			Version int `json:"version"`
		} `json:"versions"`
	}
	if err := c.call("GET", "/v1/configs/"+cid+"/versions", nil, &resp); err != nil {
		return 0, err
	}
	max := 0
	for _, v := range resp.Versions {
		if v.Version > max {
			max = v.Version
		}
	}
	if max == 0 {
		return 0, fmt.Errorf("config has no versions")
	}
	return max, nil
}

// ticker abstracts time.Ticker so watch loops are unit-testable without real
// sleeps. Chan delivers a tick each interval; Stop releases resources.
type ticker interface {
	Chan() <-chan time.Time
	Stop()
}

// realTicker adapts *time.Ticker to the ticker interface.
type realTicker struct{ t *time.Ticker }

func (r realTicker) Chan() <-chan time.Time { return r.t.C }
func (r realTicker) Stop()                  { r.t.Stop() }

func newRealTicker(d time.Duration) ticker { return realTicker{t: time.NewTicker(d)} }

// parseWatchInterval validates a --watch-interval flag value, enforcing a small
// floor so a misconfigured 0s/negative value can't hot-spin the poll loop.
func parseWatchInterval(s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil {
		// Allow a bare integer to mean seconds, matching common CLI ergonomics.
		if n, nerr := strconv.Atoi(s); nerr == nil {
			d = time.Duration(n) * time.Second
		} else {
			return 0, fmt.Errorf("invalid --watch-interval %q: %w", s, err)
		}
	}
	if d < time.Second {
		return 0, fmt.Errorf("--watch-interval must be at least 1s, got %s", d)
	}
	return d, nil
}

// watchDecision reports whether the observed version is newer than the baseline
// the caller last acted on. Pure and deterministic — the unit of test coverage
// for the poll/restart logic, independent of any process or clock.
func watchDecision(baseline, observed int) bool {
	return observed > baseline
}

// pollOnce fetches the current version and decides whether an action (restart /
// re-render) is warranted relative to baseline. It returns the observed version
// (so the caller can advance its baseline) and whether the version increased.
// A fetch error is returned but is non-fatal at the caller's discretion.
func pollOnce(vf versionFetcher, cid string, baseline int) (observed int, changed bool, err error) {
	observed, err = vf.currentVersion(cid)
	if err != nil {
		return baseline, false, err
	}
	return observed, watchDecision(baseline, observed), nil
}
