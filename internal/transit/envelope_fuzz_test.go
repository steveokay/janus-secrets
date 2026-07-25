package transit

import (
	"bytes"
	"testing"
)

// FuzzParseEnvelope feeds arbitrary strings to the ciphertext-envelope parser.
// parseEnvelope runs on attacker-supplied request bodies (`POST
// /v1/transit/decrypt/{name}` takes whatever ciphertext the caller sends), so it
// must never panic and must never report success for input it cannot fully
// account for.
func FuzzParseEnvelope(f *testing.F) {
	for _, seed := range []string{
		"janus:v1:aGVsbG8=",
		"janus:v42:", "janus:v0:aGk=", "janus:v-1:aGk=", "janus:vx:aGk=",
		"janus:v1:!!!notbase64", "janus:v1", "janus:", "janus", "",
		":::", "janus:v1:aGk=:extra", "JANUS:v1:aGk=",
		"janus:v99999999999999999999:aGk=", "janus:v 1:aGk=",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, s string) {
		version, body, err := parseEnvelope(s)
		if err != nil {
			// Rejected. It must not also hand back usable output.
			if version != 0 || body != nil {
				t.Fatalf("parseEnvelope(%q) returned err AND output (v=%d, body=%v)", s, version, body)
			}
			return
		}
		// Accepted: the version must be usable as a key version.
		if version < 1 {
			t.Fatalf("parseEnvelope(%q) accepted version %d (must be >= 1)", s, version)
		}
		// Re-encoding the parsed pieces must reproduce a parseable envelope with
		// identical content — i.e. the parse captured everything.
		round := formatEnvelope(version, body)
		v2, b2, err2 := parseEnvelope(round)
		if err2 != nil {
			t.Fatalf("re-parse of re-formatted envelope failed: %v (orig %q)", err2, s)
		}
		if v2 != version || !bytes.Equal(b2, body) {
			t.Fatalf("envelope not stable: (%d,%v) → %q → (%d,%v)", version, body, round, v2, b2)
		}
	})
}

// FuzzEnvelopeRoundTrip asserts the encode→decode contract from the other
// direction: any body the engine produces must survive the wire format byte for
// byte. A lossy envelope would corrupt ciphertext and surface as an
// undecryptable secret rather than an obvious error.
func FuzzEnvelopeRoundTrip(f *testing.F) {
	f.Add(1, []byte("hello"))
	f.Add(2, []byte{})
	f.Add(7, []byte{0x00, 0xff, 0x0d, 0x0a})
	f.Add(1, bytes.Repeat([]byte{0xAB}, 512))

	f.Fuzz(func(t *testing.T, version int, body []byte) {
		if version < 1 {
			t.Skip() // formatEnvelope's contract starts at v1
		}
		got, gotBody, err := parseEnvelope(formatEnvelope(version, body))
		if err != nil {
			t.Fatalf("round-trip failed for v%d/%d bytes: %v", version, len(body), err)
		}
		if got != version {
			t.Fatalf("version round-trip: %d → %d", version, got)
		}
		if !bytes.Equal(gotBody, body) {
			t.Fatalf("body round-trip mismatch: %v → %v", body, gotBody)
		}
	})
}
