package nethard

import (
	"errors"
	"net"
	"net/netip"
	"strings"
	"testing"
)

// mustPrefixes parses an EnvAllow value that the test expects to be valid.
func mustPrefixes(t *testing.T, raw string) []netip.Prefix {
	t.Helper()
	p, err := ParseAllow(raw)
	if err != nil {
		t.Fatalf("ParseAllow(%q): unexpected error: %v", raw, err)
	}
	return p
}

func TestParseAllowValid(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{"empty", "", nil},
		{"only separators", " , ,, ", nil},
		{"single cidr", "10.96.0.0/12", []string{"10.96.0.0/12"}},
		{"bare v4 becomes /32", "10.96.0.1", []string{"10.96.0.1/32"}},
		{"bare v6 becomes /128", "fd00::1", []string{"fd00::1/128"}},
		{"several, whitespace and trailing comma", " 10.96.0.1/32 , 192.168.0.0/16, ", []string{"10.96.0.1/32", "192.168.0.0/16"}},
		// Host bits are normalised away so the stored prefix reads the way it matches.
		{"host bits masked off", "10.96.0.1/24", []string{"10.96.0.0/24"}},
		{"v6 cidr", "fd00:abcd::/64", []string{"fd00:abcd::/64"}},
		// Overlaps a blocked range but also covers useful space: accepted here,
		// and the blocked addresses inside it stay blocked at enforcement.
		{"default route accepted", "0.0.0.0/0", []string{"0.0.0.0/0"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseAllow(tt.raw)
			if err != nil {
				t.Fatalf("ParseAllow(%q): unexpected error: %v", tt.raw, err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("ParseAllow(%q) = %v, want %v", tt.raw, got, tt.want)
			}
			for i, w := range tt.want {
				if got[i].String() != w {
					t.Fatalf("ParseAllow(%q)[%d] = %s, want %s", tt.raw, i, got[i], w)
				}
			}
		})
	}
}

func TestParseAllowRejects(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantSub string
	}{
		{"garbage", "not-an-ip", "not a valid IP address or CIDR"},
		// Hostnames are deliberately unsupported: the guard checks the RESOLVED
		// address, so trusting a name would re-open DNS rebinding.
		{"hostname", "kubernetes.default.svc", "not a valid IP address or CIDR"},
		{"bad cidr", "10.0.0.0/33", "not a valid CIDR prefix"},
		{"bad cidr v6", "fd00::/500", "not a valid CIDR prefix"},
		{"zoned address", "fe80::1%eth0", "interface zone"},
		{"v4-in-v6 address", "::ffff:10.0.0.1", "IPv4-in-IPv6 form"},
		{"v4-in-v6 prefix", "::ffff:10.0.0.0/120", "IPv4-in-IPv6 form"},

		// Entries that could never permit anything are configuration errors.
		{"imds v4", "169.254.169.254", "blocked unconditionally"},
		{"imds v4 cidr", "169.254.169.254/32", "blocked unconditionally"},
		{"link-local v4 range", "169.254.0.0/16", "blocked unconditionally"},
		{"link-local v4 subrange", "169.254.10.0/24", "blocked unconditionally"},
		{"link-local v6 range", "fe80::/10", "blocked unconditionally"},
		{"link-local v6 subrange", "fe80::/64", "blocked unconditionally"},
		{"multicast v4", "224.0.0.1", "blocked unconditionally"},
		{"multicast v6", "ff02::1", "blocked unconditionally"},
		{"unspecified v4", "0.0.0.0", "blocked unconditionally"},
		{"unspecified v6", "::", "blocked unconditionally"},
		{"imds v6", "fd00:ec2::254", "blocked unconditionally"},

		// One bad entry poisons the whole list rather than leaving a partial one.
		{"one bad among good", "10.96.0.1/32,169.254.169.254,192.168.0.0/16", "blocked unconditionally"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseAllow(tt.raw)
			if err == nil {
				t.Fatalf("ParseAllow(%q): expected an error, got %v", tt.raw, got)
			}
			if got != nil {
				t.Fatalf("ParseAllow(%q): expected no prefixes on error, got %v", tt.raw, got)
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("ParseAllow(%q): error %q does not mention %q", tt.raw, err, tt.wantSub)
			}
			// The message must name the variable so the operator knows where to look.
			if !strings.Contains(err.Error(), EnvAllow) {
				t.Fatalf("ParseAllow(%q): error %q does not name %s", tt.raw, err, EnvAllow)
			}
		})
	}
}

