package auth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/steveokay/janus-secrets/internal/nethard"
)

// TestOIDCHTTPClientBlocksMetadata proves the client used for OIDC discovery /
// JWKS refuses a link-local / cloud-metadata destination. go-oidc's NewProvider
// performs the discovery GET through the client carried on the context, and the
// same client is reused for the lazily-built JWKS RemoteKeySet, so hardening the
// client covers both fetches (finding M-4 / I-4).
//
// The dial is refused at connect time by nethard's SafeControl before any socket
// is opened, so this test is hermetic (no network, no hang).
func TestOIDCHTTPClientBlocksMetadata(t *testing.T) {
	hc := newOIDCHTTPClient()
	ctx := oidc.ClientContext(context.Background(), hc)

	// http://169.254.169.254 is the IPv4 cloud instance-metadata endpoint.
	_, err := oidc.NewProvider(ctx, "http://169.254.169.254")
	if err == nil {
		t.Fatal("expected OIDC discovery to a metadata address to be refused, got nil error")
	}
	// The wrapped error should carry nethard's block, not a generic transport or
	// timeout error, confirming the guard fired (not merely an unreachable host).
	if !strings.Contains(err.Error(), nethard.ErrBlockedAddress.Error()) &&
		!errors.Is(err, nethard.ErrBlockedAddress) {
		t.Fatalf("expected error to reflect nethard block, got: %v", err)
	}
}

// TestOIDCHTTPClientAllowsLoopback proves the default policy still permits
// loopback / private targets, which self-hosted OIDC issuers (in-cluster
// Keycloak, LAN IdP, and the test mock IdP on 127.0.0.1) legitimately use. A
// refused dial here would break every real OIDC deployment.
func TestOIDCHTTPClientAllowsLoopback(t *testing.T) {
	hc := newOIDCHTTPClient()
	ctx := oidc.ClientContext(context.Background(), hc)

	// No server is listening, so discovery fails — but it must fail with a
	// connection error, NOT nethard's block, proving loopback is permitted.
	_, err := oidc.NewProvider(ctx, "http://127.0.0.1:1")
	if err == nil {
		t.Fatal("expected discovery to fail against a closed loopback port")
	}
	if errors.Is(err, nethard.ErrBlockedAddress) ||
		strings.Contains(err.Error(), nethard.ErrBlockedAddress.Error()) {
		t.Fatalf("loopback must be permitted by default policy, but it was blocked: %v", err)
	}
}
