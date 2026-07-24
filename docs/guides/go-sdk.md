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

## Full reference

See [`sdk/go/README.md`](../../sdk/go/README.md) for the complete API surface,
a runnable example, and the caching/security model.