// TestAllowlistExemptsPrivateUnderBlockPrivate is the feature itself: with the
// tightening on, an allowlisted private destination connects and its neighbours
// do not.
func TestAllowlistExemptsPrivateUnderBlockPrivate(t *testing.T) {
	// The motivating case: the in-cluster API server's ClusterIP.
	policy := Policy{BlockPrivate: true, Allow: mustPrefixes(t, "10.96.0.1/32")}

	tests := []struct {
		name    string
		address string
		blocked bool
	}{
		{"allowlisted cluster ip", "10.96.0.1:443", false},
		{"neighbour in same /24 still blocked", "10.96.0.2:443", true},
		{"other private space still blocked", "192.168.1.10:443", true},
		{"loopback still blocked", "127.0.0.1:80", true},
		// Allowlisting private space must not disturb anything else.
		{"public still allowed", "93.184.216.34:443", false},
		{"metadata still blocked", "169.254.169.254:80", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := SafeControl(Static(policy))("tcp", tt.address, nil)
			if tt.blocked && !errors.Is(err, ErrBlockedAddress) {
				t.Fatalf("%s: expected ErrBlockedAddress, got %v", tt.address, err)
			}
			if !tt.blocked && err != nil {
				t.Fatalf("%s: expected allow, got %v", tt.address, err)
			}
		})
	}
}

// TestAllowlistCannotUnblockAlwaysBlocked is the security invariant. Parsing
// refuses these entries, so this drives checkIP DIRECTLY with a policy that has
// them allowlisted anyway — proving enforcement does not depend on parsing
// having caught them. If alwaysBlockedPrefixes ever drifts from checkIP, this is
// what fails.
func TestAllowlistCannotUnblockAlwaysBlocked(t *testing.T) {
	addrs := []string{
		"169.254.169.254", // IPv4 IMDS
		"169.254.0.1",     // link-local v4
		"fe80::1",         // link-local v6
		"fd00:ec2::254",   // IPv6 IMDS
		"0.0.0.0",         // unspecified v4
		"::",              // unspecified v6
		"224.0.0.1",       // multicast v4
		"ff02::1",         // multicast v6
	}
	for _, a := range addrs {
		t.Run(a, func(t *testing.T) {
			ip := net.ParseIP(a)
			if ip == nil {
				t.Fatalf("test setup: %q is not an IP", a)
			}
			// Allowlist the exact address, plus a default route over both
			// families, and assert it is STILL blocked under both policies.
			addr, _ := netip.AddrFromSlice(ip)
			addr = addr.Unmap()
			wide := []netip.Prefix{
				netip.PrefixFrom(addr, addr.BitLen()),
				netip.MustParsePrefix("0.0.0.0/0"),
				netip.MustParsePrefix("::/0"),
			}
			for _, p := range []Policy{
				{BlockPrivate: false, Allow: wide},
				{BlockPrivate: true, Allow: wide},
			} {
				if err := checkIP(ip, p); !errors.Is(err, ErrBlockedAddress) {
					t.Fatalf("%s allowlisted (BlockPrivate=%v): expected ErrBlockedAddress, got %v",
						a, p.BlockPrivate, err)
				}
			}
		})
	}
}

