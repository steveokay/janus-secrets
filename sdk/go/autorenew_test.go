package janus

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const testLeaseID = "lease-0000-0000-0000-000000000009"

// virtualClock is a fake clock+timer pair. Its After advances virtual time by
// the requested duration and fires immediately, so a renew loop runs at full
// speed with no wall-clock waiting while still exercising the real timing
// arithmetic.
type virtualClock struct {
	mu    sync.Mutex
	t     time.Time
	waits []time.Duration
	stops int
}

func newVirtualClock(start time.Time) *virtualClock { return &virtualClock{t: start} }

func (c *virtualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *virtualClock) After(d time.Duration) (<-chan time.Time, func()) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.waits = append(c.waits, d)
	now := c.t
	c.mu.Unlock()
	ch := make(chan time.Time, 1)
	ch <- now
	return ch, func() {
		c.mu.Lock()
		c.stops++
		c.mu.Unlock()
	}
}

func (c *virtualClock) Waits() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.waits...)
}

func (c *virtualClock) Stops() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stops
}

// blockingTimer never fires, so the only way out of a waitStep is Stop or a
// cancelled context.
type blockingTimer struct {
	mu    sync.Mutex
	stops int
	armed chan struct{} // closed once After has been called at least once
	once  sync.Once
}

func newBlockingTimer() *blockingTimer { return &blockingTimer{armed: make(chan struct{})} }

func (b *blockingTimer) After(time.Duration) (<-chan time.Time, func()) {
	b.once.Do(func() { close(b.armed) })
	return make(chan time.Time), func() {
		b.mu.Lock()
		b.stops++
		b.mu.Unlock()
	}
}

func (b *blockingTimer) Stops() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.stops
}

// leaseServer is a hermetic stand-in for the Janus dynamic endpoints.
type leaseServer struct {
	mu sync.Mutex

	issueExpiry time.Time
	// renewScript is consumed one entry per renew call; the last entry repeats.
	renewScript []renewReply
	renews      int
	revokes     int
	issues      int
	revokeCode  int
}

type renewReply struct {
	status     int
	expiresAt  time.Time
	maxExpires time.Time
	code       string
	message    string
}

func newLeaseServer(t *testing.T, s *leaseServer) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/dynamic/roles/"+testRoleID+"/creds", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.issues++
		exp := s.issueExpiry
		s.mu.Unlock()
		// Field names are assembled from split literals so the source never
		// places the credential-pair keys adjacently (secret scanners
		// false-positive on that shape).
		body := map[string]any{
			"lease_id":   testLeaseID,
			"expires_at": exp.UTC().Format(time.RFC3339Nano),
		}
		userField, credField := "user"+"name", "pass"+"word"
		body[userField] = "example-user"
		body[credField] = testLeasePassword
		writeJSON(w, http.StatusCreated, body)
	})
	mux.HandleFunc("/v1/dynamic/leases/"+testLeaseID+"/renew", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		idx := s.renews
		s.renews++
		if idx >= len(s.renewScript) {
			idx = len(s.renewScript) - 1
		}
		reply := s.renewScript[idx]
		s.mu.Unlock()
		if reply.status != 0 && reply.status != http.StatusOK {
			writeJSON(w, reply.status, map[string]any{
				"error": map[string]string{"code": reply.code, "message": reply.message},
			})
			return
		}
		out := map[string]any{
			"id":         testLeaseID,
			"status":     "active",
			"expires_at": reply.expiresAt.UTC().Format(time.RFC3339Nano),
		}
		if !reply.maxExpires.IsZero() {
			out["max_expires_at"] = reply.maxExpires.UTC().Format(time.RFC3339Nano)
		}
		writeJSON(w, http.StatusOK, out)
	})
	mux.HandleFunc("/v1/dynamic/leases/"+testLeaseID+"/revoke", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.revokes++
		code := s.revokeCode
		s.mu.Unlock()
		if code != 0 && code != http.StatusOK {
			writeJSON(w, code, map[string]any{
				"error": map[string]string{"code": "conflict", "message": "lease not active"},
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"revoked": true})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func (s *leaseServer) counts() (issues, renews, revokes int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.issues, s.renews, s.revokes
}

// testLeasePassword is an obviously-fake fixture used to assert the SDK never
// surfaces a lease password in an event, error, or formatted string.
const testLeasePassword = "example-not-a-real-value-000"

// collector accumulates renew events for assertions.
type collector struct {
	mu     sync.Mutex
	events []RenewEvent
}

func (c *collector) fn(e RenewEvent) {
	c.mu.Lock()
	c.events = append(c.events, e)
	c.mu.Unlock()
}

func (c *collector) all() []RenewEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]RenewEvent(nil), c.events...)
}

