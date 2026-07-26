# Reading secrets from Go (the Go SDK)

Janus ships a typed Go client SDK for apps that want to read secrets
**programmatically, in-process** — as an alternative to injecting them with
`janus run`. It talks to the Janus `/v1` REST API over HTTP using a scoped
service token, with an in-process TTL cache so repeated reads don't re-hit the
server.

The SDK lives in [`sdk/go/`](../../sdk/go/) as its **own Go module**
(`github.com/steveokay/janus-secrets/sdk/go`), so importing it doesn't pull in
the Janus server's dependency tree. It has **zero external dependencies**.

## Install

```
go get github.com/steveokay/janus-secrets/sdk/go
```

## Authenticate

Mint a scoped **service token** (see
[Service tokens](service-tokens.md)) with read access to the config you want
to read, then pass it via `WithToken`:

```go
client, err := janus.NewClient("https://janus.example.com",
	janus.WithToken(os.Getenv("JANUS_TOKEN")),
	janus.WithCacheTTL(30*time.Second), // default; 0 disables caching
)
```

## Read secrets

```go
ctx := context.Background()

// All resolved secrets for a config.
secrets, err := client.GetSecrets(ctx, configID) // map[string]string

// A single key.
apiKey, err := client.GetSecret(ctx, configID, "API_KEY")
```

Reads go through the **audited reveal path** — each cache miss is recorded
server-side as a `secret.reveal` event (visible in the [audit
log](../operations.md)). That is expected.

## Caching

- Config reads are cached **in memory only** for the TTL; the cache is never
  written to disk and is lost on process exit.
- Within the TTL, repeated reads are served from memory. On expiry (or after
  `client.Refresh(configID)`), the next read re-fetches.
- Pass `WithCacheTTL(0)` to disable caching (every read hits the server).

## Error handling

Non-2xx responses become an `*APIError{Status, Code, Message}` that wraps a
sentinel for common cases:

```go
switch {
case errors.Is(err, janus.ErrUnauthorized): // 401
case errors.Is(err, janus.ErrForbidden):    // 403
case errors.Is(err, janus.ErrNotFound):     // 404
case errors.Is(err, janus.ErrSealed):       // 503 — retry after unseal
}
```

## Dynamic credentials

If the token can issue [dynamic Postgres credentials](../openapi.yaml),
`client.IssueDynamic(ctx, roleID)` returns a `*Lease` (one-time password held
in memory only) with `Renew` / `Revoke` methods.

### `RunWithDynamic` — the recommended way

Managing a lease by hand means remembering to renew it *and* to revoke it on
every exit path. `RunWithDynamic` does both:

```go
err := client.RunWithDynamic(ctx, roleID, func(ctx context.Context, lease *janus.Lease) error {
	db, err := sql.Open("pgx", dsn(lease.Username, lease.Password))
	if err != nil {
		return err
	}
	defer db.Close()
	return serve(ctx, db) // auto-renew runs for as long as this does
})
```

The lease is issued, kept renewed in the background, and revoked on the way
out — on success, on an error return, and on a panic (the panic propagates
unchanged after the revoke). The `ctx` handed to your function is additionally
cancelled when auto-renew terminates — max TTL reached, lease revoked out from
under you, token lost access — so long-running work can wind down *before* the
credentials stop working.

Use `RunWithDynamicOptions(ctx, roleID, opts, fn)` to tune the policy or attach
an event handler.

### Background auto-renew

Auto-renew is **opt-in**: nothing in the SDK starts a goroutine unless you call
`StartAutoRenew` (or `RunWithDynamic`, which calls it for you).

```go
renewer, err := lease.StartAutoRenew(ctx, &janus.AutoRenewOptions{
	OnEvent: func(e janus.RenewEvent) { /* never contains the password */ },
})
if err != nil {
	return err
}
defer renewer.Stop() // idempotent; blocks until the goroutine has exited
```

**Renewal policy.** After each successful renew the loop waits **2/3 of the
remaining TTL**, ± **10% jitter** (so a fleet doesn't stampede the server),
floored at **1s**, then renews again. A failed-but-retryable attempt is retried
at half that fraction, so retries converge on the expiry instead of hot-looping.
Tune with `Fraction`, `Jitter`, and `MinInterval`.

**Error-handling contract.** Nothing is swallowed. Every attempt is reported to
`OnEvent`, and the loop ends with exactly one terminal event whose `Reason` and
`Err` are also readable from `renewer.Reason()` / `renewer.Err()` after
`<-renewer.Done()`:

| Outcome | `Reason` | `Err` |
| --- | --- | --- |
| `Stop()` called | `stopped` | `nil` |
| `ctx` cancelled | `context_done` | `context.Canceled` / `DeadlineExceeded` |
| server won't extend further | `max_ttl` | `ErrMaxTTLReached` |
| lease gone / not active (404, 409) | `lease_gone` | `*APIError` (`ErrNotFound`) |
| token rejected (401 / 403) | `unauthorized` / `forbidden` | `*APIError` |
| other non-retryable 4xx | `rejected` | `*APIError` |
| TTL ran out while retrying | `expired` | `ErrLeaseExpired` |

Retryable failures — network errors, 5xx (including 503 `sealed`), 408, 429 —
are reported as **non-terminal** events with `Err` set and retried while TTL
headroom remains. Renewal is capped server-side at the role's max TTL, so the
loop stops at that ceiling rather than retrying forever: treat `max_ttl` as
"acquire a new lease".

`RenewEvent` is value-free — it carries the lease ID and timings, never the
password, and the SDK logs nothing on its own.

**Concurrency.** `ID`, `Username` and `Password` are immutable. The expiry is
not: while a renewer is running, read it with `lease.Expiry()` (and
`lease.MaxExpiry()`), not from the `ExpiresAt` field, which the renewer writes.

**Revoke failures.** `RunWithDynamic` never masks your error. If your function
failed *and* the revoke failed, the result is `errors.Join(yourErr, *RevokeError)`
— `errors.Is(err, yourSentinel)` still holds, and `errors.As(err, &revErr)`
finds the revoke failure. If only the revoke failed, that error is returned.

## Full reference

See [`sdk/go/README.md`](../../sdk/go/README.md) for the complete API surface,
a runnable example, and the caching/security model.