// TestAllowlistInertWithoutBlockPrivate pins that the allowlist changes nothing
// when the tightening is off — it is a companion to that toggle, not a policy of
// its own. A regression here would mean the allowlist had started *restricting*
// traffic that the default policy permits.
func TestAllowlistInertWithoutBlockPrivate(t *testing.T) {
	withList := Policy{BlockPrivate: false, Allow: mustPrefixes(t, "10.96.0.1/32")}
	without := Policy{BlockPrivate: false}

	for _, address := range []string{
		"10.96.0.1:443", "10.0.0.5:80", "192.168.1.10:443", "127.0.0.1:80",
		"[fd00::5]:80", "93.184.216.34:80", "169.254.169.254:80", "[fe80::1]:80",
	} {
		a, b := SafeControl(Static(withList))("tcp", address, nil), SafeControl(Static(without))("tcp", address, nil)
		if (a == nil) != (b == nil) {
			t.Fatalf("%s: allowlist changed the outcome with BlockPrivate=false (with=%v without=%v)",
				address, a, b)
		}
	}
}

// TestAllowlistMatchesMappedDial covers a dial that resolves to the
// IPv4-in-IPv6 form. The entry is written as plain IPv4 (the only form parsing
// accepts), so enforcement must unmap before matching or the exemption silently
// would not apply.
func TestAllowlistMatchesMappedDial(t *testing.T) {
	policy := Policy{BlockPrivate: true, Allow: mustPrefixes(t, "10.96.0.1/32")}
	mapped := net.ParseIP("::ffff:10.96.0.1")
	if mapped == nil {
		t.Fatal("test setup: could not parse the mapped address")
	}
	if err := checkIP(mapped, policy); err != nil {
		t.Fatalf("mapped form of an allowlisted address: expected allow, got %v", err)
	}
	// The same address NOT allowlisted stays blocked, so the test above is not
	// passing merely because mapped addresses skip the check.
	if err := checkIP(mapped, Policy{BlockPrivate: true}); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("mapped form without an allowlist: expected ErrBlockedAddress, got %v", err)
	}
}

// TestAllowlistCIDRMatching checks range membership at both edges, since an
// off-by-one in the prefix maths would be invisible in the middle of a range.
func TestAllowlistCIDRMatching(t *testing.T) {
	policy := Policy{BlockPrivate: true, Allow: mustPrefixes(t, "10.96.0.0/24,fd00:abcd::/64")}
	tests := []struct {
		address string
		blocked bool
	}{
		{"10.96.0.0:443", false},   // network address
		{"10.96.0.255:443", false}, // broadcast address
		{"10.96.0.128:443", false}, // middle
		{"10.96.1.0:443", true},    // one past the end
		{"10.95.255.255:443", true},
		{"[fd00:abcd::1]:443", false},
		{"[fd00:abcd::ffff:ffff:ffff:ffff]:443", false},
		{"[fd00:abce::1]:443", true},
	}
	for _, tt := range tests {
		t.Run(tt.address, func(t *testing.T) {
			err := SafeControl(Static(policy))("tcp", tt.address, nil)
			if tt.blocked && !errors.Is(err, ErrBlockedAddress) {
				t.Fatalf("%s: expected ErrBlockedAddress, got %v", tt.address, err)
			}
			if !tt.blocked && err != nil {
				t.Fatalf("%s: expected allow, got %v", tt.address, err)
			}
		})
	}
}

// TestAllowlistCrossFamily pins that an IPv4 prefix never matches an IPv6 dial
// or vice versa.
func TestAllowlistCrossFamily(t *testing.T) {
	v4only := Policy{BlockPrivate: true, Allow: mustPrefixes(t, "0.0.0.0/0")}
	if err := checkIP(net.ParseIP("fd00::5"), v4only); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("v6 ULA against an IPv4-only allowlist: expected ErrBlockedAddress, got %v", err)
	}
	v6only := Policy{BlockPrivate: true, Allow: mustPrefixes(t, "::/0")}
	if err := checkIP(net.ParseIP("10.0.0.5"), v6only); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("v4 private against an IPv6-only allowlist: expected ErrBlockedAddress, got %v", err)
	}
}

