package main

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeVersionFetcher returns a scripted sequence of versions/errors, one per
// call, so poll logic is deterministic without a server. Safe for concurrent
// use: the supervisor polls from its own goroutine while tests inspect calls().
type fakeVersionFetcher struct {
	seq  []int
	errs []error
	mu   sync.Mutex
	i    int
}

func (f *fakeVersionFetcher) currentVersion(string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	idx := f.i
	if idx >= len(f.seq) {
		idx = len(f.seq) - 1
	}
	f.i++
	var err error
	if idx < len(f.errs) {
		err = f.errs[idx]
	}
	return f.seq[idx], err
}

// calls reports how many times currentVersion has been invoked.
func (f *fakeVersionFetcher) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.i
}

func TestWatchDecision(t *testing.T) {
	cases := []struct {
		name               string
		baseline, observed int
		want               bool
	}{
		{"unchanged", 3, 3, false},
		{"increased", 3, 4, true},
		{"jumped", 1, 9, true},
		{"decreased_ignored", 5, 4, false},
		{"zero_baseline_first_version", 0, 1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := watchDecision(tc.baseline, tc.observed); got != tc.want {
				t.Fatalf("watchDecision(%d,%d)=%v want %v", tc.baseline, tc.observed, got, tc.want)
			}
		})
	}
}

func TestPollOnce(t *testing.T) {
	t.Run("changed", func(t *testing.T) {
		vf := &fakeVersionFetcher{seq: []int{5}}
		obs, changed, err := pollOnce(vf, "c1", 4)
		if err != nil || obs != 5 || !changed {
			t.Fatalf("obs=%d changed=%v err=%v", obs, changed, err)
		}
	})
	t.Run("unchanged", func(t *testing.T) {
		vf := &fakeVersionFetcher{seq: []int{4}}
		obs, changed, err := pollOnce(vf, "c1", 4)
		if err != nil || obs != 4 || changed {
			t.Fatalf("obs=%d changed=%v err=%v", obs, changed, err)
		}
	})
	t.Run("error_keeps_baseline", func(t *testing.T) {
		vf := &fakeVersionFetcher{seq: []int{0}, errs: []error{errors.New("boom")}}
		obs, changed, err := pollOnce(vf, "c1", 7)
		if err == nil || obs != 7 || changed {
			t.Fatalf("obs=%d changed=%v err=%v (want baseline kept, err set)", obs, changed, err)
		}
	})
}

func TestParseWatchInterval(t *testing.T) {
	cases := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"10s", 10 * time.Second, false},
		{"1m", time.Minute, false},
		{"5", 5 * time.Second, false}, // bare int → seconds
		{"500ms", 0, true},            // below 1s floor
		{"0s", 0, true},
		{"-3s", 0, true},
		{"garbage", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := parseWatchInterval(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseWatchInterval(%q) want error, got %v", tc.in, got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("parseWatchInterval(%q)=%v,%v want %v", tc.in, got, err, tc.want)
			}
		})
	}
}
