package janus

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"
)

// Renewal-policy defaults used when AutoRenewOptions leaves a field zero.
//
// The policy is "renew at ~2/3 of the remaining TTL, with jitter": after every
// successful renew the renewer recomputes the time left until the lease's
// expiry and sleeps DefaultRenewFraction of it (± DefaultRenewJitter, chosen
// uniformly at random) before renewing again. That leaves roughly a third of
// the TTL as headroom for retries if the server is briefly unreachable, and the
// jitter keeps a fleet of processes that started together from renewing in
// lockstep.
const (
	// DefaultRenewFraction is the fraction of the remaining TTL to wait before
	// attempting the next renew.
	DefaultRenewFraction = 2.0 / 3.0

	// DefaultRenewJitter is the relative jitter applied to each computed wait
	// (0.1 == ±10%).
	DefaultRenewJitter = 0.1

	// DefaultMinRenewInterval is the floor on the computed wait, so a very short
	// or already-elapsed TTL cannot turn the renewer into a hot loop.
	DefaultMinRenewInterval = 1 * time.Second
)

// StopReason explains why a Renewer stopped. It is reported on the terminal
// RenewEvent and via Renewer.Reason.
type StopReason string

const (
	// StopReasonStopped — Renewer.Stop was called. Not an error.
	StopReasonStopped StopReason = "stopped"

	// StopReasonContextDone — the context passed to StartAutoRenew was
	// cancelled or its deadline passed.
	StopReasonContextDone StopReason = "context_done"

	// StopReasonMaxTTL — the server will not extend the lease any further: it
	// has reached the role's max TTL (max_expires_at), so renewing again is
	// pointless. Terminal by design, not a failure of this client.
	StopReasonMaxTTL StopReason = "max_ttl"

	// StopReasonLeaseGone — the lease no longer exists or is no longer active
	// (HTTP 404 / 409). It was revoked or expired server-side.
	StopReasonLeaseGone StopReason = "lease_gone"

	// StopReasonUnauthorized — the token was rejected (HTTP 401).
	StopReasonUnauthorized StopReason = "unauthorized"

	// StopReasonForbidden — the token may no longer renew this lease (HTTP 403).
	StopReasonForbidden StopReason = "forbidden"

	// StopReasonRejected — the server rejected the renew with some other
	// non-retryable 4xx.
	StopReasonRejected StopReason = "rejected"

	// StopReasonExpired — the lease's expiry passed before a renew succeeded
	// (for example the server stayed unreachable through the whole TTL).
	StopReasonExpired StopReason = "expired"

	// StopReasonRevokeFailed — reported by RunWithDynamic when the final revoke
	// failed. It never appears as a Renewer stop reason.
	StopReasonRevokeFailed StopReason = "revoke_failed"
)

// Terminal auto-renew errors. Use errors.Is to test for them.
var (
	// ErrMaxTTLReached means renewal is capped: the lease has reached the
	// server-side max TTL and cannot be extended again. Acquire a new lease.
	ErrMaxTTLReached = errors.New("janus: lease reached its server-side max TTL")

	// ErrLeaseExpired means the lease's expiry passed before any renew
	// succeeded; the credentials are dead.
	ErrLeaseExpired = errors.New("janus: lease expired before it could be renewed")
)

// RenewEvent reports one step of a background renew loop. It is value-free: it
// carries the lease ID and timings only, never the lease password.
type RenewEvent struct {
	// LeaseID is the lease this event concerns.
	LeaseID string

	// Renewed is true when this event reports a successful renew.
	Renewed bool

	// ExpiresAt is the lease expiry known at the time of the event (after a
	// successful renew, the new expiry).
	ExpiresAt time.Time

	// Err is non-nil when a renew attempt failed. A non-terminal Err is a
	// retryable failure (network error, 5xx, 429): the renewer will try again
	// while TTL headroom remains.
	Err error

	// Terminal is true on the final event of a renew loop: the renewer has
	// stopped and will emit nothing further.
	Terminal bool

	// Reason is set on the terminal event.
	Reason StopReason
}

// AutoRenewOptions tunes a background renew loop. The zero value is valid and
// selects the documented defaults.
type AutoRenewOptions struct {
	// Fraction of the remaining TTL to wait before the next renew attempt.
	// Must be in (0, 1]; zero or out-of-range selects DefaultRenewFraction.
	Fraction float64

	// Jitter is the relative jitter applied to each wait (0.1 == ±10%). Zero
	// selects DefaultRenewJitter; a negative value disables jitter.
	Jitter float64

	// MinInterval floors the computed wait. Zero selects
	// DefaultMinRenewInterval.
	MinInterval time.Duration

	// OnEvent, if set, receives every renew event — successes, retryable
	// failures, and the single terminal event. It is called synchronously from
	// the renewer's goroutine, so it must not block for long and must not call
	// Renewer.Stop (which waits for that goroutine).
	//
	// Nothing is logged by the SDK itself; OnEvent is the only place renew
	// activity surfaces. Events never contain the lease password.
	OnEvent func(RenewEvent)

	// Test hooks. Left unexported so the public API stays small; the SDK's own
	// tests substitute a virtual clock and timer so no test waits on wall time.
	now   func() time.Time
	after func(time.Duration) (<-chan time.Time, func())
	rand  func() float64
}

