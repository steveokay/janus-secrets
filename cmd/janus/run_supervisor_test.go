package main

import (
	"bytes"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeTicker is a manually-driven ticker: tests call tick() to emit and close()
// to stop the loop, so the poll/restart logic runs without real sleeps.
type fakeTicker struct {
	c      chan time.Time
	closed bool
	mu     sync.Mutex
}

func newFakeTicker() *fakeTicker { return &fakeTicker{c: make(chan time.Time, 1)} }

func (f *fakeTicker) Chan() <-chan time.Time { return f.c }
func (f *fakeTicker) Stop()                  {}

func (f *fakeTicker) tick() { f.c <- time.Now() }
func (f *fakeTicker) close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		close(f.c)
		f.closed = true
	}
}

// fakeChild is an in-process childProc: it records lifecycle events and blocks
// in Wait until Terminate (or an explicit finish) is signalled.
type fakeChild struct {
	env      []string
	started  atomic.Bool
	waitDone chan struct{}
}

func (c *fakeChild) Start() error {
	c.started.Store(true)
	return nil
}

func (c *fakeChild) Wait() error {
	<-c.waitDone
	return nil
}

func (c *fakeChild) Terminate(time.Duration) {
	select {
	case <-c.waitDone:
	default:
		close(c.waitDone)
	}
}

func TestSupervisorRestartsOnVersionBump(t *testing.T) {
	var mu sync.Mutex
	var spawned []*fakeChild
	factory := func(_ string, _, env []string, _ io.Reader, _, _ io.Writer) childProc {
		mu.Lock()
		defer mu.Unlock()
		ch := &fakeChild{env: env, waitDone: make(chan struct{})}
		spawned = append(spawned, ch)
		return ch
	}

	// baseline read = 1; tick #1 sees 1 (no change); tick #2 sees 2 (restart).
	vf := &fakeVersionFetcher{seq: []int{1, 1, 2}}
	fake := newFakeTicker()

	envCalls := 0
	sup := &runSupervisor{
		name: "svc",
		envFor: func() ([]string, error) {
			envCalls++
			return []string{"V=2"}, nil
		},
		newCmd:   factory,
		grace:    time.Millisecond,
		errOut:   &bytes.Buffer{},
		vf:       vf,
		cid:      "c1",
		tickerFn: func(time.Duration) ticker { return fake },
	}

	done := make(chan struct{})
	go func() {
		_ = sup.run(time.Second, []string{"V=1"})
		close(done)
	}()

	// Wait for the first child to be spawned + started.
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(spawned) == 1 && spawned[0].started.Load()
	})

	fake.tick() // no change
	fake.tick() // 1→2, triggers a graceful restart

	// Second child should spawn with the refetched env.
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(spawned) == 2 && spawned[1].started.Load()
	})

	mu.Lock()
	if got := spawned[1].env; len(got) != 1 || got[0] != "V=2" {
		mu.Unlock()
		t.Fatalf("restarted child env = %v, want [V=2]", got)
	}
	first := spawned[0]
	second := spawned[1]
	mu.Unlock()

	// First child must have been terminated; finish the second so run() returns.
	select {
	case <-first.waitDone:
	default:
		t.Fatal("first child was not terminated on restart")
	}
	second.Terminate(0)

	<-done
	if envCalls != 1 {
		t.Fatalf("envFor called %d times, want 1 (once per restart)", envCalls)
	}
}

// TestSupervisorNoRestartWhenUnchanged verifies a steady version does not spawn
// a second child.
func TestSupervisorNoRestartWhenUnchanged(t *testing.T) {
	var mu sync.Mutex
	var count int
	var only *fakeChild
	factory := func(_ string, _, _ []string, _ io.Reader, _, _ io.Writer) childProc {
		mu.Lock()
		defer mu.Unlock()
		count++
		only = &fakeChild{waitDone: make(chan struct{})}
		return only
	}
	vf := &fakeVersionFetcher{seq: []int{4, 4, 4}}
	fake := newFakeTicker()
	sup := &runSupervisor{
		name:     "svc",
		envFor:   func() ([]string, error) { return nil, nil },
		newCmd:   factory,
		grace:    time.Millisecond,
		errOut:   &bytes.Buffer{},
		vf:       vf,
		cid:      "c1",
		tickerFn: func(time.Duration) ticker { return fake },
	}
	done := make(chan struct{})
	go func() { _ = sup.run(time.Second, nil); close(done) }()

	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return count == 1 })
	fake.tick()
	fake.tick()
	// Give the loop a moment, then end the child.
	waitFor(t, func() bool { return vf.calls() >= 3 })

	mu.Lock()
	if count != 1 {
		mu.Unlock()
		t.Fatalf("spawned %d children, want 1 (no restart on steady version)", count)
	}
	ch := only
	mu.Unlock()
	ch.Terminate(0)
	<-done
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not met before deadline")
}
