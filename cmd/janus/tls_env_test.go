package main

import (
	"reflect"
	"testing"
)

// TestBuildTLSConfig covers the JANUS_TLS_* env parsing and validation surface.
func TestBuildTLSConfig(t *testing.T) {
	tests := []struct {
		name      string
		env       map[string]string
		wantErr   bool
		wantCert  string
		wantKey   string
		wantDomns []string
		wantEmail string
		wantCache string
		wantRedir string
		wantMode  string
	}{
		{
			name:     "no env is plain http",
			env:      map[string]string{},
			wantMode: "http",
		},
		{
			name:      "static certs both set",
			env:       map[string]string{"JANUS_TLS_CERT": "/etc/c.pem", "JANUS_TLS_KEY": "/etc/k.pem", "JANUS_TLS_REDIRECT_HTTP": ":80"},
			wantCert:  "/etc/c.pem",
			wantKey:   "/etc/k.pem",
			wantRedir: ":80",
			wantMode:  "https (static-cert)",
		},
		{
			name:    "cert without key errors",
			env:     map[string]string{"JANUS_TLS_CERT": "/etc/c.pem"},
			wantErr: true,
		},
		{
			name:      "acme domains parsed and trimmed",
			env:       map[string]string{"JANUS_TLS_ACME_DOMAINS": " a.example , b.example ,", "JANUS_TLS_ACME_EMAIL": "ops@example", "JANUS_TLS_ACME_CACHE": "/var/janus"},
			wantDomns: []string{"a.example", "b.example"},
			wantEmail: "ops@example",
			wantCache: "/var/janus",
			wantMode:  "https (acme)",
		},
		{
			name:    "static and acme mutually exclusive",
			env:     map[string]string{"JANUS_TLS_CERT": "/c", "JANUS_TLS_KEY": "/k", "JANUS_TLS_ACME_DOMAINS": "a.example"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// t.Setenv both sets and auto-restores; unset vars stay unset because
			// each subtest starts from the ambient (clean) environment.
			for _, k := range []string{
				"JANUS_TLS_CERT", "JANUS_TLS_KEY", "JANUS_TLS_ACME_DOMAINS",
				"JANUS_TLS_ACME_EMAIL", "JANUS_TLS_ACME_CACHE", "JANUS_TLS_REDIRECT_HTTP",
			} {
				t.Setenv(k, "")
			}
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			cfg, err := buildTLSConfig()
			if (err != nil) != tt.wantErr {
				t.Fatalf("buildTLSConfig() err=%v wantErr=%v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if cfg.CertFile != tt.wantCert {
				t.Errorf("CertFile = %q, want %q", cfg.CertFile, tt.wantCert)
			}
			if cfg.KeyFile != tt.wantKey {
				t.Errorf("KeyFile = %q, want %q", cfg.KeyFile, tt.wantKey)
			}
			if len(tt.wantDomns) > 0 && !reflect.DeepEqual(cfg.ACMEDomains, tt.wantDomns) {
				t.Errorf("ACMEDomains = %v, want %v", cfg.ACMEDomains, tt.wantDomns)
			}
			if cfg.ACMEEmail != tt.wantEmail {
				t.Errorf("ACMEEmail = %q, want %q", cfg.ACMEEmail, tt.wantEmail)
			}
			if cfg.ACMECache != tt.wantCache {
				t.Errorf("ACMECache = %q, want %q", cfg.ACMECache, tt.wantCache)
			}
			if cfg.RedirectHTTP != tt.wantRedir {
				t.Errorf("RedirectHTTP = %q, want %q", cfg.RedirectHTTP, tt.wantRedir)
			}
			if got := tlsMode(cfg); got != tt.wantMode {
				t.Errorf("tlsMode = %q, want %q", got, tt.wantMode)
			}
		})
	}
}
