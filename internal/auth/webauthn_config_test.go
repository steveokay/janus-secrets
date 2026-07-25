package auth

import (
	"errors"
	"strings"
	"testing"
)

func TestWebAuthnConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     WebAuthnConfig
		wantErr bool
	}{
		{"disabled zero value", WebAuthnConfig{}, false},
		{"exact host origin", WebAuthnConfig{RPID: "janus.example.com", Origins: []string{"https://janus.example.com"}}, false},
		{"origin with port", WebAuthnConfig{RPID: "janus.example.com", Origins: []string{"https://janus.example.com:8443"}}, false},
		{"subdomain origin", WebAuthnConfig{RPID: "example.com", Origins: []string{"https://janus.example.com"}}, false},
		{"multiple origins", WebAuthnConfig{RPID: "example.com", Origins: []string{"https://a.example.com", "https://b.example.com"}}, false},
		{"http localhost", WebAuthnConfig{RPID: "localhost", Origins: []string{"http://localhost:5173"}}, false},
		{"http loopback ip host under localhost rpid", WebAuthnConfig{RPID: "localhost", Origins: []string{"http://localhost"}}, false},

		{"rp id without origins", WebAuthnConfig{RPID: "example.com"}, true},
		{"origins without rp id", WebAuthnConfig{Origins: []string{"https://example.com"}}, true},
		{"rp id with scheme", WebAuthnConfig{RPID: "https://example.com", Origins: []string{"https://example.com"}}, true},
		{"rp id with port", WebAuthnConfig{RPID: "example.com:443", Origins: []string{"https://example.com"}}, true},
		{"rp id with path", WebAuthnConfig{RPID: "example.com/janus", Origins: []string{"https://example.com"}}, true},
		{"rp id wildcard", WebAuthnConfig{RPID: "*.example.com", Origins: []string{"https://a.example.com"}}, true},
		{"rp id upper case", WebAuthnConfig{RPID: "Example.com", Origins: []string{"https://example.com"}}, true},
		{"rp id trailing dot", WebAuthnConfig{RPID: "example.com.", Origins: []string{"https://example.com"}}, true},
		{"rp id is an ip", WebAuthnConfig{RPID: "10.0.0.5", Origins: []string{"https://10.0.0.5"}}, true},
		{"origin not a url", WebAuthnConfig{RPID: "example.com", Origins: []string{"://"}}, true},
		{"origin bad scheme", WebAuthnConfig{RPID: "example.com", Origins: []string{"ftp://example.com"}}, true},
		{"origin with path", WebAuthnConfig{RPID: "example.com", Origins: []string{"https://example.com/ui"}}, true},
		{"origin with query", WebAuthnConfig{RPID: "example.com", Origins: []string{"https://example.com?a=1"}}, true},
		{"origin with userinfo", WebAuthnConfig{RPID: "example.com", Origins: []string{"https://u:p@example.com"}}, true},
		{"plain http non-localhost", WebAuthnConfig{RPID: "example.com", Origins: []string{"http://example.com"}}, true},
		{"origin host unrelated to rp id", WebAuthnConfig{RPID: "example.com", Origins: []string{"https://evil.com"}}, true},
		// "notexample.com" ends with "example.com" as a STRING but is not a
		// subdomain — the check must be label-aware, not a suffix match.
		{"origin host suffix but not subdomain", WebAuthnConfig{RPID: "example.com", Origins: []string{"https://notexample.com"}}, true},
		// The RP ID may not be a subdomain of the origin (the relationship only
		// works the other way round).
		{"rp id narrower than origin", WebAuthnConfig{RPID: "a.example.com", Origins: []string{"https://example.com"}}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.wantErr && err == nil {
				t.Fatal("expected a validation error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantErr && !errors.Is(err, ErrValidation) {
				t.Fatalf("error should wrap ErrValidation, got %v", err)
			}
		})
	}
}

func TestWebAuthnConfigEnabled(t *testing.T) {
	if (WebAuthnConfig{}).Enabled() {
		t.Fatal("zero config must be disabled")
	}
	if (WebAuthnConfig{RPID: "example.com"}).Enabled() {
		t.Fatal("rp id without origins must be disabled")
	}
	if !(WebAuthnConfig{RPID: "example.com", Origins: []string{"https://example.com"}}).Enabled() {
		t.Fatal("a complete config must be enabled")
	}
}

// SetWebAuthnConfig must refuse an invalid config rather than half-enabling it.
func TestSetWebAuthnConfigRejectsInvalid(t *testing.T) {
	svc := &Service{}
	if err := svc.SetWebAuthnConfig(WebAuthnConfig{RPID: "example.com", Origins: []string{"https://evil.com"}}); err == nil {
		t.Fatal("expected an error for a mismatched origin")
	}
	if svc.WebAuthnEnabled() {
		t.Fatal("a rejected config must leave passkeys disabled")
	}
	if err := svc.SetWebAuthnConfig(WebAuthnConfig{}); err != nil {
		t.Fatalf("zero config should disable cleanly: %v", err)
	}
	if svc.WebAuthnEnabled() || svc.WebAuthnRPID() != "" {
		t.Fatal("zero config must disable passkeys")
	}
	if err := svc.SetWebAuthnConfig(WebAuthnConfig{RPID: "example.com", Origins: []string{"https://example.com"}}); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	if !svc.WebAuthnEnabled() || svc.WebAuthnRPID() != "example.com" {
		t.Fatal("valid config did not enable passkeys")
	}
}

func TestSanitizeNickname(t *testing.T) {
	tests := []struct{ in, want string }{
		{"  Work laptop  ", "Work laptop"},
		{"", ""},
		{"\x00\x01bad\x7f", "bad"},
		{strings.Repeat("x", 200), strings.Repeat("x", webauthnMaxNickname)},
	}
	for _, tc := range tests {
		if got := sanitizeNickname(tc.in); got != tc.want {
			t.Errorf("sanitizeNickname(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestUserHandleRejectsMalformedID(t *testing.T) {
	if _, err := userHandle("not-a-uuid"); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation, got %v", err)
	}
	h, err := userHandle("0f7d5e2a-1b3c-4d5e-8f90-a1b2c3d4e5f6")
	if err != nil {
		t.Fatalf("valid uuid rejected: %v", err)
	}
	if len(h) != 16 {
		t.Fatalf("handle should be 16 bytes, got %d", len(h))
	}
}