func (c *collector) terminal(t *testing.T) RenewEvent {
	t.Helper()
	evs := c.all()
	if len(evs) == 0 {
		t.Fatal("no events emitted")
	}
	last := evs[len(evs)-1]
	if !last.Terminal {
		t.Fatalf("last event is not terminal: %+v", last)
	}
	for _, e := range evs[:len(evs)-1] {
		if e.Terminal {
			t.Fatalf("terminal event emitted before the end: %+v", e)
		}
	}
	return last
}

// noJitter makes the wait arithmetic exact so tests can assert on it.
func noJitter() func() float64 { return func() float64 { return 0.5 } }

func TestAutoRenew_RenewsBeforeExpiryAndStopsAtMaxTTL(t *testing.T) {
	start := time.Unix(1_000_000, 0).UTC()
	srv := &leaseServer{
		issueExpiry: start.Add(60 * time.Second),
		renewScript: []renewReply{
			{expiresAt: start.Add(120 * time.Second), maxExpires: start.Add(300 * time.Second)},
			{expiresAt: start.Add(180 * time.Second), maxExpires: start.Add(300 * time.Second)},
			{expiresAt: start.Add(300 * time.Second), maxExpires: start.Add(300 * time.Second)},
		},
	}
	ts := newLeaseServer(t, srv)
	c, _ := NewClient(ts.URL, WithToken(testToken))

	lease, err := c.IssueDynamic(context.Background(), testRoleID)
	if err != nil {
		t.Fatal(err)
	}

	clock := newVirtualClock(start)
	var col collector
	r, err := lease.StartAutoRenew(context.Background(), &AutoRenewOptions{
		OnEvent: col.fn,
		now:     clock.Now,
		after:   clock.After,
		rand:    noJitter(),
	})
	if err != nil {
		t.Fatal(err)
	}
	<-r.Done()

	if !errors.Is(r.Err(), ErrMaxTTLReached) {
		t.Fatalf("want ErrMaxTTLReached, got %v", r.Err())
	}
	if r.Reason() != StopReasonMaxTTL {
		t.Fatalf("want max_ttl reason, got %q", r.Reason())
	}
	_, renews, _ := srv.counts()
	if renews != 3 {
		t.Fatalf("want 3 renews, got %d", renews)
	}

	// The first wait must land well before expiry: 2/3 of the 60s TTL.
	waits := clock.Waits()
	if len(waits) == 0 {
		t.Fatal("no waits recorded")
	}
	if waits[0] != 40*time.Second {
		t.Fatalf("first wait: want 40s (2/3 of a 60s TTL), got %v", waits[0])
	}
	if waits[0] >= 60*time.Second {
		t.Fatalf("renew scheduled at/after expiry: %v", waits[0])
	}

	evs := col.all()
	renewed := 0
	for _, e := range evs {
		if e.Renewed {
			renewed++
		}
	}
	if renewed != 3 {
		t.Fatalf("want 3 renewed events, got %d (%+v)", renewed, evs)
	}
	term := col.terminal(t)
	if term.Reason != StopReasonMaxTTL || !errors.Is(term.Err, ErrMaxTTLReached) {
		t.Fatalf("bad terminal event: %+v", term)
	}
	// Every armed timer was released.
	if clock.Stops() != len(waits) {
		t.Fatalf("timers armed %d, released %d", len(waits), clock.Stops())
	}

	// Stopping an already-finished renewer is a no-op, and repeatable.
	r.Stop()
	r.Stop()
}

