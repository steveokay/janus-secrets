package nethard

import (
	"errors"
	"net/http"
	"net/url"
	"testing"
)

func TestSafeControl(t *testing.T) {
	allow := Policy{BlockPrivate: false}
	strict := Policy{BlockPrivate: true}

	tests := []struct {
		name    string
		policy  Policy
		address string
		blocked bool
	}{
		// Always blocked: link-local / cloud metadata.
		{"imds v4", allow, "169.254.169.254:80", true},
		{"link-local v4 other", allow, "169.254.0.1:443", true},
		{"link-local v6", allow, "[fe80::1]:80", true},
		{"imds v6 fd00:ec2", allow, "[fd00:ec2::254]:80", true},
		{"unspecified v4", allow, "0.0.0.0:80", true},
		{"unspecified v6", allow, "[::]:80", true},
		{"multicast v4", allow, "224.0.0.1:80", true},
		{"multicast v6", allow, "[ff02::1]:80", true},

		// Allowed by default (self-hosted needs these).
		{"rfc1918 10/8 allowed", allow, "10.0.0.5:80", false},
		{"rfc1918 192.168 allowed", allow, "192.168.1.10:443", false},
		{"rfc1918 172.16 allowed", allow, "172.16.0.9:80", false},
		{"loopback v4 allowed", allow, "127.0.0.1:80", false},
		{"loopback v6 allowed", allow, "[::1]:80", false},
		{"ula allowed", allow, "[fd00::5]:80", false},
		{"public allowed", allow, "93.184.216.34:80", false},

		// With BLOCK_PRIVATE=true, private + loopback are also rejected.
		{"rfc1918 blocked strict", strict, "10.0.0.5:80", true},
		{"loopback blocked strict", strict, "127.0.0.1:80", true},
		{"ula blocked strict", strict, "[fd00::5]:80", true},
		// ... but a public address still passes under strict.
		{"public allowed strict", strict, "93.184.216.34:80", false},
		// ... and metadata is still blocked under strict.
		{"imds blocked strict", strict, "169.254.169.254:80", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := SafeControl(tt.policy)
			err := ctrl("tcp", tt.address, nil)
			if tt.blocked && err == nil {
				t.Fatalf("address %q: expected block, got allow", tt.address)
			}
			if !tt.blocked && err != nil {
				t.Fatalf("address %q: expected allow, got %v", tt.address, err)
			}
			if tt.blocked && !errors.Is(err, ErrBlockedAddress) {
				t.Fatalf("address %q: expected ErrBlockedAddress, got %v", tt.address, err)
			}
		})
	}
}

func TestSafeControlNonIP(t *testing.T) {
	// Control is expected to receive a resolved IP literal; a hostname must fail
	// closed rather than pass unchecked.
	ctrl := SafeControl(Policy{})
	if err := ctrl("tcp", "example.com:80", nil); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("non-IP host: expected ErrBlockedAddress, got %v", err)
	}
}

func TestCheckRedirectScheme(t *testing.T) {
	tests := []struct {
		scheme  string
		blocked bool
	}{
		{"http", false},
		{"https", false},
		{"file", true},
		{"gopher", true},
		{"ftp", true},
	}
	for _, tt := range tests {
		t.Run(tt.scheme, func(t *testing.T) {
			req := &http.Request{URL: &url.URL{Scheme: tt.scheme, Host: "example.com"}}
			err := checkRedirect(req, nil)
			if tt.blocked && err == nil {
				t.Fatalf("scheme %q: expected reject", tt.scheme)
			}
			if !tt.blocked && err != nil {
				t.Fatalf("scheme %q: expected allow, got %v", tt.scheme, err)
			}
		})
	}
}

func TestCheckRedirectCount(t *testing.T) {
	req := &http.Request{URL: &url.URL{Scheme: "https", Host: "example.com"}}
	// Under the cap: allowed.
	via := make([]*http.Request, maxRedirects-1)
	if err := checkRedirect(req, via); err != nil {
		t.Fatalf("under cap (%d): expected allow, got %v", len(via), err)
	}
	// At/over the cap: rejected regardless of scheme.
	via = make([]*http.Request, maxRedirects)
	if err := checkRedirect(req, via); err == nil {
		t.Fatalf("at cap (%d): expected reject", len(via))
	}
}

func TestPolicyFromEnv(t *testing.T) {
	t.Setenv(EnvBlockPrivate, "true")
	if !PolicyFromEnv().BlockPrivate {
		t.Fatal("expected BlockPrivate=true when env is truthy")
	}
	t.Setenv(EnvBlockPrivate, "")
	if PolicyFromEnv().BlockPrivate {
		t.Fatal("expected BlockPrivate=false when env is empty")
	}
	t.Setenv(EnvBlockPrivate, "no")
	if PolicyFromEnv().BlockPrivate {
		t.Fatal("expected BlockPrivate=false when env is 'no'")
	}
}

func TestSafeHTTPClientWired(t *testing.T) {
	c := SafeHTTPClient(5, Policy{})
	if c.Transport == nil {
		t.Fatal("expected transport")
	}
	if c.CheckRedirect == nil {
		t.Fatal("expected CheckRedirect")
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok || tr.DialContext == nil {
		t.Fatal("expected *http.Transport with DialContext guard")
	}
}
