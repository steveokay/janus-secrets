// Package nethard provides SSRF hardening for Janus's operator-configured
// outbound clients (notification webhooks, rotation webhooks/DB dials, secret
// sync providers). It is defense-in-depth: every input that drives these dials
// is admin-gated, but a compromised admin (or a DNS-rebinding host) could still
// aim an outbound request at the cloud instance-metadata endpoint.
//
// Janus is SELF-HOSTED, so private/internal targets are legitimate and common
// (in-cluster Kubernetes API, internal webhook receivers, LAN SMTP relays,
// internal Postgres/MySQL/Redis). Therefore the DEFAULT policy blocks only the
// universal, no-legitimate-use SSRF range — the link-local / cloud-metadata
// block (169.254.0.0/16, fe80::/10, fd00:ec2::254) plus unspecified/multicast —
// and ALLOWS loopback and RFC1918/ULA. Operators who run without any legitimate
// private target can set JANUS_OUTBOUND_BLOCK_PRIVATE=true to also reject
// loopback + RFC1918 + ULA.
//
// The guard runs at CONNECT time via net.Dialer.Control, inspecting the RESOLVED
// IP the kernel is about to dial — this is what defeats DNS rebinding, since the
// name is re-resolved and re-checked on every dial (including redirect follows).
package nethard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"
)

// EnvBlockPrivate, when set truthy, tightens the default policy to also reject
// loopback + RFC1918 + ULA (private) address space.
const EnvBlockPrivate = "JANUS_OUTBOUND_BLOCK_PRIVATE"

// ErrBlockedAddress is returned by the guard when a resolved dial target is in a
// blocked range. It is intentionally value-free (carries no host/credential).
var ErrBlockedAddress = errors.New("nethard: destination address is not permitted")

// maxRedirects bounds redirect chains on SafeHTTPClient. The Control function
// re-checks each redirected dial's resolved IP; CheckRedirect additionally caps
// the count and rejects non-http(s) schemes.
const maxRedirects = 5

// Policy is the resolved SSRF policy, read from the environment once at
// construction so the connect-time hot path never touches os.Getenv.
type Policy struct {
	// BlockPrivate, when true, also rejects loopback + RFC1918 + ULA (private)
	// space in addition to the always-blocked link-local/metadata ranges.
	BlockPrivate bool
}

// PolicyFromEnv builds a Policy from the process environment. The link-local /
// metadata block is unconditional; only the private-space tightening is env-gated.
func PolicyFromEnv() Policy {
	return Policy{BlockPrivate: envTruthy(os.Getenv(EnvBlockPrivate))}
}

func envTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// fd00ec2IMDS is the IPv6 instance-metadata address (AWS IMDS over IPv6). It is
// in the ULA fc00::/7 range, so it is only reachable when private space is
// otherwise allowed — we block it unconditionally regardless of BlockPrivate.
var fd00ec2IMDS = net.ParseIP("fd00:ec2::254")

// checkIP reports whether ip is permitted under the policy. The link-local /
// metadata / unspecified / multicast ranges are ALWAYS rejected; loopback and
// private (RFC1918/ULA) space is rejected only when policy.BlockPrivate is set.
func checkIP(ip net.IP, policy Policy) error {
	if ip == nil {
		return ErrBlockedAddress
	}
	// Always-blocked, no-legitimate-use ranges.
	//   - unspecified (0.0.0.0, ::)
	//   - multicast (224.0.0.0/4, ff00::/8)
	//   - link-local unicast: IPv4 169.254.0.0/16 (covers IMDS 169.254.169.254),
	//     IPv6 fe80::/10
	//   - link-local multicast (covered by IsMulticast / IsLinkLocalMulticast)
	//   - the IPv6 IMDS address fd00:ec2::254 (ULA, but a pure metadata target)
	if ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() {
		return ErrBlockedAddress
	}
	if fd00ec2IMDS.Equal(ip) {
		return ErrBlockedAddress
	}
	// Private space is legitimate for self-hosted deployments unless the
	// operator opts into the stricter policy.
	if policy.BlockPrivate {
		if ip.IsLoopback() || ip.IsPrivate() {
			return ErrBlockedAddress
		}
	}
	return nil
}

// SafeControl is a net.Dialer.Control function. It parses the RESOLVED address
// (host:port, where host is already an IP literal at Control time) and rejects
// it if the policy forbids the IP. Because Control runs after DNS resolution on
// every dial — including redirect follows and reconnects — this defeats
// DNS-rebinding attacks that a URL-time check would miss.
func SafeControl(policy Policy) func(network, address string, c syscall.RawConn) error {
	return func(network, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			// Fall back to treating the whole address as a host (no port).
			host = address
		}
		ip := net.ParseIP(host)
		if ip == nil {
			// Control is called with the resolved IP literal; a non-IP here is
			// unexpected, so fail closed.
			return ErrBlockedAddress
		}
		return checkIP(ip, policy)
	}
}

// SafeDialContext returns a DialContext function whose dialer applies SafeControl
// for non-HTTP dials (SMTP, Postgres/MySQL/Redis). The timeout bounds a single
// connect attempt so a black-holed internal IP cannot hang a scheduler goroutine.
func SafeDialContext(policy Policy, timeout time.Duration) func(ctx context.Context, network, address string) (net.Conn, error) {
	d := &net.Dialer{Timeout: timeout, Control: SafeControl(policy)}
	return d.DialContext
}

// SafeHTTPClient returns an *http.Client whose transport dials through a
// SafeControl-guarded dialer and whose CheckRedirect caps the redirect count and
// rejects non-http(s) redirect targets. The per-dial Control re-check means each
// redirect hop's resolved IP is validated too, so a 30x to an internal/metadata
// host is refused at connect time.
func SafeHTTPClient(timeout time.Duration, policy Policy) *http.Client {
	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           SafeDialContext(policy, timeout),
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &http.Client{
		Timeout:       timeout,
		Transport:     tr,
		CheckRedirect: checkRedirect,
	}
}

// checkRedirect bounds the redirect chain and rejects any redirect to a
// non-http(s) scheme (e.g. file://, gopher://). The resolved-IP guard is applied
// by the transport's Control fn on the redirected dial, so this function's job
// is the count cap and scheme allowlist.
func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("nethard: stopped after %d redirects", maxRedirects)
	}
	switch req.URL.Scheme {
	case "http", "https":
		return nil
	default:
		return fmt.Errorf("nethard: refusing redirect to scheme %q", req.URL.Scheme)
	}
}
