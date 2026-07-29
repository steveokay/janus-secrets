package nethard

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
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
			ctrl := SafeControl(Static(tt.policy))
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
	ctrl := SafeControl(Static(Policy{}))
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

func TestPolicyFromEnvAllowProxy(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{"", false},
		{"no", false},
		{"false", false},
		{"true", true},
		{"1", true},
		{"YES", true},
		{" on ", true},
	}
	for _, tt := range tests {
		t.Run("value="+tt.value, func(t *testing.T) {
			t.Setenv(EnvAllowProxy, tt.value)
			if got := PolicyFromEnv().AllowProxy; got != tt.want {
				t.Fatalf("%s=%q: AllowProxy=%v, want %v", EnvAllowProxy, tt.value, got, tt.want)
			}
		})
	}
}

func TestProxyEnvVarsSet(t *testing.T) {
	// Clear every var this helper inspects (both spellings) so the ambient
	// environment cannot influence the result.
	for _, name := range proxyEnvVars {
		t.Setenv(name, "")
		t.Setenv(strings.ToLower(name), "")
	}
	if got := ProxyEnvVarsSet(); len(got) != 0 {
		t.Fatalf("expected no proxy vars, got %v", got)
	}
	// Whitespace-only does not count as configured.
	t.Setenv("http_proxy", "   ")
	if got := ProxyEnvVarsSet(); len(got) != 0 {
		t.Fatalf("whitespace-only proxy: expected none, got %v", got)
	}
	// Lower-case spelling counts, and is reported under its canonical name once.
	t.Setenv("https_proxy", "http://proxy.internal:3128")
	got := ProxyEnvVarsSet()
	if len(got) != 1 || got[0] != "HTTPS_PROXY" {
		t.Fatalf("expected [HTTPS_PROXY], got %v", got)
	}
}

// TestSafeHTTPClientNoProxyByDefault pins the fix for the proxy SSRF gap: a
// proxy would terminate the TCP connection, so SafeControl would validate the
// PROXY's IP instead of the operator-supplied destination.
func TestSafeHTTPClientNoProxyByDefault(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://proxy.internal:3128")
	t.Setenv("HTTP_PROXY", "http://proxy.internal:3128")

	c := SafeHTTPClient(5*time.Second, Static(PolicyFromEnv()))
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatal("expected *http.Transport")
	}
	if tr.Proxy != nil {
		t.Fatal("default policy must not set a Proxy function (a proxy blinds the connect-time IP guard)")
	}
}

func TestSafeHTTPClientProxyOptIn(t *testing.T) {
	t.Setenv(EnvAllowProxy, "true")
	t.Setenv("HTTPS_PROXY", "http://proxy.internal:3128")

	c := SafeHTTPClient(5*time.Second, Static(PolicyFromEnv()))
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatal("expected *http.Transport")
	}
	if tr.Proxy == nil {
		t.Fatalf("expected Proxy restored when %s is set", EnvAllowProxy)
	}
	// A normal host is allowed through to http.ProxyFromEnvironment.
	req := &http.Request{URL: &url.URL{Scheme: "https", Host: "hooks.example.com"}}
	proxyURL, err := tr.Proxy(req)
	if err != nil {
		t.Fatalf("normal host: expected allow, got %v", err)
	}
	// net/http caches the proxy environment process-wide on first use, so the
	// returned URL is only asserted when this test won that race.
	if proxyURL != nil && proxyURL.Host != "proxy.internal:3128" {
		t.Fatalf("expected the environment proxy, got %v", proxyURL)
	}
	// A blocked literal IP is rejected before the proxy is consulted.
	req = &http.Request{URL: &url.URL{Scheme: "http", Host: "169.254.169.254"}}
	if _, err := tr.Proxy(req); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("metadata IP: expected ErrBlockedAddress, got %v", err)
	}
}

func TestCheckURLHost(t *testing.T) {
	allow := Policy{}
	strict := Policy{BlockPrivate: true}

	tests := []struct {
		name    string
		policy  Policy
		rawURL  string
		blocked bool
	}{
		{"imds literal v4", allow, "http://169.254.169.254/latest/meta-data/", true},
		{"imds literal v4 with port", allow, "http://169.254.169.254:80/", true},
		{"link-local literal v6", allow, "http://[fe80::1]/", true},
		{"imds literal v6", allow, "http://[fd00:ec2::254]/", true},
		{"unspecified literal", allow, "http://0.0.0.0/", true},
		{"public literal", allow, "https://93.184.216.34/hook", false},
		{"rfc1918 literal allowed by default", allow, "http://10.0.0.5:9000/hook", false},
		{"rfc1918 literal blocked strict", strict, "http://10.0.0.5:9000/hook", true},
		{"normal hostname passes (unverifiable via proxy)", allow, "https://hooks.example.com/x", false},
		// Documents the honest limitation: a hostname that resolves to the
		// metadata IP is NOT caught at URL time.
		{"rebinding hostname not caught", allow, "https://metadata.attacker.example/", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := url.Parse(tt.rawURL)
			if err != nil {
				t.Fatalf("parse %q: %v", tt.rawURL, err)
			}
			err = checkURLHost(u, tt.policy)
			if tt.blocked && !errors.Is(err, ErrBlockedAddress) {
				t.Fatalf("%q: expected ErrBlockedAddress, got %v", tt.rawURL, err)
			}
			if !tt.blocked && err != nil {
				t.Fatalf("%q: expected allow, got %v", tt.rawURL, err)
			}
		})
	}

	if err := checkURLHost(nil, allow); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("nil URL: expected ErrBlockedAddress, got %v", err)
	}
}

func TestSafeHTTPClientWired(t *testing.T) {
	c := SafeHTTPClient(5, Static(Policy{}))
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
