package auth

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// WebAuthnConfig is the operator-supplied Relying Party configuration for
// passkeys. It is NEVER inferred from the request Host: a browser silently
// refuses an assertion whose RP ID does not match the one the credential was
// registered under, and a Host-derived RP ID would make that failure depend on
// which proxy hostname a user happened to reach. Configuration is the single
// source of truth, and it is validated at boot (see Validate) so a typo fails
// loudly instead of breaking passkeys silently.
//
// The zero value means "passkeys disabled" — every WebAuthn endpoint then
// reports not-configured rather than half-working.
type WebAuthnConfig struct {
	// RPID is the Relying Party ID: a bare registrable domain, no scheme, no
	// port, no path (e.g. "janus.example.com"). Set from JANUS_WEBAUTHN_RP_ID.
	RPID string
	// RPDisplayName is shown by the authenticator's UI. Defaults to "Janus".
	RPDisplayName string
	// Origins are the fully-qualified origins the SPA is served from
	// (e.g. "https://janus.example.com"). Set from JANUS_WEBAUTHN_ORIGINS.
	Origins []string
}

// Enabled reports whether passkeys are configured.
func (c WebAuthnConfig) Enabled() bool { return c.RPID != "" && len(c.Origins) > 0 }

// Validate checks the RP ID / origin relationship that the browser will enforce
// anyway, but at boot where the operator can see it:
//
//   - RPID must be a bare host: no scheme, no port, no path, no wildcard.
//   - Every origin must parse, use http or https, and carry no path/query.
//   - http:// is only accepted for localhost / 127.0.0.1 / [::1] — WebAuthn
//     requires a secure context everywhere else.
//   - Every origin's host must equal RPID or be a subdomain of it, which is the
//     rule the user agent applies when deciding whether a ceremony may use the
//     requested RP ID.
func (c WebAuthnConfig) Validate() error {
	if c.RPID == "" && len(c.Origins) == 0 {
		return nil // disabled
	}
	if c.RPID == "" {
		return fmt.Errorf("%w: webauthn rp_id is required when origins are set", ErrValidation)
	}
	if len(c.Origins) == 0 {
		return fmt.Errorf("%w: at least one webauthn origin is required", ErrValidation)
	}
	if err := validateRPID(c.RPID); err != nil {
		return err
	}
	for _, o := range c.Origins {
		if err := validateOrigin(o, c.RPID); err != nil {
			return err
		}
	}
	return nil
}

func validateRPID(rpID string) error {
	if strings.ContainsAny(rpID, "/:*? ") {
		return fmt.Errorf("%w: webauthn rp_id %q must be a bare host (no scheme, port, path, or wildcard)", ErrValidation, rpID)
	}
	if rpID != strings.ToLower(rpID) {
		return fmt.Errorf("%w: webauthn rp_id %q must be lower-case", ErrValidation, rpID)
	}
	if strings.HasPrefix(rpID, ".") || strings.HasSuffix(rpID, ".") {
		return fmt.Errorf("%w: webauthn rp_id %q has a leading or trailing dot", ErrValidation, rpID)
	}
	// An IP address is not a valid RP ID (the spec requires a domain).
	if net.ParseIP(rpID) != nil {
		return fmt.Errorf("%w: webauthn rp_id %q must be a domain, not an IP address", ErrValidation, rpID)
	}
	return nil
}

func validateOrigin(origin, rpID string) error {
	u, err := url.Parse(origin)
	if err != nil {
		return fmt.Errorf("%w: webauthn origin %q is not a URL", ErrValidation, origin)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%w: webauthn origin %q must use http or https", ErrValidation, origin)
	}
	if u.Path != "" && u.Path != "/" {
		return fmt.Errorf("%w: webauthn origin %q must not carry a path", ErrValidation, origin)
	}
	if u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return fmt.Errorf("%w: webauthn origin %q must be scheme://host[:port] only", ErrValidation, origin)
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return fmt.Errorf("%w: webauthn origin %q has no host", ErrValidation, origin)
	}
	if u.Scheme == "http" && !isLocalhostHost(host) {
		return fmt.Errorf("%w: webauthn origin %q must be https (http is only allowed for localhost)", ErrValidation, origin)
	}
	if host != rpID && !strings.HasSuffix(host, "."+rpID) {
		return fmt.Errorf("%w: webauthn origin %q host %q is neither rp_id %q nor a subdomain of it",
			ErrValidation, origin, host, rpID)
	}
	return nil
}

// isLocalhostHost reports whether host is a loopback name/address, the only
// place a browser treats http:// as a secure context.
func isLocalhostHost(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}
