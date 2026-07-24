# Janus Go SDK

A typed Go client for the [Janus](../../README.md) secrets manager. It lets Go
applications read secrets from Janus programmatically — with an in-process TTL
cache and optional dynamic-credential lease management — instead of
hand-rolling HTTP calls.

For subprocess injection (`env`-var style), use `janus run`. This SDK is for
apps that want **programmatic reads** in-process.

## Install

The SDK is its own Go module, so importing it does **not** pull in the Janus
server's dependency tree:

```
go get github.com/steveokay/janus-secrets/sdk/go
```

```go
import janus "github.com/steveokay/janus-secrets/sdk/go"
```

The module has **zero external dependencies** (standard library only).

## Quick start

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	janus "github.com/steveokay/janus-secrets/sdk/go"
)

func main() {
	client, err := janus.NewClient("https://janus.example.com",
		janus.WithToken(os.Getenv("JANUS_TOKEN")), // a janus_svc_... service token
		janus.WithCacheTTL(30*time.Second),
	)
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	const configID = "cfg-00000000-0000-0000-0000-000000000001"

	// Fetch all resolved secrets for a config.
	secrets, err := client.GetSecrets(ctx, configID)
	if err != nil {
		log.Fatal(err)
	}
	db := secrets["DATABASE_URL"] // use it — never log the value

	// Or fetch a single key.
	apiKey, err := client.GetSecret(ctx, configID, "API_KEY")
	if err != nil {
		log.Fatal(err)
	}

	_ = db
	_ = apiKey
	fmt.Println("loaded", len(secrets), "secrets")
}
```

A runnable example lives in [`example_test.go`](example_test.go)
(`go test -run Example`).

## API surface

| Method | Description |
| --- | --- |
| `NewClient(baseURL, opts...)` | Construct a client. Options: `WithToken`, `WithHTTPClient`, `WithCacheTTL`. |
| `GetSecrets(ctx, configID)` | Resolved `map[string]string` for a config (audited reveal on cache miss). |
| `GetSecret(ctx, configID, key)` | Single resolved value; `ErrNotFound` if the key is absent. |
| `Refresh(configID)` | Evict a config's cache (or `Refresh("")` to clear all). |
| `IssueDynamic(ctx, roleID)` | Issue a dynamic DB credential `*Lease`; the password is returned once. |
| `Lease.Renew(ctx)` / `Lease.Revoke(ctx)` | Extend or immediately drop a dynamic lease. |

All methods take a `context.Context` and honour the underlying `http.Client`
timeouts (default 30s; override with `WithHTTPClient`).

### Options

- `WithToken(string)` — a `janus_svc_` service token, sent as
  `Authorization: Bearer <token>`.
- `WithHTTPClient(*http.Client)` — bring your own transport / timeouts / TLS.
- `WithCacheTTL(time.Duration)` — cache TTL for config reads. Default is
  `30s` (`janus.DefaultCacheTTL`); pass `0` to disable caching entirely.

## Caching model

`GetSecrets` (and `GetSecret`, which reads through it) caches each config's
resolved secrets **in memory only** for the configured TTL:

- Within the TTL, repeated reads of the same config are served from the cache
  and do **not** hit the server.
- On a cache miss (first read, or after TTL expiry), the SDK calls the audited
  reveal endpoint (recorded server-side as a `secret.reveal` event) and
  re-populates the cache.
- The cache is **concurrency-safe** and the map returned to you is always a
  **copy** — mutating it never affects the cache.
- `Refresh(configID)` busts the entry so the next read re-fetches;
  `Refresh("")` clears the whole cache.

**Secrets are never written to disk.** The cache lives in process memory and is
lost when the process exits. No SDK method logs secret values, so the client is
safe to use alongside your application's own logger.

## Errors

Non-2xx responses are parsed from the server's `{"error":{code,message}}`
envelope into an `*APIError{Status, Code, Message}`. Common cases wrap a
sentinel, so `errors.Is` works:

```go
secrets, err := client.GetSecrets(ctx, configID)
switch {
case errors.Is(err, janus.ErrUnauthorized): // 401
case errors.Is(err, janus.ErrForbidden):    // 403
case errors.Is(err, janus.ErrNotFound):     // 404
case errors.Is(err, janus.ErrSealed):       // 503 — server sealed, retry after unseal
}
```

`APIError` never carries a secret value — the server's error envelope is
value-free by design.

## Dynamic credentials (leases)

If your token can issue [dynamic Postgres credentials](../../docs/openapi.yaml),
`IssueDynamic` returns a `*Lease` whose one-time password is held in memory
only:

```go
lease, err := client.IssueDynamic(ctx, roleID)
// lease.Username, lease.Password (returned once), lease.ExpiresAt
defer lease.Revoke(context.Background())

// ... later, extend before expiry (capped at the role's max TTL):
_ = lease.Renew(ctx)
```

## License

Apache-2.0, same as the parent project.
