package transit

import "testing"

func TestEnvelopeRoundTrip(t *testing.T) {
	s := formatEnvelope(3, []byte{0x01, 0x02, 0x03})
	if s != "janus:v3:AQID" {
		t.Fatalf("format = %q", s)
	}
	ver, body, err := parseEnvelope("janus:v3:AQID")
	if err != nil {
		t.Fatal(err)
	}
	if ver != 3 || string(body) != "\x01\x02\x03" {
		t.Fatalf("parse = %d %x", ver, body)
	}
}

func TestParseEnvelopeRejects(t *testing.T) {
	for _, bad := range []string{"", "nope", "janus:v0:AQID", "janus:vx:AQID", "janus:v1:!!!", "vault:v1:AQID"} {
		if _, _, err := parseEnvelope(bad); err == nil {
			t.Fatalf("expected error for %q", bad)
		}
	}
}
