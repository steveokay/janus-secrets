package janus

import (
	"context"
	"errors"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"
)

// assertNoRenewGoroutines fails if any goroutine is still parked inside the
// SDK's background renew machinery. It is deliberately more precise than a
// NumGoroutine delta, which an httptest server's own connection handlers make
// unreliable. It polls briefly because a goroutine can be a few instructions
// from returning when Stop's channel close is observed.
func assertNoRenewGoroutines(t *testing.T) {
	t.Helper()
	needles := []string{
		"janus.(*Renewer).run",
		"janus.(*Client).RunWithDynamicOptions.func",
	}
	buf := make([]byte, 1<<20)
	var dump string
	for i := 0; i < 200; i++ {
		dump = string(buf[:runtime.Stack(buf, true)])
		clean := true
		for _, n := range needles {
			if strings.Contains(dump, n) {
				clean = false
			}
		}
		if clean {
			return
		}
		runtime.Gosched()
	}
	t.Fatalf("background renew goroutine still running:\n%s", dump)
}

// runServer builds a lease server whose issued lease expires far enough out
// that the default (real-clock) renew cadence never fires during the test.
func runServer(t *testing.T) (*leaseServer, *Client) {
	t.Helper()
	srv := &leaseServer{
		issueExpiry: time.Now().Add(time.Hour),
		renewScript: []renewReply{{expiresAt: time.Now().Add(2 * time.Hour)}},
	}
	ts := newLeaseServer(t, srv)
	c, err := NewClient(ts.URL, WithToken(testToken))
	if err != nil {
		t.Fatal(err)
	}
	return srv, c
}