func TestPolicyFromEnvAllow(t *testing.T) {
	t.Setenv(EnvBlockPrivate, "true")

	t.Setenv(EnvAllow, "10.96.0.1/32, 192.168.0.0/16")
	p := PolicyFromEnv()
	if len(p.Allow) != 2 {
		t.Fatalf("expected 2 allowlist prefixes, got %v", p.Allow)
	}
	if err := checkIP(net.ParseIP("10.96.0.1"), p); err != nil {
		t.Fatalf("allowlisted address from env: expected allow, got %v", err)
	}

	// Unset: no allowlist, and the tightening behaves as it always did.
	t.Setenv(EnvAllow, "")
	if p := PolicyFromEnv(); len(p.Allow) != 0 {
		t.Fatalf("expected no prefixes when unset, got %v", p.Allow)
	}

	// Malformed: dropped entirely (fail closed), never partially applied.
	t.Setenv(EnvAllow, "10.96.0.1/32,nonsense")
	p = PolicyFromEnv()
	if len(p.Allow) != 0 {
		t.Fatalf("expected a malformed allowlist to be dropped, got %v", p.Allow)
	}
	if err := checkIP(net.ParseIP("10.96.0.1"), p); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("malformed allowlist must not partially apply, got %v", err)
	}
}

// TestSourceIsLive is the reason Source exists. Every engine builds ONE
// http.Client at construction and keeps it for the process lifetime, so a
// policy captured by value there could never change. This drives the dialer
// built from a Source and asserts an edit lands on the very next dial — both
// tightening and loosening — without rebuilding anything.
func TestSourceIsLive(t *testing.T) {
	src := NewSource(Policy{BlockPrivate: true})
	ctrl := SafeControl(src) // built once, exactly as an engine's client is

	if err := ctrl("tcp", "10.96.0.1:443", nil); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("before the edit: expected ErrBlockedAddress, got %v", err)
	}

	// Loosen: allowlist the address.
	src.Set(Policy{BlockPrivate: true, Allow: mustPrefixes(t, "10.96.0.1/32")})
	if err := ctrl("tcp", "10.96.0.1:443", nil); err != nil {
		t.Fatalf("after allowlisting: expected allow on the next dial, got %v", err)
	}

	// Tighten again: the exemption is withdrawn just as immediately.
	src.Set(Policy{BlockPrivate: true})
	if err := ctrl("tcp", "10.96.0.1:443", nil); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("after withdrawing: expected ErrBlockedAddress, got %v", err)
	}

	// No edit can ever reach the always-blocked ranges, live or otherwise.
	src.Set(Policy{Allow: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}})
	if err := ctrl("tcp", "169.254.169.254:80", nil); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("metadata after a live edit: expected ErrBlockedAddress, got %v", err)
	}
}

// TestSourceNilAndZeroFailClosed pins that a Source which was never populated
// does not silently disable the guard.
func TestSourceNilAndZeroFailClosed(t *testing.T) {
	var nilSrc *Source
	if err := SafeControl(nilSrc)("tcp", "169.254.169.254:80", nil); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("nil Source: expected ErrBlockedAddress, got %v", err)
	}
	if p := (&Source{}).Policy(); p.BlockPrivate || len(p.Allow) != 0 || p.AllowProxy {
		t.Fatalf("zero Source: expected the empty policy, got %+v", p)
	}
}

func TestValidateEnv(t *testing.T) {
	t.Setenv(EnvAllow, "")
	if err := ValidateEnv(); err != nil {
		t.Fatalf("unset allowlist: expected no error, got %v", err)
	}
	t.Setenv(EnvAllow, "10.96.0.1/32")
	if err := ValidateEnv(); err != nil {
		t.Fatalf("valid allowlist: expected no error, got %v", err)
	}
	t.Setenv(EnvAllow, "169.254.169.254/32")
	if err := ValidateEnv(); err == nil {
		t.Fatal("metadata allowlist entry: expected an error")
	}
}

func TestDescribeAllow(t *testing.T) {
	if got := DescribeAllow(nil); got != nil {
		t.Fatalf("empty allowlist: expected nil, got %v", got)
	}
	got := DescribeAllow(mustPrefixes(t, "10.96.0.1,10.0.0.0/8"))
	want := []string{"10.96.0.1/32", "10.0.0.0/8"}
	if len(got) != len(want) {
		t.Fatalf("DescribeAllow = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("DescribeAllow[%d] = %s, want %s", i, got[i], want[i])
		}
	}
}
