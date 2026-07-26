package janus_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	janus "github.com/steveokay/janus-secrets/sdk/go"
)

// Example demonstrates creating a client, reading a config's secrets, and the
// in-process cache: the second GetSecrets within the TTL is served from memory
// and does not hit the server.
func Example() {
	// A stand-in Janus server for the example. In real use, point NewClient at
	// your Janus deployment's base URL.
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		// obviously-fake fixture values, not real secrets
		fmt.Fprint(w, `{"version":1,"secrets":{"DATABASE_URL":"postgres://fake"}}`)
	}))
	defer srv.Close()

	client, err := janus.NewClient(srv.URL,
		janus.WithToken("janus_svc_example-token-000"),
		janus.WithCacheTTL(30*time.Second),
	)
	if err != nil {
		panic(err)
	}

	const configID = "cfg-00000000-0000-0000-0000-000000000001"
	ctx := context.Background()

	// First read hits the server (audited server-side as secret.reveal).
	secrets, err := client.GetSecrets(ctx, configID)
	if err != nil {
		if errors.Is(err, janus.ErrSealed) {
			panic("server is sealed")
		}
		panic(err)
	}
	// Never log secret values in real code.
	fmt.Println("has DATABASE_URL:", secrets["DATABASE_URL"] != "")

	// Second read within the TTL is served from the in-process cache.
	_, _ = client.GetSecrets(ctx, configID)
	fmt.Println("server hits:", hits)

	// Output:
	// has DATABASE_URL: true
	// server hits: 1
}

// ExampleClient_RunWithDynamic shows the recommended way to use dynamic
// credentials: the lease is issued, kept renewed in the background for as long
// as the function runs, and revoked on every exit path — success, error, or
// panic.
func ExampleClient_RunWithDynamic() {
	// A stand-in Janus server for the example.
	const leaseID = "lease-0000-0000-0000-000000000009"
	revoked := false
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/dynamic/roles/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// obviously-fake fixture values, not real credentials
		fmt.Fprintf(w, `{"lease_id":%q,"username":"u_example","password":"fake","expires_at":%q}`,
			leaseID, time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	})
	mux.HandleFunc("/v1/dynamic/leases/"+leaseID+"/revoke", func(w http.ResponseWriter, r *http.Request) {
		revoked = true
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"revoked":true}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client, err := janus.NewClient(srv.URL, janus.WithToken("janus_svc_example-token-000"))
	if err != nil {
		panic(err)
	}

	err = client.RunWithDynamic(context.Background(), "role-0000-0000-0000-000000000002",
		func(ctx context.Context, lease *janus.Lease) error {
			// Open a DB connection with lease.Username / lease.Password here.
			// Never log the password.
			fmt.Println("working as:", lease.Username)
			return nil
		})
	if err != nil {
		panic(err)
	}
	fmt.Println("revoked:", revoked)

	// Output:
	// working as: u_example
	// revoked: true
}

// ExampleLease_StartAutoRenew shows opt-in background renewal for a lease whose
// lifetime the caller manages directly. Nothing renews unless a renewer is
// started, and the renewer must be stopped. (Compiled, not run.)
func ExampleLease_StartAutoRenew() {
	client, err := janus.NewClient("https://janus.example.com",
		janus.WithToken("janus_svc_example-token-000"))
	if err != nil {
		panic(err)
	}

	ctx := context.Background()
	lease, err := client.IssueDynamic(ctx, "role-0000-0000-0000-000000000002")
	if err != nil {
		panic(err)
	}
	defer func() { _ = lease.Revoke(ctx) }()

	renewer, err := lease.StartAutoRenew(ctx, &janus.AutoRenewOptions{
		OnEvent: func(e janus.RenewEvent) {
			// Events are value-free: never a password.
			if e.Terminal && e.Err != nil {
				// Credentials are about to stop working: wind down.
				fmt.Println("auto-renew ended:", e.Reason)
			}
		},
	})
	if err != nil {
		panic(err)
	}
	defer renewer.Stop()

	// ... use lease.Username / lease.Password, and lease.Expiry() if you need
	// the current expiry while the renewer is running ...
}