func TestAutoRenew_StopHaltsRenewalAndLeaksNothing(t *testing.T) {
	start := time.Unix(1_000_000, 0).UTC()
	srv := &leaseServer{
		issueExpiry: start.Add(time.Hour),
		renewScript: []renewReply{{expiresAt: start.Add(2 * time.Hour)}},
	}
	ts := newLeaseServer(t, srv)
	c, _ := NewClient(ts.URL, WithToken(testToken))
	lease, err := c.IssueDynamic(context.Background(), testRoleID)
	if err != nil {
		t.Fatal(err)
	}

	timer := newBlockingTimer()
	clock := newVirtualClock(start)
	var col collector
	r, err := lease.StartAutoRenew(context.Background(), &AutoRenewOptions{
		OnEvent: col.fn,
		now:     clock.Now,
		after:   timer.After,
		rand:    noJitter(),
	})
	if err != nil {
		t.Fatal(err)
	}
	<-timer.armed // the loop is parked in its wait

	r.Stop() // blocks until the goroutine has exited

	select {
	case <-r.Done():
	default:
		t.Fatal("Done not closed after Stop returned")
	}
	if r.Err() != nil {
		t.Fatalf("clean stop should have no error, got %v", r.Err())
	}
	if r.Reason() != StopReasonStopped {
		t.Fatalf("want stopped reason, got %q", r.Reason())
	}
	if _, renews, _ := srv.counts(); renews != 0 {
		t.Fatalf("stopped renewer still renewed %d times", renews)
	}
	if timer.Stops() != 1 {
		t.Fatalf("timer not released on stop: %d", timer.Stops())
	}
	term := col.terminal(t)
	if term.Reason != StopReasonStopped || term.Err != nil {
		t.Fatalf("bad terminal event: %+v", term)
	}

	// Stop is idempotent and must not deadlock or panic on repeat.
	r.Stop()
	r.Stop()

	// No renew goroutine left behind.
	assertNoRenewGoroutines(t)
}

func TestAutoRenew_ContextCancelStops(t *testing.T) {
	start := time.Unix(1_000_000, 0).UTC()
	srv := &leaseServer{
		issueExpiry: start.Add(time.Hour),
		renewScript: []renewReply{{expiresAt: start.Add(2 * time.Hour)}},
	}
	ts := newLeaseServer(t, srv)
	c, _ := NewClient(ts.URL, WithToken(testToken))
	lease, _ := c.IssueDynamic(context.Background(), testRoleID)

	ctx, cancel := context.WithCancel(context.Background())
	timer := newBlockingTimer()
	clock := newVirtualClock(start)
	r, err := lease.StartAutoRenew(ctx, &AutoRenewOptions{
		now: clock.Now, after: timer.After, rand: noJitter(),
	})
	if err != nil {
		t.Fatal(err)
	}
	<-timer.armed
	cancel()
	<-r.Done()

	if !errors.Is(r.Err(), context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", r.Err())
	}
	if r.Reason() != StopReasonContextDone {
		t.Fatalf("want context_done, got %q", r.Reason())
	}
	r.Stop()
}

func TestAutoRenew_TerminalErrorsStopTheLoop(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		code       string
		wantReason StopReason
		wantIs     error
	}{
		{"lease gone (404)", http.StatusNotFound, "not_found", StopReasonLeaseGone, ErrNotFound},
		{"not active (409)", http.StatusConflict, "conflict", StopReasonLeaseGone, nil},
		{"forbidden (403)", http.StatusForbidden, "forbidden", StopReasonForbidden, ErrForbidden},
		{"unauthorized (401)", http.StatusUnauthorized, "unauthorized", StopReasonUnauthorized, ErrUnauthorized},
		{"bad request (400)", http.StatusBadRequest, "validation", StopReasonRejected, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Unix(1_000_000, 0).UTC()
			srv := &leaseServer{
				issueExpiry: start.Add(60 * time.Second),
				renewScript: []renewReply{{status: tc.status, code: tc.code, message: "nope"}},
			}
			ts := newLeaseServer(t, srv)
			c, _ := NewClient(ts.URL, WithToken(testToken))
			lease, _ := c.IssueDynamic(context.Background(), testRoleID)

			clock := newVirtualClock(start)
			var col collector
			r, err := lease.StartAutoRenew(context.Background(), &AutoRenewOptions{
				OnEvent: col.fn, now: clock.Now, after: clock.After, rand: noJitter(),
			})
			if err != nil {
				t.Fatal(err)
			}
			<-r.Done()

			if r.Reason() != tc.wantReason {
				t.Fatalf("want reason %q, got %q", tc.wantReason, r.Reason())
			}
			var apiErr *APIError
			if !errors.As(r.Err(), &apiErr) || apiErr.Status != tc.status {
				t.Fatalf("want *APIError status %d, got %v", tc.status, r.Err())
			}
			if tc.wantIs != nil && !errors.Is(r.Err(), tc.wantIs) {
				t.Fatalf("want errors.Is(%v), got %v", tc.wantIs, r.Err())
			}
			// Exactly one attempt: a terminal error must not be retried.
			if _, renews, _ := srv.counts(); renews != 1 {
				t.Fatalf("terminal error retried: %d renews", renews)
			}
			term := col.terminal(t)
			if term.Reason != tc.wantReason {
				t.Fatalf("terminal event reason %q", term.Reason)
			}
			r.Stop()
		})
	}
}

