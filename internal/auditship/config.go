package auditship

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

// Mode selects the shipper's destination kind.
type Mode string

const (
	ModeOff     Mode = "off"
	ModeWebhook Mode = "webhook"
	ModeSyslog  Mode = "syslog"
)

// Config is the operator-supplied audit-shipper configuration, parsed from the
// environment. The destination lives here (not in Postgres); only the durable
// high-water mark is persisted. Secret material (the webhook HMAC key) comes
// from the environment and is never logged or persisted.
type Config struct {
	Mode Mode

	// webhook
	WebhookURL     string
	WebhookHMACKey string

	// syslog
	SyslogNetwork string // "udp" | "tcp"
	SyslogAddr    string // host:port

	// SendTimeout bounds a single destination send. Zero → default.
	SendTimeout time.Duration
}

// Enabled reports whether the shipper has a real destination configured.
func (c Config) Enabled() bool { return c.Mode == ModeWebhook || c.Mode == ModeSyslog }

// ConfigFromEnv reads JANUS_AUDIT_SHIP_* into a Config and validates it. An
// unset/"off" mode returns a disabled config (Mode=off) with no error. A
// configured mode with a missing/invalid destination is a fatal misconfig
// (returned error), so a typo never silently drops the audit stream.
func ConfigFromEnv() (Config, error) {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("JANUS_AUDIT_SHIP_MODE")))
	c := Config{SendTimeout: 15 * time.Second}
	switch mode {
	case "", "off":
		c.Mode = ModeOff
		return c, nil
	case "webhook":
		c.Mode = ModeWebhook
		c.WebhookURL = strings.TrimSpace(os.Getenv("JANUS_AUDIT_SHIP_WEBHOOK_URL"))
		c.WebhookHMACKey = os.Getenv("JANUS_AUDIT_SHIP_WEBHOOK_HMAC_KEY")
		if err := validateWebhookURL(c.WebhookURL); err != nil {
			return Config{}, err
		}
	case "syslog":
		c.Mode = ModeSyslog
		c.SyslogNetwork = strings.ToLower(strings.TrimSpace(os.Getenv("JANUS_AUDIT_SHIP_SYSLOG_NETWORK")))
		if c.SyslogNetwork == "" {
			c.SyslogNetwork = "udp"
		}
		if c.SyslogNetwork != "udp" && c.SyslogNetwork != "tcp" {
			return Config{}, fmt.Errorf("JANUS_AUDIT_SHIP_SYSLOG_NETWORK must be udp or tcp, got %q", c.SyslogNetwork)
		}
		c.SyslogAddr = strings.TrimSpace(os.Getenv("JANUS_AUDIT_SHIP_SYSLOG_ADDR"))
		if err := validateSyslogAddr(c.SyslogAddr); err != nil {
			return Config{}, err
		}
	default:
		return Config{}, fmt.Errorf("JANUS_AUDIT_SHIP_MODE must be off, webhook or syslog, got %q", mode)
	}
	return c, nil
}

// validateWebhookURL enforces an absolute http(s) URL (guards gosec G107 and
// prevents shipping to an unexpected scheme like file://).
func validateWebhookURL(u string) error {
	if u == "" {
		return fmt.Errorf("JANUS_AUDIT_SHIP_WEBHOOK_URL is required when mode=webhook")
	}
	parsed, err := url.Parse(u)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("JANUS_AUDIT_SHIP_WEBHOOK_URL must be an absolute http(s) URL")
	}
	return nil
}

// validateSyslogAddr enforces a host:port destination.
func validateSyslogAddr(addr string) error {
	if addr == "" {
		return fmt.Errorf("JANUS_AUDIT_SHIP_SYSLOG_ADDR is required when mode=syslog")
	}
	host, port, err := splitHostPort(addr)
	if err != nil || host == "" || port == "" {
		return fmt.Errorf("JANUS_AUDIT_SHIP_SYSLOG_ADDR must be host:port")
	}
	return nil
}

// splitHostPort is net.SplitHostPort with a friendlier signature for validation.
func splitHostPort(addr string) (host, port string, err error) {
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		return "", "", fmt.Errorf("missing port")
	}
	host = strings.TrimSpace(addr[:i])
	port = strings.TrimSpace(addr[i+1:])
	// strip optional [ipv6] brackets
	host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	if host == "" || port == "" {
		return "", "", fmt.Errorf("host and port required")
	}
	return host, port, nil
}