type resolvedOpts struct {
	fraction    float64
	jitter      float64
	minInterval time.Duration
	onEvent     func(RenewEvent)
	now         func() time.Time
	after       func(time.Duration) (<-chan time.Time, func())
	rand        func() float64
}

func realAfter(d time.Duration) (<-chan time.Time, func()) {
	t := time.NewTimer(d)
	return t.C, func() { t.Stop() }
}

func resolve(o *AutoRenewOptions) resolvedOpts {
	r := resolvedOpts{
		fraction:    DefaultRenewFraction,
		jitter:      DefaultRenewJitter,
		minInterval: DefaultMinRenewInterval,
		now:         time.Now,
		after:       realAfter,
		rand:        rand.Float64,
	}
	if o == nil {
		return r
	}
	if o.Fraction > 0 && o.Fraction <= 1 {
		r.fraction = o.Fraction
	}
	if o.Jitter > 0 {
		r.jitter = o.Jitter
	} else if o.Jitter < 0 {
		r.jitter = 0
	}
	if o.MinInterval > 0 {
		r.minInterval = o.MinInterval
	}
	r.onEvent = o.OnEvent
	if o.now != nil {
		r.now = o.now
	}
	if o.after != nil {
		r.after = o.after
	}
	if o.rand != nil {
		r.rand = o.rand
	}
	return r
}

func (o resolvedOpts) emit(e RenewEvent) {
	if o.onEvent != nil {
		o.onEvent(e)
	}
}

// waitFor computes how long to sleep before the next renew attempt, given the
// time left before the lease expires. retry halves the fraction so a failed
// attempt is retried sooner than the normal cadence while still converging on
// the expiry instead of hot-looping.
func (o resolvedOpts) waitFor(remaining time.Duration, retry bool) time.Duration {
	frac := o.fraction
	if retry {
		frac /= 2
	}
	wait := time.Duration(float64(remaining) * frac)
	if o.jitter > 0 {
		wait = time.Duration(float64(wait) * (1 + o.jitter*(2*o.rand()-1)))
	}
	if wait < o.minInterval {
		wait = o.minInterval
	}
	if wait > remaining {
		wait = remaining
	}
	if wait < 0 {
		wait = 0
	}
	return wait
}

// Renewer is a running background renew loop for a single Lease. Create one
// with Lease.StartAutoRenew. It is safe for concurrent use.
type Renewer struct {
	leaseID string

	stopOnce sync.Once
	stopCh   chan struct{}
	doneCh   chan struct{}
	cancel   context.CancelFunc

	mu     sync.Mutex
	err    error
	reason StopReason
}

// Stop halts the renew loop, cancels any renew request in flight, and blocks
// until the renewer's goroutine has exited. It is idempotent: calling it again
// (or after the loop already stopped on its own) returns immediately.
//
// Do not call Stop from an OnEvent callback — that callback runs on the very
// goroutine Stop waits for.
func (r *Renewer) Stop() {
	r.stopOnce.Do(func() {
		close(r.stopCh)
		r.cancel()
	})
	<-r.doneCh
}

// Done returns a channel closed once the renew loop has exited, whether because
// Stop was called or because it hit a terminal condition. After it is closed,
// Err and Reason report the outcome.
func (r *Renewer) Done() <-chan struct{} { return r.doneCh }

// Err returns the terminal error that ended the renew loop, or nil if it
// stopped cleanly (Stop was called). Read it after Done is closed.
//
// Common values to test with errors.Is: ErrMaxTTLReached, ErrLeaseExpired,
// ErrNotFound / ErrForbidden / ErrUnauthorized (via *APIError), context.Canceled.
func (r *Renewer) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

// Reason returns the StopReason recorded when the loop ended. Read it after
// Done is closed.
func (r *Renewer) Reason() StopReason {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reason
}

func (r *Renewer) finish(o resolvedOpts, err error, reason StopReason, expiresAt time.Time) {
	r.mu.Lock()
	r.err = err
	r.reason = reason
	r.mu.Unlock()
	o.emit(RenewEvent{
		LeaseID:   r.leaseID,
		ExpiresAt: expiresAt,
		Err:       err,
		Terminal:  true,
		Reason:    reason,
	})
}

