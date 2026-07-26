package janus

import (
	"context"
	"errors"
	"time"
)

// revokeTimeout bounds the final revoke in RunWithDynamic. The revoke runs on a
// context detached from the caller's, so a lease is still handed back even when
// the caller's context was cancelled — but it must not hang forever.
const revokeTimeout = 30 * time.Second

// RunWithDynamic issues a dynamic credential lease, keeps it renewed in the
// background for as long as fn runs, and revokes it on the way out — including
// when fn returns an error, when ctx is cancelled, and when fn panics.
//
// It is the recommended way to use dynamic credentials: no lease is left
// dangling, and nothing has to remember to renew.
//
//	err := client.RunWithDynamic(ctx, roleID, func(ctx context.Context, l *janus.Lease) error {
//	        db, err := sql.Open("pgx", dsn(l.Username, l.Password))
//	        if err != nil {
//	                return err
//	        }
//	        defer db.Close()
//	        return serve(ctx, db)
//	})
//
// The context handed to fn is derived from ctx and is additionally cancelled if
// auto-renew terminates — the lease hit its max TTL, was revoked out from under
// us, or the token lost access — so long-running work can wind down before the
// credentials stop working. Inspect why via AutoRenewOptions.OnEvent.
//
// Error contract: the error fn returns is never masked. If the final revoke
// also fails, the returned error is errors.Join(fnErr, *RevokeError), so
// errors.Is/errors.As still find the caller's error, and the revoke failure is
// additionally reported to OnEvent with Reason StopReasonRevokeFailed. If fn
// panics, the panic propagates unchanged after the lease is revoked.
func (c *Client) RunWithDynamic(ctx context.Context, roleID string, fn func(context.Context, *Lease) error) error {
	return c.RunWithDynamicOptions(ctx, roleID, nil, fn)
}

// RunWithDynamicOptions is RunWithDynamic with a tunable renewal policy and an
// event handler. Passing nil opts selects the defaults documented on
// StartAutoRenew.
func (c *Client) RunWithDynamicOptions(
	ctx context.Context,
	roleID string,
	opts *AutoRenewOptions,
	fn func(context.Context, *Lease) error,
) (err error) {
	if fn == nil {
		return errors.New("janus: fn is required")
	}
	lease, err := c.IssueDynamic(ctx, roleID)
	if err != nil {
		return err
	}

	// From here on the lease exists server-side, so every exit path — error,
	// panic, cancellation — must revoke it.
	revoke := func() {
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), revokeTimeout)
		defer cancel()
		if rerr := lease.Revoke(rctx); rerr != nil {
			wrapped := &RevokeError{LeaseID: lease.ID, Err: rerr}
			if opts != nil && opts.OnEvent != nil {
				opts.OnEvent(RenewEvent{
					LeaseID:  lease.ID,
					Err:      wrapped,
					Terminal: true,
					Reason:   StopReasonRevokeFailed,
				})
			}
			err = errors.Join(err, wrapped)
		}
	}

	renewer, rerr := lease.StartAutoRenew(ctx, opts)
	if rerr != nil {
		err = rerr
		revoke()
		return err
	}

	fnCtx, cancelFn := context.WithCancel(ctx)
	// Wind fn down if auto-renew ends for any reason. The goroutine always
	// exits, because the deferred Stop below always closes the renewer.
	go func() {
		<-renewer.Done()
		cancelFn()
	}()

	defer func() {
		cancelFn()
		renewer.Stop()
		revoke()
	}()

	err = fn(fnCtx, lease)
	return err
}