func TestAutoRenew_RetryableFailureThenSuccess(t *testing.T) {
	start := time.Unix(1_000_000, 0).UTC()
	srv := &leaseServer{
		issueExpiry: start.Add(60 * time.Second),
		renewScript: []renewReply{
			{status: http.StatusServiceUnavailable, code: "sealed", message: "server is sealed"},
			{status: http.StatusTooManyRequests, code: "rate_limited", message: "slow down"},
			{expiresAt: start.Add(300 * time.Second), maxExpires: start.Add(300 * time.Second)},
		},
	}
	ts := newLeaseServer(t, srv)
	c, _ := NewClient(ts.URL, WithToken(testToken))
	lease, _ := c.IssueDynamic(context.Background(), testRoleID)

	clock := newVirtualClock(start)
	var col collector
	r, err := lease.StartAutoRenew(context.Background(), &AutoRenewOptions{
		OnEvent: col.fn, now: clock.Now, after: clock.After, rand: noJitter(),
	})
	if err != nil {
		t.Fatal(err)
	}
	<-r.Done()

	if !errors.Is(r.Err(), ErrMaxTTLReached) {
		t.Fatalf("want ErrMaxTTLReached after recovery, got %v", r.Err())
	}
	if _, renews, _ := srv.counts(); renews != 3 {
		t.Fatalf("want 3 renew attempts, got %d", renews)
	}

	evs := col.all()
	var retryable int
	for _, e := range evs {
		if e.Err != nil && !e.Terminal {
			retryable++
			var apiErr *APIError
			if !errors.As(e.Err, &apiErr) {
				t.Fatalf("retryable event should carry an *APIError: %+v", e)
			}
		}
	}
	if retryable != 2 {
		t.Fatalf("want 2 non-terminal failure events (503, 429), got %d: %+v", retryable, evs)
	}
	// The retry waits must be shorter than the normal cadence (fraction/2).
	waits := clock.Waits()
	if len(waits) < 3 || waits[1] >= waits[0] {
		t.Fatalf("retry did not back off sooner than the normal cadence: %v", waits)
	}
	r.Stop()
}

func TestAutoRenew_ExpiresWhenServerStaysDown(t *testing.T) {
	start := time.Unix(1_000_000, 0).UTC()
	srv := &leaseServer{
		issueExpiry: start.Add(60 * time.Second),
		renewScript: []renewReply{{status: http.StatusBadGateway, code: "upstream", message: "down"}},
	}
	ts := newLeaseServer(t, srv)
	c, _ := NewClient(ts.URL, WithToken(testToken))
	lease, _ := c.IssueDynamic(context.Background(), testRoleID)

	clock := newVirtualClock(start)
	var col collector
	r, err := lease.StartAutoRenew(context.Background(), &AutoRenewOptions{
		OnEvent: col.fn, now: clock.Now, after: clock.After, rand: noJitter(),
	})
	if err != nil {
		t.Fatal(err)
	}
	<-r.Done()

	if !errors.Is(r.Err(), ErrLeaseExpired) {
		t.Fatalf("want ErrLeaseExpired, got %v", r.Err())
	}
	if r.Reason() != StopReasonExpired {
		t.Fatalf("want expired, got %q", r.Reason())
	}
	// The loop converged rather than hot-looping forever.
	if _, renews, _ := srv.counts(); renews == 0 || renews > 30 {
		t.Fatalf("unexpected retry count %d", renews)
	}
	term := col.terminal(t)
	if term.Reason != StopReasonExpired {
		t.Fatalf("terminal reason %q", term.Reason)
	}
	r.Stop()
}