func TestRunWithDynamic_RevokesOnSuccess(t *testing.T) {
	srv, c := runServer(t)

	var sawLease *Lease
	err := c.RunWithDynamic(context.Background(), testRoleID, func(ctx context.Context, l *Lease) error {
		sawLease = l
		if l.Username != "example-user" || l.Password != testLeasePassword {
			t.Errorf("lease not populated: %+v", l)
		}
		if ctx.Err() != nil {
			t.Errorf("fn context already cancelled: %v", ctx.Err())
		}
		return nil
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	issues, _, revokes := srv.counts()
	if issues != 1 || revokes != 1 {
		t.Fatalf("want 1 issue and 1 revoke, got issues=%d revokes=%d", issues, revokes)
	}
	if sawLease == nil {
		t.Fatal("fn never ran")
	}
	assertNoRenewGoroutines(t)
}

func TestRunWithDynamic_RevokesOnError(t *testing.T) {
	srv, c := runServer(t)
	sentinel := errors.New("boom")

	err := c.RunWithDynamic(context.Background(), testRoleID, func(context.Context, *Lease) error {
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("caller error masked: %v", err)
	}
	if _, _, revokes := srv.counts(); revokes != 1 {
		t.Fatalf("want 1 revoke, got %d", revokes)
	}
}

func TestRunWithDynamic_RevokesOnPanic(t *testing.T) {
	srv, c := runServer(t)

	func() {
		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("panic did not propagate")
			}
			if s, _ := r.(string); s != "kaboom" {
				t.Fatalf("wrong panic value: %v", r)
			}
		}()
		_ = c.RunWithDynamic(context.Background(), testRoleID, func(context.Context, *Lease) error {
			panic("kaboom")
		})
	}()

	if _, _, revokes := srv.counts(); revokes != 1 {
		t.Fatalf("lease not revoked on panic: revokes=%d", revokes)
	}
}

func TestRunWithDynamic_RevokesOnContextCancel(t *testing.T) {
	srv, c := runServer(t)
	ctx, cancel := context.WithCancel(context.Background())

	err := c.RunWithDynamic(ctx, testRoleID, func(fnCtx context.Context, _ *Lease) error {
		cancel()
		<-fnCtx.Done()
		return fnCtx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	// The revoke runs on a detached context, so it still reaches the server.
	if _, _, revokes := srv.counts(); revokes != 1 {
		t.Fatalf("want 1 revoke after cancellation, got %d", revokes)
	}
}

func TestRunWithDynamic_RevokeFailureDoesNotMaskCallerError(t *testing.T) {
	srv, c := runServer(t)
	srv.revokeCode = http.StatusConflict
	sentinel := errors.New("caller failed")

	var events []RenewEvent
	err := c.RunWithDynamicOptions(context.Background(), testRoleID,
		&AutoRenewOptions{OnEvent: func(e RenewEvent) { events = append(events, e) }},
		func(context.Context, *Lease) error { return sentinel },
	)
	if !errors.Is(err, sentinel) {
		t.Fatalf("caller error masked by the revoke failure: %v", err)
	}
	var revErr *RevokeError
	if !errors.As(err, &revErr) {
		t.Fatalf("revoke failure not reported alongside: %v", err)
	}
	if revErr.LeaseID != testLeaseID {
		t.Fatalf("revoke error lost the lease ID: %+v", revErr)
	}
	var reported bool
	for _, e := range events {
		if e.Reason == StopReasonRevokeFailed && e.Err != nil {
			reported = true
		}
	}
	if !reported {
		t.Fatalf("revoke failure not reported to OnEvent: %+v", events)
	}
	if strings.Contains(err.Error(), testLeasePassword) {
		t.Fatal("password leaked into the run error")
	}
}

func TestRunWithDynamic_RevokeFailureSurfacesOnSuccess(t *testing.T) {
	srv, c := runServer(t)
	srv.revokeCode = http.StatusConflict

	err := c.RunWithDynamic(context.Background(), testRoleID, func(context.Context, *Lease) error {
		return nil
	})
	var revErr *RevokeError
	if !errors.As(err, &revErr) {
		t.Fatalf("want a *RevokeError when the caller succeeded but revoke failed, got %v", err)
	}
}

func TestRunWithDynamic_CancelsFnWhenRenewalTerminates(t *testing.T) {
	// The lease is already at its max TTL, so the very first renew ends the
	// loop — and that must cancel the context handed to fn.
	start := time.Now()
	srv := &leaseServer{
		issueExpiry: start.Add(2 * time.Second),
		renewScript: []renewReply{{
			expiresAt:  start.Add(2 * time.Second),
			maxExpires: start.Add(2 * time.Second),
		}},
	}
	ts := newLeaseServer(t, srv)
	c, _ := NewClient(ts.URL, WithToken(testToken))

	var terminal RenewEvent
	err := c.RunWithDynamicOptions(context.Background(), testRoleID,
		&AutoRenewOptions{
			MinInterval: time.Millisecond,
			Jitter:      -1, // deterministic
			OnEvent: func(e RenewEvent) {
				if e.Terminal {
					terminal = e
				}
			},
		},
		func(fnCtx context.Context, _ *Lease) error {
			<-fnCtx.Done() // blocks until auto-renew gives up
			return nil
		},
	)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if terminal.Reason != StopReasonMaxTTL || !errors.Is(terminal.Err, ErrMaxTTLReached) {
		t.Fatalf("want a max-TTL terminal event, got %+v", terminal)
	}
	if _, _, revokes := srv.counts(); revokes != 1 {
		t.Fatalf("want 1 revoke, got %d", revokes)
	}
}

func TestRunWithDynamic_RejectsNilFn(t *testing.T) {
	_, c := runServer(t)
	if err := c.RunWithDynamic(context.Background(), testRoleID, nil); err == nil {
		t.Fatal("nil fn should error")
	}
}

func TestRunWithDynamic_IssueFailurePropagates(t *testing.T) {
	c, _ := NewClient("https://127.0.0.1:1", WithToken(testToken),
		WithHTTPClient(&http.Client{Timeout: 2 * time.Second}))
	ran := false
	err := c.RunWithDynamic(context.Background(), testRoleID, func(context.Context, *Lease) error {
		ran = true
		return nil
	})
	if err == nil {
		t.Fatal("want an issue error")
	}
	if ran {
		t.Fatal("fn ran despite a failed issue")
	}
}