// StartAutoRenew starts a background goroutine that keeps this lease renewed.
//
// Auto-renew is strictly opt-in: nothing in this SDK starts a goroutine unless
// you call this method (or RunWithDynamic, which calls it for you). The caller
// owns the returned Renewer and MUST Stop it — typically with defer — or the
// goroutine lives until the lease reaches a terminal state.
//
// Policy: after each successful renew the loop waits DefaultRenewFraction (2/3)
// of the remaining TTL, ± DefaultRenewJitter (10%), floored at
// DefaultMinRenewInterval, then renews again. See AutoRenewOptions to tune it.
//
// The loop ends — permanently, emitting one terminal RenewEvent — when:
//   - Stop is called (Reason StopReasonStopped, Err nil);
//   - ctx is cancelled (StopReasonContextDone);
//   - the server will not extend the lease further, i.e. it has reached
//     max_expires_at (StopReasonMaxTTL, ErrMaxTTLReached) — renewal is capped
//     server-side, so the loop surfaces that rather than retrying forever;
//   - the server rejects the renew non-retryably: 401, 403, 404, 409 and other
//     4xx (StopReasonUnauthorized / Forbidden / LeaseGone / Rejected);
//   - the lease's expiry passes before any renew succeeds (StopReasonExpired,
//     ErrLeaseExpired).
//
// Retryable failures — network errors, 5xx (including 503 "sealed"), 408 and
// 429 — do not end the loop: they are reported as a non-terminal RenewEvent
// with Err set, and the loop retries with the remaining TTL as its budget.
//
// Errors are never swallowed: every failure reaches OnEvent, and the terminal
// one is also available from Err after Done closes.
func (l *Lease) StartAutoRenew(ctx context.Context, opts *AutoRenewOptions) (*Renewer, error) {
	if l == nil {
		return nil, errors.New("janus: nil lease")
	}
	if l.client == nil {
		return nil, errors.New("janus: lease not bound to a client")
	}
	if l.ID == "" {
		return nil, errors.New("janus: lease has no ID")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	o := resolve(opts)
	rctx, cancel := context.WithCancel(ctx)
	r := &Renewer{
		leaseID: l.ID,
		stopCh:  make(chan struct{}),
		doneCh:  make(chan struct{}),
		cancel:  cancel,
	}
	go r.run(rctx, l, o)
	return r, nil
}

// waitStep sleeps for d. It returns true to continue the loop, or false after
// having recorded a terminal outcome (stop requested, or context done).
func (r *Renewer) waitStep(ctx context.Context, o resolvedOpts, d time.Duration, expiresAt time.Time) bool {
	ch, stopTimer := o.after(d)
	defer stopTimer()
	select {
	case <-r.stopCh:
		r.finish(o, nil, StopReasonStopped, expiresAt)
		return false
	case <-ctx.Done():
		r.finish(o, ctx.Err(), StopReasonContextDone, expiresAt)
		return false
	case <-ch:
		return true
	}
}

func (r *Renewer) run(ctx context.Context, l *Lease, o resolvedOpts) {
	defer r.cancel()
	defer close(r.doneCh)

	retry := false
	for {
		prev := l.Expiry()
		remaining := prev.Sub(o.now())
		if remaining <= 0 {
			r.finish(o, ErrLeaseExpired, StopReasonExpired, prev)
			return
		}
		if !r.waitStep(ctx, o, o.waitFor(remaining, retry), prev) {
			return
		}
		retry = false

		view, err := l.renewView(ctx)
		if err != nil {
			// A stop request beats every other interpretation: cancelling the
			// in-flight request is how Stop unblocks promptly.
			select {
			case <-r.stopCh:
				r.finish(o, nil, StopReasonStopped, prev)
				return
			default:
			}
			if ctx.Err() != nil {
				r.finish(o, ctx.Err(), StopReasonContextDone, prev)
				return
			}
			if reason, terminal := classifyRenewErr(err); terminal {
				r.finish(o, err, reason, prev)
				return
			}
			o.emit(RenewEvent{LeaseID: r.leaseID, ExpiresAt: prev, Err: err})
			retry = true
			continue
		}

		next := l.Expiry()
		o.emit(RenewEvent{LeaseID: r.leaseID, Renewed: true, ExpiresAt: next})

		// Renewal is capped server-side. Two independent signals that the cap
		// has been reached: the server reports expires_at at/after
		// max_expires_at, or the renew simply did not move the expiry forward.
		atMax := !view.MaxExpires.IsZero() && !view.ExpiresAt.Before(view.MaxExpires)
		if atMax || !next.After(prev) {
			r.finish(o, ErrMaxTTLReached, StopReasonMaxTTL, next)
			return
		}
	}
}

// classifyRenewErr decides whether a failed renew ends the loop. 4xx responses
// are terminal (the server will keep saying no), except 408 and 429 which are
// transient. Everything else — network errors, 5xx, a sealed server — is
// retryable while TTL headroom remains.
func classifyRenewErr(err error) (StopReason, bool) {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return "", false
	}
	switch apiErr.Status {
	case 401:
		return StopReasonUnauthorized, true
	case 403:
		return StopReasonForbidden, true
	case 404, 409:
		return StopReasonLeaseGone, true
	case 408, 429:
		return "", false
	}
	if apiErr.Status >= 400 && apiErr.Status < 500 {
		return StopReasonRejected, true
	}
	return "", false
}

// RevokeError reports that revoking a lease failed. RunWithDynamic joins it
// onto the error the caller's function returned, so the caller's own error is
// never replaced — test for either with errors.Is / errors.As.
type RevokeError struct {
	LeaseID string
	Err     error
}

func (e *RevokeError) Error() string {
	return fmt.Sprintf("janus: revoking lease %s failed: %v", e.LeaseID, e.Err)
}

func (e *RevokeError) Unwrap() error { return e.Err }