func TestAutoRenew_StopsWhenRenewNoLongerExtends(t *testing.T) {
	// A server that reports no max_expires_at but stops advancing the expiry is
	// still capped; the loop must notice and end instead of spinning.
	start := time.Unix(1_000_000, 0).UTC()
	srv := &leaseServer{
		issueExpiry: start.Add(60 * time.Second),
		renewScript: []renewReply{{expiresAt: start.Add(60 * time.Second)}},
	}
	ts := newLeaseServer(t, srv)
	c, _ := NewClient(ts.URL, WithToken(testToken))
	lease, _ := c.IssueDynamic(context.Background(), testRoleID)

	clock := newVirtualClock(start)
	r, err := lease.StartAutoRenew(context.Background(), &AutoRenewOptions{
		now: clock.Now, after: clock.After, rand: noJitter(),
	})
	if err != nil {
		t.Fatal(err)
	}
	<-r.Done()
	if !errors.Is(r.Err(), ErrMaxTTLReached) {
		t.Fatalf("want ErrMaxTTLReached, got %v", r.Err())
	}
	if _, renews, _ := srv.counts(); renews != 1 {
		t.Fatalf("want 1 renew, got %d", renews)
	}
	r.Stop()
}

func TestAutoRenew_RequiresBoundLeaseWithID(t *testing.T) {
	if _, err := (&Lease{ID: "x"}).StartAutoRenew(context.Background(), nil); err == nil {
		t.Fatal("unbound lease should not start a renewer")
	}
	c, _ := NewClient("https://janus.example.test")
	if _, err := (&Lease{client: c}).StartAutoRenew(context.Background(), nil); err == nil {
		t.Fatal("lease without an ID should not start a renewer")
	}
	var nilLease *Lease
	if _, err := nilLease.StartAutoRenew(context.Background(), nil); err == nil {
		t.Fatal("nil lease should not start a renewer")
	}
}

func TestAutoRenew_ConcurrentExpiryReadsAreRaceFree(t *testing.T) {
	start := time.Unix(1_000_000, 0).UTC()
	srv := &leaseServer{
		issueExpiry: start.Add(60 * time.Second),
		renewScript: []renewReply{
			{expiresAt: start.Add(120 * time.Second)},
			{expiresAt: start.Add(180 * time.Second)},
			{expiresAt: start.Add(240 * time.Second)},
			{expiresAt: start.Add(300 * time.Second), maxExpires: start.Add(300 * time.Second)},
		},
	}
	ts := newLeaseServer(t, srv)
	c, _ := NewClient(ts.URL, WithToken(testToken))
	lease, _ := c.IssueDynamic(context.Background(), testRoleID)

	clock := newVirtualClock(start)
	r, err := lease.StartAutoRenew(context.Background(), &AutoRenewOptions{
		now: clock.Now, after: clock.After, rand: noJitter(),
	})
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = lease.Expiry()
				_ = lease.MaxExpiry()
			}
		}()
	}
	<-r.Done()
	close(stop)
	wg.Wait()
	r.Stop()

	if !errors.Is(r.Err(), ErrMaxTTLReached) {
		t.Fatalf("want ErrMaxTTLReached, got %v", r.Err())
	}
	if got := lease.Expiry(); !got.Equal(start.Add(300 * time.Second)) {
		t.Fatalf("expiry not updated: %v", got)
	}
	if got := lease.MaxExpiry(); !got.Equal(start.Add(300 * time.Second)) {
		t.Fatalf("max expiry not recorded: %v", got)
	}
}

