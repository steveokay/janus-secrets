package nethard

import (
	"fmt"
	"net"
	"net/netip"
	"os"
	"strings"
)

// EnvAllow is a comma-separated list of IP addresses and CIDR prefixes that are
// EXEMPT from the JANUS_OUTBOUND_BLOCK_PRIVATE tightening.
//
// It exists because the on/off toggle is the wrong shape for a cluster-native
// deployment. The Helm chart sets JANUS_OUTBOUND_BLOCK_PRIVATE=true, which also
// blocks kubernetes.default.svc — a ClusterIP in a private range — so the k8s
// sync provider and service-account federation stop working, and the documented
// remedy was to turn the whole control off. An allowlist lets both hold at once:
//
//	JANUS_OUTBOUND_BLOCK_PRIVATE=true
//	JANUS_OUTBOUND_ALLOW=10.96.0.1/32     # the API server's ClusterIP
//
// SCOPE — this is the load-bearing rule. The allowlist exempts ONLY the private
// -space tightening. The link-local / cloud-metadata ranges stay blocked
// unconditionally and CANNOT be allowlisted, because they have no legitimate
// outbound use and are the highest-value SSRF target there is; a typo in this
// variable must never be able to hand an attacker 169.254.169.254. Enforcement
// (checkIP) consults the allowlist strictly inside the BlockPrivate branch, and
// parsing rejects any entry that lies entirely within an always-blocked range,
// so such an entry fails loudly at boot instead of sitting there looking useful.
//
// Entries are IPs and CIDRs — never hostnames. The guard runs at connect time on
// the RESOLVED address, which is what defeats DNS rebinding; allowlisting a NAME
// would mean trusting DNS for that name and would re-open the exact attack the
// resolved-IP check exists to close.
const EnvAllow = "JANUS_OUTBOUND_ALLOW"

// alwaysBlockedPrefixes enumerates, as prefixes, the ranges checkIP rejects
// unconditionally. It exists so parsing can refuse an allowlist entry that could
// never take effect.
//
// It MUST stay in step with checkIP's own conditions. It is not the enforcement
// path — checkIP is, and it does not consult this list — so a drift here can
// only make parsing more permissive, never enforcement. TestAllowlistCannotUnblockAlwaysBlocked
// pins that by allowlisting a representative address from each range and
// asserting checkIP still blocks it.
var alwaysBlockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/32"),        // unspecified v4
	netip.MustParsePrefix("169.254.0.0/16"),    // link-local v4, incl. IMDS 169.254.169.254
	netip.MustParsePrefix("224.0.0.0/4"),       // multicast v4
	netip.MustParsePrefix("::/128"),            // unspecified v6
	netip.MustParsePrefix("fe80::/10"),         // link-local v6
	netip.MustParsePrefix("ff00::/8"),          // multicast v6 (incl. interface-local)
	netip.MustParsePrefix("fd00:ec2::254/128"), // IPv6 IMDS
}

