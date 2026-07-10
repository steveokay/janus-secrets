package secretsync

import (
	"testing"
	"time"
)

func TestBackoff(t *testing.T) {
	cases := []struct {
		n    int
		want time.Duration
	}{
		{1, 1 * time.Minute},
		{2, 2 * time.Minute},
		{3, 4 * time.Minute},
		{7, 64 * time.Minute}, // would be 64m → capped
		{20, 1 * time.Hour},   // capped
	}
	for _, c := range cases {
		if got := backoff(c.n); got != capDur(c.want) {
			t.Errorf("backoff(%d) = %v, want %v", c.n, got, capDur(c.want))
		}
	}
}

func capDur(d time.Duration) time.Duration {
	if d > time.Hour {
		return time.Hour
	}
	return d
}
