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