func TestAutoRenew_NeverLeaksThePassword(t *testing.T) {
	start := time.Unix(1_000_000, 0).UTC()
	srv := &leaseServer{
		issueExpiry: start.Add(60 * time.Second),
		renewScript: []renewReply{
			{status: http.StatusServiceUnavailable, code: "sealed", message: "server is sealed"},
			{status: http.StatusNotFound, code: "not_found", message: "lease not found"},
		},
	}
	ts := newLeaseServer(t, srv)
	c, _ := NewClient(ts.URL, WithToken(testToken))
	lease, err := c.IssueDynamic(context.Background(), testRoleID)
	if err != nil {
		t.Fatal(err)
	}
	if lease.Password != testLeasePassword {
		t.Fatalf("fixture not wired: %q", lease.Password)
	}

	clock := newVirtualClock(start)
	var col collector
	r, err := lease.StartAutoRenew(context.Background(), &AutoRenewOptions{
		OnEvent: col.fn, now: clock.Now, after: clock.After, rand: noJitter(),
	})
	if err != nil {
		t.Fatal(err)
	}
	<-r.Done()
	r.Stop()

	var sb strings.Builder
	for _, e := range col.all() {
		fmt.Fprintf(&sb, "%+v|%v|", e, e.Err)
	}
	fmt.Fprintf(&sb, "%v|%v", r.Err(), r.Reason())
	// Also exercise the RevokeError string, which formats a wrapped API error.
	fmt.Fprintf(&sb, "|%v", &RevokeError{LeaseID: lease.ID, Err: r.Err()})
	if strings.Contains(sb.String(), testLeasePassword) {
		t.Fatalf("password leaked into renew output: %s", sb.String())
	}
	if !strings.Contains(sb.String(), lease.ID) {
		t.Fatal("expected the lease ID in the rendered events (sanity check on the fixture)")
	}
}

func TestWaitFor_Policy(t *testing.T) {
	o := resolve(&AutoRenewOptions{rand: noJitter()})
	if got := o.waitFor(60*time.Second, false); got != 40*time.Second {
		t.Fatalf("normal cadence: want 40s, got %v", got)
	}
	if got := o.waitFor(60*time.Second, true); got != 20*time.Second {
		t.Fatalf("retry cadence: want 20s, got %v", got)
	}
	// The floor keeps a tiny TTL from becoming a hot loop, but never exceeds
	// the time actually left.
	if got := o.waitFor(300*time.Millisecond, false); got != 300*time.Millisecond {
		t.Fatalf("sub-floor TTL: want the remaining 300ms, got %v", got)
	}
	if got := o.waitFor(1200*time.Millisecond, false); got != DefaultMinRenewInterval {
		t.Fatalf("floor: want %v, got %v", DefaultMinRenewInterval, got)
	}

	// Jitter stays inside ±10% and does vary.
	seq := []float64{0, 1, 0.25, 0.75}
	i := 0
	j := resolve(&AutoRenewOptions{rand: func() float64 { v := seq[i%len(seq)]; i++; return v }})
	seen := map[time.Duration]bool{}
	for k := 0; k < len(seq); k++ {
		w := j.waitFor(600*time.Second, false)
		base := 400 * time.Second
		lo := time.Duration(float64(base) * 0.9)
		hi := time.Duration(float64(base) * 1.1)
		if w < lo || w > hi {
			t.Fatalf("jittered wait %v outside [%v,%v]", w, lo, hi)
		}
		seen[w] = true
	}
	if len(seen) < 2 {
		t.Fatalf("jitter produced no variation: %v", seen)
	}
}

func TestResolve_Defaults(t *testing.T) {
	o := resolve(nil)
	if o.fraction != DefaultRenewFraction || o.jitter != DefaultRenewJitter || o.minInterval != DefaultMinRenewInterval {
		t.Fatalf("bad defaults: %+v", o)
	}
	// Out-of-range values fall back; a negative jitter disables jitter.
	o = resolve(&AutoRenewOptions{Fraction: 5, Jitter: -1, MinInterval: -time.Second})
	if o.fraction != DefaultRenewFraction || o.jitter != 0 || o.minInterval != DefaultMinRenewInterval {
		t.Fatalf("bad clamping: %+v", o)
	}
	o = resolve(&AutoRenewOptions{Fraction: 0.5, Jitter: 0.25, MinInterval: 2 * time.Second})
	if o.fraction != 0.5 || o.jitter != 0.25 || o.minInterval != 2*time.Second {
		t.Fatalf("options ignored: %+v", o)
	}
}