// ParseAllow parses the value of EnvAllow into prefixes.
//
// Accepted: a bare IP ("10.96.0.1", treated as a single-address prefix) or a
// CIDR ("10.96.0.0/12"). Blank fields are skipped, so trailing commas and
// whitespace are harmless. A CIDR with host bits set is normalised to its
// network form so the stored prefix reads the way it matches.
//
// It fails on the FIRST bad entry and returns no prefixes at all, rather than
// keeping the entries that did parse. Both directions fail closed, but this one
// is predictable: a typo cannot leave a partially-applied allowlist that permits
// some traffic and quietly drops the rest. The server refuses to boot on this
// error, so the operator fixes it rather than discovering it in an integration.
func ParseAllow(raw string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for field := range strings.SplitSeq(raw, ",") {
		entry := strings.TrimSpace(field)
		if entry == "" {
			continue
		}
		p, err := parseAllowEntry(entry)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// parseAllowEntry parses and validates one allowlist entry.
func parseAllowEntry(entry string) (netip.Prefix, error) {
	var p netip.Prefix
	if strings.Contains(entry, "/") {
		parsed, err := netip.ParsePrefix(entry)
		if err != nil {
			return netip.Prefix{}, fmt.Errorf("%s: %q is not a valid CIDR prefix", EnvAllow, entry)
		}
		// 10.96.0.1/24 -> 10.96.0.0/24. Contains() already ignores host bits;
		// normalising means anything that reports the parsed policy back to an
		// operator shows the range that actually matches.
		p = parsed.Masked()
	} else {
		addr, err := netip.ParseAddr(entry)
		if err != nil {
			return netip.Prefix{}, fmt.Errorf("%s: %q is not a valid IP address or CIDR prefix", EnvAllow, entry)
		}
		if addr.Zone() != "" {
			return netip.Prefix{}, fmt.Errorf(
				"%s: %q carries an interface zone; zoned addresses are link-local, which is blocked unconditionally", EnvAllow, entry)
		}
		p = netip.PrefixFrom(addr, addr.BitLen())
	}
	// Reject the IPv4-in-IPv6 spelling rather than trying to normalise it. For a
	// prefix the bit count is ambiguous (is ::ffff:10.0.0.0/104 a /8?), and
	// silently guessing wrong would widen or narrow what the operator wrote.
	// Enforcement unmaps the dialled address, so the plain IPv4 form matches
	// every dial that the mapped form would have.
	if p.Addr().Is4In6() {
		return netip.Prefix{}, fmt.Errorf(
			"%s: %q uses the IPv4-in-IPv6 form; write it as plain IPv4 (e.g. 10.96.0.1/32)", EnvAllow, entry)
	}
	if blocked, by := entirelyAlwaysBlocked(p); blocked {
		return netip.Prefix{}, fmt.Errorf(
			"%s: %q lies entirely inside %s, which is blocked unconditionally and cannot be allowlisted", EnvAllow, entry, by)
	}
	return p, nil
}

// entirelyAlwaysBlocked reports whether every address in p is unconditionally
// blocked, and which range makes it so. A prefix that merely OVERLAPS a blocked
// range (0.0.0.0/0, say) is not rejected: it still permits useful space, and the
// blocked addresses inside it stay blocked at enforcement time.
func entirelyAlwaysBlocked(p netip.Prefix) (bool, netip.Prefix) {
	for _, b := range alwaysBlockedPrefixes {
		if b.Addr().Is4() != p.Addr().Is4() {
			continue // different address family
		}
		// p is masked, so p.Addr() is its network address: if the blocked range
		// is no more specific than p and contains p's base, it contains all of p.
		if b.Bits() <= p.Bits() && b.Contains(p.Addr()) {
			return true, b
		}
	}
	return false, netip.Prefix{}
}

// allowlisted reports whether the resolved dial address matches any allowlist
// prefix. The address is unmapped first, so a dial that resolves to the
// IPv4-in-IPv6 form still matches a plain IPv4 prefix.
func allowlisted(ip net.IP, prefixes []netip.Prefix) bool {
	if len(prefixes) == 0 {
		return false
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false // unparseable: fail closed
	}
	addr = addr.Unmap()
	for _, p := range prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// allowFromEnv reads EnvAllow, discarding an unparseable value.
//
// Dropping the allowlist fails CLOSED — the destinations stay blocked — and the
// server refuses to start on the same error (buildBootConfig calls ValidateEnv),
// so a running server never silently holds a half-understood allowlist.
func allowFromEnv() []netip.Prefix {
	prefixes, err := ParseAllow(os.Getenv(EnvAllow))
	if err != nil {
		return nil
	}
	return prefixes
}

// ValidateEnv reports any error in the outbound-policy environment. The server
// calls it at boot and `janus doctor` reports it as a preflight check, so a
// malformed allowlist is a startup failure with a named entry rather than an
// integration that mysteriously cannot connect.
func ValidateEnv() error {
	_, err := ParseAllow(os.Getenv(EnvAllow))
	return err
}

// DescribeAllow renders the parsed allowlist for operator-facing output. It
// returns nil when the allowlist is empty, so callers can omit the line.
func DescribeAllow(prefixes []netip.Prefix) []string {
	if len(prefixes) == 0 {
		return nil
	}
	out := make([]string, 0, len(prefixes))
	for _, p := range prefixes {
		out = append(out, p.String())
	}
	return out
}
