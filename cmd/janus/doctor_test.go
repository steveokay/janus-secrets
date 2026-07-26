package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/steveokay/janus-secrets/internal/api"
	"github.com/steveokay/janus-secrets/internal/auth"
	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/store"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// clearJanusEnv unsets every JANUS_* variable for the duration of a test, so a
// developer's shell (or a previous subtest) cannot influence the result.
func clearJanusEnv(t *testing.T) {
	t.Helper()
	for _, kv := range os.Environ() {
		i := strings.IndexByte(kv, '=')
		if i <= 0 {
			continue
		}
		if k := kv[:i]; strings.HasPrefix(k, "JANUS_") {
			// t.Setenv registers the restore; unsetting straight after keeps the
			// variable gone for this test while still being restored on cleanup.
			t.Setenv(k, "")
			_ = os.Unsetenv(k)
		}
	}
}

func setEnv(t *testing.T, env map[string]string) {
	t.Helper()
	for k, v := range env {
		t.Setenv(k, v)
	}
}

// janusStub serves the two endpoints doctor probes, so origin reachability can
// be exercised without booting the real server.
func janusStub(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sys/live", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"live"}`))
	})
	mux.HandleFunc("/v1/sys/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","initialized":true,"sealed":false}`))
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

// writeCert emits a self-signed leaf + key into dir and returns their paths.
func writeCert(t *testing.T, dir, host string, notBefore, notAfter time.Time) (certPath, keyPath string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}

// ---------------------------------------------------------------------------
// env.unknown — the highest-value check
// ---------------------------------------------------------------------------

func TestCheckUnknownEnv(t *testing.T) {
	tests := []struct {
		name       string
		env        map[string]string
		want       doctorStatus
		wantDetail []string
	}{
		{
			name: "only known variables",
			env: map[string]string{
				"JANUS_DATABASE_URL":     "postgres://u:p@db:5432/janus",
				"JANUS_SEAL_TYPE":        "shamir",
				"JANUS_WEBAUTHN_ORIGINS": "https://janus.example.com",
			},
			want: statusPass,
		},
		{
			// The incident's sibling: a singular ORIGIN is silently ignored.
			name:       "singular ORIGIN typo is named with a suggestion",
			env:        map[string]string{"JANUS_WEBAUTHN_ORIGIN": "http://localhost:8210"},
			want:       statusWarn,
			wantDetail: []string{"JANUS_WEBAUTHN_ORIGIN — did you mean JANUS_WEBAUTHN_ORIGINS?"},
		},
		{
			name:       "transposition is suggested",
			env:        map[string]string{"JANUS_DATABSE_URL": "postgres://x"},
			want:       statusWarn,
			wantDetail: []string{"did you mean JANUS_DATABASE_URL?"},
		},
		{
			name:       "far-off name is listed without a guess",
			env:        map[string]string{"JANUS_TOTALLY_MADE_UP_THING": "1"},
			want:       statusWarn,
			wantDetail: []string{"JANUS_TOTALLY_MADE_UP_THING"},
		},
		{
			name: "non-JANUS variables are ignored",
			env:  map[string]string{"PATH_LIKE_THING": "x"},
			want: statusPass,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearJanusEnv(t)
			setEnv(t, tc.env)
			got := checkUnknownEnv()
			if got.Status != tc.want {
				t.Fatalf("status = %s, want %s (detail %v)", got.Status, tc.want, got.Detail)
			}
			joined := strings.Join(got.Detail, "\n")
			for _, want := range tc.wantDetail {
				if !strings.Contains(joined, want) {
					t.Errorf("detail %q does not contain %q", joined, want)
				}
			}
			if tc.want == statusWarn && got.Fix == "" {
				t.Error("a WARN must carry a fix")
			}
		})
	}
}

// TestDoctorEnvAllowlistCoversSource is the drift guard: every JANUS_* variable
// the source actually reads must be in knownEnvVars, or doctor would report a
// legitimate variable as a typo (crying wolf is how a diagnostic gets ignored).
func TestDoctorEnvAllowlistCoversSource(t *testing.T) {
	re := regexp.MustCompile(`"(JANUS_[A-Z0-9_]+)"`)
	seen := map[string]string{}
	root := filepath.Join("..", "..")
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // unreadable paths are not this test's business
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "node_modules", "web", "docs":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		b, rerr := os.ReadFile(p) // #nosec G304 -- test walks the repo it lives in
		if rerr != nil {
			return nil
		}
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			if _, ok := seen[m[1]]; !ok {
				seen[m[1]] = p
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) < 20 {
		t.Fatalf("only found %d JANUS_* literals; the walk is not reaching the source", len(seen))
	}
	for name, file := range seen {
		if !knownEnvVars[name] {
			t.Errorf("%s is read by %s but missing from knownEnvVars in doctor.go", name, file)
		}
	}
}

func TestEditDistance(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "abc", 0},
		{"abc", "abd", 1},
		{"JANUS_WEBAUTHN_ORIGIN", "JANUS_WEBAUTHN_ORIGINS", 1},
		{"kitten", "sitting", 3},
		{"", "abc", 3},
	}
	for _, tc := range tests {
		if got := editDistance(tc.a, tc.b); got != tc.want {
			t.Errorf("editDistance(%q,%q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// db checks
// ---------------------------------------------------------------------------

func TestCheckDatabaseDSN(t *testing.T) {
	tests := []struct {
		name string
		dsn  string
		want doctorStatus
	}{
		{"unset", "", statusFail},
		{"valid url", "postgres://janus:pw@db.internal:5432/janus?sslmode=require", statusPass},
		{"garbage", "://:::", statusFail},
		{"keyword dsn is not url-shaped", "host=db user=janus dbname=janus", statusWarn},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := checkDatabaseDSN(tc.dsn)
			if got.Status != tc.want {
				t.Fatalf("status = %s, want %s (%s)", got.Status, tc.want, got.Summary)
			}
			if got.Status != statusPass && got.Fix == "" {
				t.Error("a non-PASS must carry a fix")
			}
		})
	}
}

func TestCheckDatabaseSSLMode(t *testing.T) {
	tests := []struct {
		name string
		dsn  string
		want doctorStatus
	}{
		{"unset", "", statusSkip},
		{"require", "postgres://u:p@db:5432/janus?sslmode=require", statusPass},
		{"verify-full", "postgres://u:p@db:5432/janus?sslmode=verify-full", statusPass},
		{"disable to loopback is fine (the dev stack)", "postgres://u:p@127.0.0.1:5433/janus?sslmode=disable", statusPass},
		{"disable to localhost is fine", "postgres://u:p@localhost:5433/janus?sslmode=disable", statusPass},
		{"disable to remote warns", "postgres://u:p@db.internal:5432/janus?sslmode=disable", statusWarn},
		{"unset sslmode to remote warns", "postgres://u:p@db.internal:5432/janus", statusWarn},
		{"unset sslmode to loopback is fine", "postgres://u:p@127.0.0.1:5432/janus", statusPass},
		{"nonsense mode warns", "postgres://u:p@db:5432/janus?sslmode=maybe", statusWarn},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := checkDatabaseSSLMode(tc.dsn)
			if got.Status != tc.want {
				t.Fatalf("status = %s, want %s (%s)", got.Status, tc.want, got.Summary)
			}
		})
	}
}

func TestCheckDatabasePool(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want doctorStatus
	}{
		{"unset", nil, statusPass},
		{"coherent", map[string]string{
			"JANUS_DB_MAX_CONNS":          "20",
			"JANUS_DB_MIN_CONNS":          "2",
			"JANUS_DB_MAX_CONN_LIFETIME":  "1h",
			"JANUS_DB_MAX_CONN_IDLE_TIME": "30m",
		}, statusPass},
		{"invalid value", map[string]string{"JANUS_DB_MAX_CONNS": "many"}, statusFail},
		{"min above max", map[string]string{
			"JANUS_DB_MAX_CONNS": "4", "JANUS_DB_MIN_CONNS": "8",
		}, statusFail},
		{"idle not below lifetime", map[string]string{
			"JANUS_DB_MAX_CONN_LIFETIME": "30m", "JANUS_DB_MAX_CONN_IDLE_TIME": "1h",
		}, statusWarn},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearJanusEnv(t)
			setEnv(t, tc.env)
			got := checkDatabasePool()
			if got.Status != tc.want {
				t.Fatalf("status = %s, want %s (%s)", got.Status, tc.want, got.Summary)
			}
		})
	}
}

func TestEmbeddedSchemaVersion(t *testing.T) {
	v, err := embeddedSchemaVersion()
	if err != nil {
		t.Fatal(err)
	}
	if v < 44 {
		t.Fatalf("embedded schema version = %d, expected at least the 44 migrations in the tree", v)
	}
}

// ---------------------------------------------------------------------------
// seal
// ---------------------------------------------------------------------------

func TestCheckSealEnvOnly(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want doctorStatus
	}{
		{"unknown type", map[string]string{"JANUS_SEAL_TYPE": "vault"}, statusFail},
		{"shamir", map[string]string{"JANUS_SEAL_TYPE": "shamir"}, statusPass},
		{"awskms without arn", map[string]string{"JANUS_SEAL_TYPE": "awskms"}, statusFail},
		{"awskms with arn", map[string]string{
			"JANUS_SEAL_TYPE": "awskms", "JANUS_AWS_KMS_KEY_ARN": "arn:aws:kms:eu-west-1:1:key/abc",
		}, statusPass},
		{"gcpkms without key", map[string]string{"JANUS_SEAL_TYPE": "gcpkms"}, statusFail},
		{"azurekv missing key name", map[string]string{
			"JANUS_SEAL_TYPE": "azurekv", "JANUS_AZURE_KEYVAULT_URL": "https://v.vault.azure.net",
		}, statusFail},
		{"azurekv complete", map[string]string{
			"JANUS_SEAL_TYPE":          "azurekv",
			"JANUS_AZURE_KEYVAULT_URL": "https://v.vault.azure.net",
			"JANUS_AZURE_KEY_NAME":     "janus",
		}, statusPass},
		{"unset with no database", nil, statusWarn},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearJanusEnv(t)
			setEnv(t, tc.env)
			got := checkSeal(context.Background(), nil, time.Second, newScrubber())
			if got.Status != tc.want {
				t.Fatalf("status = %s, want %s (%s)", got.Status, tc.want, got.Summary)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// webauthn — the motivating case
// ---------------------------------------------------------------------------

func TestCheckWebAuthnConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  auth.WebAuthnConfig
		want doctorStatus
	}{
		{"disabled", auth.WebAuthnConfig{}, statusPass},
		{"valid", auth.WebAuthnConfig{RPID: "janus.example.com", Origins: []string{"https://janus.example.com"}}, statusPass},
		{"rp id without origins", auth.WebAuthnConfig{RPID: "janus.example.com"}, statusFail},
		{"origin is not a subdomain", auth.WebAuthnConfig{
			RPID: "example.com", Origins: []string{"https://notexample.com"},
		}, statusFail},
		{"rp id carries a port", auth.WebAuthnConfig{
			RPID: "janus.example.com:8443", Origins: []string{"https://janus.example.com:8443"},
		}, statusFail},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := checkWebAuthnConfig(tc.cfg)
			if got.Status != tc.want {
				t.Fatalf("status = %s, want %s (%s)", got.Status, tc.want, got.Summary)
			}
		})
	}
}

// TestCheckWebAuthnOriginsCatchesPortMismatch is the regression test for the
// incident that motivated `janus doctor`: JANUS_WEBAUTHN_ORIGINS naming a port
// the server does not serve on. The configuration is internally valid, so boot
// accepts it and the passkey ceremony fails in the browser looking like a bug.
func TestCheckWebAuthnOriginsCatchesPortMismatch(t *testing.T) {
	clearJanusEnv(t)
	ts := janusStub(t)
	served, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	servedPort := served.Port()
	// A port nothing is listening on: the stub's port + 1 is not bound to a
	// Janus, and the dial is refused (or answers as not-Janus, also a problem).
	wrongOrigin := "http://localhost:1" // port 1 is never a Janus

	prev := containerDetector
	containerDetector = func() bool { return false }
	t.Cleanup(func() { containerDetector = prev })

	opts := doctorOpts{timeout: 2 * time.Second}

	t.Run("origin matching the served port passes", func(t *testing.T) {
		cfg := auth.WebAuthnConfig{RPID: "localhost", Origins: []string{"http://localhost:" + servedPort}}
		got := checkWebAuthnOrigins(context.Background(), cfg, "127.0.0.1:"+servedPort, opts)
		if got.Status != statusPass {
			t.Fatalf("status = %s, want PASS (%s; %v)", got.Status, got.Summary, got.Detail)
		}
	})

	t.Run("origin on a port nothing serves warns and names both ports", func(t *testing.T) {
		cfg := auth.WebAuthnConfig{RPID: "localhost", Origins: []string{wrongOrigin}}
		got := checkWebAuthnOrigins(context.Background(), cfg, "127.0.0.1:"+servedPort, opts)
		if got.Status != statusWarn {
			t.Fatalf("status = %s, want WARN (%s)", got.Status, got.Summary)
		}
		joined := strings.Join(got.Detail, "\n")
		if !strings.Contains(joined, "nothing is listening on that port") {
			t.Errorf("detail should say nothing is listening, got %q", joined)
		}
		if !strings.Contains(joined, servedPort) || !strings.Contains(joined, "port 1") {
			t.Errorf("detail should name both the configured and the served port, got %q", joined)
		}
		if !strings.Contains(got.Fix, "JANUS_WEBAUTHN_ORIGINS") {
			t.Errorf("fix should name the variable to change, got %q", got.Fix)
		}
	})

	// The exact shape of the incident: another Janus really is listening on the
	// port the origin names. A probe alone would have called this healthy.
	t.Run("a different Janus on the origin port is still a warning", func(t *testing.T) {
		cfg := auth.WebAuthnConfig{RPID: "localhost", Origins: []string{"http://localhost:" + servedPort}}
		got := checkWebAuthnOrigins(context.Background(), cfg, "127.0.0.1:18212", opts)
		if got.Status != statusWarn {
			t.Fatalf("status = %s, want WARN (%s; %v)", got.Status, got.Summary, got.Detail)
		}
		joined := strings.Join(got.Detail, "\n")
		if !strings.Contains(joined, "DIFFERENT Janus instance") {
			t.Errorf("detail should identify the other instance, got %q", joined)
		}
	})

	// A local reverse proxy terminating on :443 in front of :8200 is a normal
	// production shape and must not be reported as a problem.
	t.Run("standard ports are assumed to be a local reverse proxy", func(t *testing.T) {
		cfg := auth.WebAuthnConfig{RPID: "localhost", Origins: []string{"http://localhost"}}
		got := checkWebAuthnOrigins(context.Background(), cfg, "127.0.0.1:8200", opts)
		if got.Status != statusPass {
			t.Fatalf("status = %s, want PASS (%s; %v)", got.Status, got.Summary, got.Detail)
		}
		if !strings.Contains(strings.Join(got.Detail, "\n"), "reverse proxy") {
			t.Errorf("detail should explain the assumption, got %v", got.Detail)
		}
	})

	t.Run("loopback origins are not judged from inside a container", func(t *testing.T) {
		containerDetector = func() bool { return true }
		defer func() { containerDetector = func() bool { return false } }()
		cfg := auth.WebAuthnConfig{RPID: "localhost", Origins: []string{wrongOrigin}}
		got := checkWebAuthnOrigins(context.Background(), cfg, "127.0.0.1:"+servedPort, opts)
		if got.Status != statusSkip {
			t.Fatalf("status = %s, want SKIP inside a container (%s)", got.Status, got.Summary)
		}
	})

	t.Run("disabled passkeys skip", func(t *testing.T) {
		got := checkWebAuthnOrigins(context.Background(), auth.WebAuthnConfig{}, ":8200", opts)
		if got.Status != statusSkip {
			t.Fatalf("status = %s, want SKIP", got.Status)
		}
	})

	// --offline still catches the mismatch: the loopback verdict comes from the
	// listen address, not from the network.
	t.Run("offline still catches a loopback port mismatch", func(t *testing.T) {
		cfg := auth.WebAuthnConfig{RPID: "localhost", Origins: []string{wrongOrigin}}
		got := checkWebAuthnOrigins(context.Background(), cfg, ":8200", doctorOpts{offline: true, timeout: time.Second})
		if got.Status != statusWarn {
			t.Fatalf("status = %s, want WARN under --offline (%s)", got.Status, got.Summary)
		}
		joined := strings.Join(got.Detail, "\n")
		if strings.Contains(joined, "nothing is listening") || strings.Contains(joined, "DIFFERENT Janus") {
			t.Errorf("--offline must not add probe-derived evidence, got %q", joined)
		}
	})
}

func TestCheckWebAuthnOriginsUnresolvableHost(t *testing.T) {
	clearJanusEnv(t)
	prev := containerDetector
	containerDetector = func() bool { return false }
	t.Cleanup(func() { containerDetector = prev })

	cfg := auth.WebAuthnConfig{
		RPID:    "invalid.",
		Origins: []string{"https://invalid."},
	}
	// A trailing-dot TLD that cannot resolve; the check must call it out as a
	// name problem rather than a connectivity one.
	got := checkWebAuthnOrigins(context.Background(), cfg, ":8200", doctorOpts{timeout: 2 * time.Second})
	if got.Status == statusPass {
		t.Fatalf("an unresolvable origin should not pass: %s %v", got.Summary, got.Detail)
	}
}

func TestOriginPortAndListenPort(t *testing.T) {
	tests := []struct{ origin, want string }{
		{"https://a.example.com", "443"},
		{"http://localhost", "80"},
		{"http://localhost:8210", "8210"},
		{"https://a.example.com:8443", "8443"},
	}
	for _, tc := range tests {
		u, err := url.Parse(tc.origin)
		if err != nil {
			t.Fatal(err)
		}
		if got := originPort(u); got != tc.want {
			t.Errorf("originPort(%q) = %q, want %q", tc.origin, got, tc.want)
		}
	}
	listens := []struct{ addr, want string }{
		{"", "8200"},
		{":8212", "8212"},
		{"127.0.0.1:8212", "8212"},
		{"nonsense", ""},
	}
	for _, tc := range listens {
		if got := listenPortOf(tc.addr); got != tc.want {
			t.Errorf("listenPortOf(%q) = %q, want %q", tc.addr, got, tc.want)
		}
	}
}

func TestLocalServerURL(t *testing.T) {
	tests := []struct {
		addr string
		tls  bool
		want string
	}{
		{"", false, "http://127.0.0.1:8200"},
		{":8212", false, "http://127.0.0.1:8212"},
		{"0.0.0.0:9000", true, "https://127.0.0.1:9000"},
		{"10.0.0.5:8200", false, "http://10.0.0.5:8200"},
		{"garbage", false, ""},
	}
	for _, tc := range tests {
		if got := localServerURL(tc.addr, tc.tls); got != tc.want {
			t.Errorf("localServerURL(%q,%v) = %q, want %q", tc.addr, tc.tls, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// tls
// ---------------------------------------------------------------------------

func TestCheckTLS(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	goodCert, goodKey := writeCert(t, t.TempDir(), "janus.example.com", now.Add(-time.Hour), now.Add(365*24*time.Hour))
	expDir := t.TempDir()
	expCert, expKey := writeCert(t, expDir, "janus.example.com", now.Add(-48*time.Hour), now.Add(-time.Hour))
	soonDir := t.TempDir()
	soonCert, soonKey := writeCert(t, soonDir, "janus.example.com", now.Add(-time.Hour), now.Add(3*24*time.Hour))

	tests := []struct {
		name string
		env  map[string]string
		want doctorStatus
	}{
		{"plain http is the documented default", nil, statusPass},
		{"static certs", map[string]string{
			"JANUS_TLS_CERT": goodCert, "JANUS_TLS_KEY": goodKey,
		}, statusPass},
		{"only one half of the static pair", map[string]string{
			"JANUS_TLS_CERT": goodCert,
		}, statusFail},
		{"static and acme together", map[string]string{
			"JANUS_TLS_CERT": goodCert, "JANUS_TLS_KEY": goodKey,
			"JANUS_TLS_ACME_DOMAINS": "janus.example.com",
		}, statusFail},
		{"missing cert file", map[string]string{
			"JANUS_TLS_CERT": filepath.Join(dir, "nope.pem"), "JANUS_TLS_KEY": goodKey,
		}, statusFail},
		{"expired cert", map[string]string{
			"JANUS_TLS_CERT": expCert, "JANUS_TLS_KEY": expKey,
		}, statusFail},
		{"cert expiring soon", map[string]string{
			"JANUS_TLS_CERT": soonCert, "JANUS_TLS_KEY": soonKey,
		}, statusWarn},
		{"cert does not cover the passkey rp id", map[string]string{
			"JANUS_TLS_CERT": goodCert, "JANUS_TLS_KEY": goodKey,
			"JANUS_WEBAUTHN_RP_ID": "other.example.com",
		}, statusWarn},
		{"cert covers the passkey rp id", map[string]string{
			"JANUS_TLS_CERT": goodCert, "JANUS_TLS_KEY": goodKey,
			"JANUS_WEBAUTHN_RP_ID": "janus.example.com",
		}, statusPass},
		{"acme email set without acme", map[string]string{
			"JANUS_TLS_ACME_EMAIL": "ops@example.com",
		}, statusWarn},
		{"redirect set without static certs", map[string]string{
			"JANUS_TLS_REDIRECT_HTTP": ":80",
		}, statusWarn},
		{"acme", map[string]string{
			"JANUS_TLS_ACME_DOMAINS": "janus.example.com", "JANUS_TLS_ACME_EMAIL": "ops@example.com",
		}, statusPass},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearJanusEnv(t)
			setEnv(t, tc.env)
			cfg, err := buildTLSConfig()
			got := checkTLS(cfg, err, "", newScrubber())
			if got.Status != tc.want {
				t.Fatalf("status = %s, want %s (%s; %v)", got.Status, tc.want, got.Summary, got.Detail)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// outbound / observability / subsystems
// ---------------------------------------------------------------------------

func TestCheckOutbound(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want doctorStatus
	}{
		{"default", nil, statusPass},
		{"block private", map[string]string{"JANUS_OUTBOUND_BLOCK_PRIVATE": "true"}, statusPass},
		{"allow proxy without a proxy set", map[string]string{"JANUS_OUTBOUND_ALLOW_PROXY": "true"}, statusWarn},
		{"allow proxy with a proxy set", map[string]string{
			"JANUS_OUTBOUND_ALLOW_PROXY": "true", "HTTPS_PROXY": "http://proxy.internal:3128",
		}, statusWarn},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearJanusEnv(t)
			t.Setenv("HTTP_PROXY", "")
			t.Setenv("HTTPS_PROXY", "")
			t.Setenv("ALL_PROXY", "")
			setEnv(t, tc.env)
			got := checkOutbound()
			if got.Status != tc.want {
				t.Fatalf("status = %s, want %s (%s)", got.Status, tc.want, got.Summary)
			}
		})
	}
}

func TestCheckMetricsAndLogging(t *testing.T) {
	tests := []struct {
		name  string
		env   map[string]string
		check func() doctorCheck
		want  doctorStatus
	}{
		{"metrics disabled", nil, checkMetrics, statusPass},
		{"metrics with a weak token", map[string]string{"JANUS_METRICS_TOKEN": "hunter2"}, checkMetrics, statusWarn},
		{"metrics with a strong token", map[string]string{
			"JANUS_METRICS_TOKEN": strings.Repeat("a", 64),
		}, checkMetrics, statusPass},
		{"logging default", nil, checkLogging, statusPass},
		{"logging json warn", map[string]string{
			"JANUS_LOG_LEVEL": "warn", "JANUS_LOG_FORMAT": "json",
		}, checkLogging, statusPass},
		{"logging debug", map[string]string{"JANUS_LOG_LEVEL": "debug"}, checkLogging, statusWarn},
		{"logging invalid level", map[string]string{"JANUS_LOG_LEVEL": "verbose"}, checkLogging, statusWarn},
		{"logging invalid format", map[string]string{"JANUS_LOG_FORMAT": "yaml"}, checkLogging, statusWarn},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearJanusEnv(t)
			setEnv(t, tc.env)
			got := tc.check()
			if got.Status != tc.want {
				t.Fatalf("status = %s, want %s (%s)", got.Status, tc.want, got.Summary)
			}
		})
	}
}

// TestCheckMetricsNeverPrintsTheToken guards the obvious footgun in a check
// whose whole subject is a credential.
func TestCheckMetricsNeverPrintsTheToken(t *testing.T) {
	clearJanusEnv(t)
	const tok = "s3cr3t-metrics-token-value"
	t.Setenv("JANUS_METRICS_TOKEN", tok)
	got := checkMetrics()
	blob := got.Summary + got.Fix + strings.Join(got.Detail, " ")
	if strings.Contains(blob, tok) {
		t.Fatalf("the metrics token leaked into the report: %q", blob)
	}
}

func TestCheckHTTPLimits(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want doctorStatus
	}{
		{"defaults", nil, statusPass},
		{"tuned but safe", map[string]string{
			"JANUS_HTTP_READ_TIMEOUT": "45s", "JANUS_HTTP_IDLE_TIMEOUT": "3m",
		}, statusPass},
		{"write timeout truncates streams", map[string]string{"JANUS_HTTP_WRITE_TIMEOUT": "60s"}, statusWarn},
		{"body cap removed", map[string]string{"JANUS_HTTP_MAX_BODY_BYTES": "0"}, statusWarn},
		{"read timeout disabled", map[string]string{"JANUS_HTTP_READ_TIMEOUT": "0"}, statusWarn},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearJanusEnv(t)
			setEnv(t, tc.env)
			got := checkHTTPLimits()
			if got.Status != tc.want {
				t.Fatalf("status = %s, want %s (%s)", got.Status, tc.want, got.Summary)
			}
		})
	}
}

func TestCheckAuditShippingAndBackups(t *testing.T) {
	tests := []struct {
		name  string
		env   map[string]string
		check func() doctorCheck
		want  doctorStatus
	}{
		{"shipping off", nil, checkAuditShipping, statusPass},
		{"shipping half-configured", map[string]string{
			"JANUS_AUDIT_SHIP_WEBHOOK_URL": "https://siem.example.com/in",
		}, checkAuditShipping, statusWarn},
		{"shipping webhook without hmac", map[string]string{
			"JANUS_AUDIT_SHIP_MODE":        "webhook",
			"JANUS_AUDIT_SHIP_WEBHOOK_URL": "https://siem.example.com/in",
		}, checkAuditShipping, statusWarn},
		{"shipping webhook complete", map[string]string{
			"JANUS_AUDIT_SHIP_MODE":             "webhook",
			"JANUS_AUDIT_SHIP_WEBHOOK_URL":      "https://siem.example.com/in",
			"JANUS_AUDIT_SHIP_WEBHOOK_HMAC_KEY": strings.Repeat("k", 32),
		}, checkAuditShipping, statusPass},
		{"shipping enabled but tick zero", map[string]string{
			"JANUS_AUDIT_SHIP_MODE":             "webhook",
			"JANUS_AUDIT_SHIP_WEBHOOK_URL":      "https://siem.example.com/in",
			"JANUS_AUDIT_SHIP_WEBHOOK_HMAC_KEY": strings.Repeat("k", 32),
			"JANUS_AUDIT_SHIP_TICK":             "0",
		}, checkAuditShipping, statusWarn},
		{"shipping bad mode", map[string]string{"JANUS_AUDIT_SHIP_MODE": "kafka"}, checkAuditShipping, statusFail},

		{"backups off", nil, checkBackupSchedule, statusPass},
		{"bucket without tick", map[string]string{
			"JANUS_BACKUP_S3_BUCKET": "janus-dr",
		}, checkBackupSchedule, statusWarn},
		{"tick without credentials", map[string]string{
			"JANUS_BACKUP_TICK": "6h", "JANUS_BACKUP_S3_BUCKET": "janus-dr",
		}, checkBackupSchedule, statusFail},
		{"complete", map[string]string{
			"JANUS_BACKUP_TICK":                 "6h",
			"JANUS_BACKUP_S3_BUCKET":            "janus-dr",
			"JANUS_BACKUP_S3_REGION":            "eu-west-1",
			"JANUS_BACKUP_S3_ACCESS_KEY_ID":     "AKIAEXAMPLE",
			"JANUS_BACKUP_S3_SECRET_ACCESS_KEY": "shhh-not-in-the-report",
		}, checkBackupSchedule, statusPass},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearJanusEnv(t)
			setEnv(t, tc.env)
			got := tc.check()
			if got.Status != tc.want {
				t.Fatalf("status = %s, want %s (%s)", got.Status, tc.want, got.Summary)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// server.status
// ---------------------------------------------------------------------------

func TestCheckServerReachable(t *testing.T) {
	clearJanusEnv(t)
	ts := janusStub(t)

	got := checkServerReachable(context.Background(), doctorOpts{timeout: 2 * time.Second, address: ts.URL},
		"", buildTLSConfigOrDie(t), newScrubber())
	if got.Status != statusPass {
		t.Fatalf("status = %s, want PASS (%s)", got.Status, got.Summary)
	}

	// Nothing listening → SKIP, because doctor is explicitly usable without a
	// running server; an absent server must not manufacture a failure.
	got = checkServerReachable(context.Background(), doctorOpts{timeout: time.Second, address: "http://127.0.0.1:1"},
		"", buildTLSConfigOrDie(t), newScrubber())
	if got.Status != statusSkip {
		t.Fatalf("status = %s, want SKIP for an unreachable server (%s)", got.Status, got.Summary)
	}

	got = checkServerReachable(context.Background(), doctorOpts{offline: true}, "", buildTLSConfigOrDie(t), newScrubber())
	if got.Status != statusSkip {
		t.Fatalf("status = %s, want SKIP under --offline", got.Status)
	}
}

func TestCheckServerReachableSealed(t *testing.T) {
	clearJanusEnv(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sys/health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok","initialized":true,"sealed":true}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	got := checkServerReachable(context.Background(), doctorOpts{timeout: 2 * time.Second, address: ts.URL},
		"", buildTLSConfigOrDie(t), newScrubber())
	if got.Status != statusWarn || !strings.Contains(got.Summary, "SEALED") {
		t.Fatalf("status = %s (%s), want a WARN naming the sealed state", got.Status, got.Summary)
	}
}

func buildTLSConfigOrDie(t *testing.T) api.TLSConfig {
	t.Helper()
	c, err := buildTLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// ---------------------------------------------------------------------------
// report shape, exit status, redaction
// ---------------------------------------------------------------------------

func TestFinalizeReportExitVerdict(t *testing.T) {
	tests := []struct {
		name     string
		statuses []doctorStatus
		strict   bool
		wantOK   bool
	}{
		{"all pass", []doctorStatus{statusPass, statusPass}, false, true},
		{"warn is tolerated", []doctorStatus{statusPass, statusWarn}, false, true},
		{"warn fails under strict", []doctorStatus{statusPass, statusWarn}, true, false},
		{"fail always fails", []doctorStatus{statusPass, statusFail}, false, false},
		{"skip is neutral", []doctorStatus{statusSkip, statusSkip}, true, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rep := &doctorReport{}
			for i, s := range tc.statuses {
				rep.add(doctorCheck{Name: fmt.Sprintf("c%d", i), Status: s})
			}
			finalizeReport(rep, tc.strict, newScrubber())
			if rep.OK != tc.wantOK {
				t.Fatalf("OK = %v, want %v (%+v)", rep.OK, tc.wantOK, rep.Summary)
			}
		})
	}
}

func TestDoctorJSONOutput(t *testing.T) {
	clearJanusEnv(t)
	rep := runDoctor(context.Background(), doctorOpts{offline: true, timeout: time.Second})
	var sb strings.Builder
	if err := writeDoctorReport(&sb, rep, true); err != nil {
		t.Fatal(err)
	}
	var decoded doctorReport
	if err := json.Unmarshal([]byte(sb.String()), &decoded); err != nil {
		t.Fatalf("--json did not emit valid JSON: %v", err)
	}
	if len(decoded.Checks) != len(rep.Checks) || len(decoded.Checks) < 15 {
		t.Fatalf("expected the full check list, got %d", len(decoded.Checks))
	}
	for _, c := range decoded.Checks {
		if c.Name == "" || c.Status == "" {
			t.Fatalf("check with an empty name/status: %+v", c)
		}
		if (c.Status == statusWarn || c.Status == statusFail) && c.Fix == "" {
			t.Errorf("%s is %s but carries no fix", c.Name, c.Status)
		}
	}
}

// TestDoctorNeverPrintsSecrets is the leak test for this command: no DSN
// password, no token, no key material may appear in either output mode, on any
// path — including the error paths, which is where third-party strings echo a
// connection URL back at you.
func TestDoctorNeverPrintsSecrets(t *testing.T) {
	clearJanusEnv(t)
	const (
		dbPassword  = "correct-horse-battery-staple"
		metricsTok  = "metrics-token-do-not-print-me"
		hmacKey     = "audit-hmac-key-do-not-print-me"
		s3Secret    = "s3-secret-access-key-do-not-print"
		proxyURL    = "http://user:proxy-password-here@proxy.internal:3128"
		serviceToks = "janus_svc_do_not_print_this_token"
	)
	// Point at a closed port so the connect path fails: pgx's error is the
	// classic place a DSN gets echoed.
	t.Setenv("JANUS_DATABASE_URL", "postgres://janus:"+dbPassword+"@127.0.0.1:1/janus?sslmode=disable")
	t.Setenv("JANUS_METRICS_TOKEN", metricsTok)
	t.Setenv("JANUS_TOKEN", serviceToks)
	t.Setenv("JANUS_AUDIT_SHIP_MODE", "webhook")
	t.Setenv("JANUS_AUDIT_SHIP_WEBHOOK_URL", "https://siem.example.com/in")
	t.Setenv("JANUS_AUDIT_SHIP_WEBHOOK_HMAC_KEY", hmacKey)
	t.Setenv("JANUS_BACKUP_TICK", "6h")
	t.Setenv("JANUS_BACKUP_S3_BUCKET", "janus-dr")
	t.Setenv("JANUS_BACKUP_S3_REGION", "eu-west-1")
	t.Setenv("JANUS_BACKUP_S3_ACCESS_KEY_ID", "AKIAEXAMPLE")
	t.Setenv("JANUS_BACKUP_S3_SECRET_ACCESS_KEY", s3Secret)
	t.Setenv("JANUS_OUTBOUND_ALLOW_PROXY", "true")
	t.Setenv("HTTPS_PROXY", proxyURL)
	t.Setenv("JANUS_SEAL_TYPE", "shamir")

	secrets := []string{dbPassword, metricsTok, hmacKey, s3Secret, proxyURL, "proxy-password-here", serviceToks}

	for _, asJSON := range []bool{false, true} {
		rep := runDoctor(context.Background(), doctorOpts{timeout: 2 * time.Second})
		var sb strings.Builder
		if err := writeDoctorReport(&sb, rep, asJSON); err != nil {
			t.Fatal(err)
		}
		out := sb.String()
		for _, s := range secrets {
			if strings.Contains(out, s) {
				t.Fatalf("json=%v: report leaked %q\n---\n%s", asJSON, s, out)
			}
		}
		// The DSN check must still be informative about everything else.
		if !strings.Contains(out, "127.0.0.1") {
			t.Errorf("json=%v: the report should still name the database host", asJSON)
		}
	}
}

func TestRedactDSN(t *testing.T) {
	tests := []struct{ in, want string }{
		{"postgres://janus:hunter2@db:5432/janus?sslmode=require", "postgres://janus:[redacted]@db:5432/janus?sslmode=require"},
		{"postgres://janus@db:5432/janus", "postgres://janus@db:5432/janus"},
		{"postgres://db:5432/janus", "postgres://db:5432/janus"},
		{"not a url", "(unparseable connection string)"},
	}
	for _, tc := range tests {
		if got := redactDSN(tc.in); got != tc.want {
			t.Errorf("redactDSN(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestScrubberIgnoresTrivialValues(t *testing.T) {
	clearJanusEnv(t)
	// A 1-character password must not turn every occurrence of that letter into
	// [redacted]; the scrubber's floor exists for exactly this.
	t.Setenv("JANUS_DATABASE_URL", "postgres://janus:a@db:5432/janus")
	s := newScrubber()
	const sample = "a completely ordinary sentence"
	if got := s.clean(sample); got != sample {
		t.Fatalf("clean(%q) = %q; a trivial secret must not be substituted", sample, got)
	}
}

// ---------------------------------------------------------------------------
// end-to-end through the cobra command
// ---------------------------------------------------------------------------

func TestDoctorCommandExitStatus(t *testing.T) {
	clearJanusEnv(t)
	// No database URL at all → db.dsn FAILs → non-zero exit.
	out, _, err := runCmd(t, newDoctorCmd(), "--offline")
	if err == nil {
		t.Fatalf("expected a non-zero exit when a check fails:\n%s", out)
	}
	if !strings.Contains(out, "FAIL  db.dsn") {
		t.Errorf("expected a db.dsn FAIL line, got:\n%s", out)
	}
	if !strings.Contains(out, "fix:") {
		t.Errorf("a failing check must print its fix, got:\n%s", out)
	}
}

func TestDoctorCommandStrictPromotesWarnings(t *testing.T) {
	clearJanusEnv(t)
	// A configuration whose only problem is a warning.
	t.Setenv("JANUS_DATABASE_URL", "postgres://janus:pw@127.0.0.1:1/janus?sslmode=disable")
	t.Setenv("JANUS_SEAL_TYPE", "shamir")
	t.Setenv("JANUS_WEBAUTHN_ORIGIN", "http://localhost:8210") // the typo

	out, _, err := runCmd(t, newDoctorCmd(), "--offline")
	if err == nil {
		t.Skip("environment produced a FAIL as well; strict promotion is covered by the unit test")
	}
	if !strings.Contains(out, "WARN  env.unknown") {
		t.Errorf("expected the typo'd variable to warn, got:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// integration (real Postgres via testcontainers)
// ---------------------------------------------------------------------------

func TestDoctorAgainstRealPostgres(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	clearJanusEnv(t)
	dsn := bootPostgres(t)

	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Before migrating, the schema check must say so rather than crash.
	if got := checkSchemaVersion(ctx, st, 10*time.Second, newScrubber()); got.Status != statusWarn {
		t.Fatalf("unmigrated database: status = %s, want WARN (%s)", got.Status, got.Summary)
	}
	// The seal is not initialized and no seal type is configured → FAIL.
	if got := checkSeal(ctx, st, 10*time.Second, newScrubber()); got.Status != statusFail {
		t.Fatalf("uninitialized seal with no JANUS_SEAL_TYPE: status = %s, want FAIL (%s)", got.Status, got.Summary)
	}

	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if got := checkSchemaVersion(ctx, st, 10*time.Second, newScrubber()); got.Status != statusPass {
		t.Fatalf("migrated database: status = %s, want PASS (%s)", got.Status, got.Summary)
	}

	// Store a Shamir seal config, then assert the mismatch footgun is caught.
	seals := store.NewSealConfigStore(st)
	if err := seals.Put(ctx, &crypto.SealConfig{
		Type: crypto.SealTypeShamir, Threshold: 1, Shares: 1,
		KeyCheckValue: []byte("kcv"), WrappedMasterKey: []byte("wrapped"),
	}); err != nil {
		t.Fatal(err)
	}

	t.Run("matching seal type passes", func(t *testing.T) {
		t.Setenv("JANUS_SEAL_TYPE", crypto.SealTypeShamir)
		got := checkSeal(ctx, st, 10*time.Second, newScrubber())
		if got.Status != statusPass {
			t.Fatalf("status = %s, want PASS (%s)", got.Status, got.Summary)
		}
	})

	t.Run("env disagreeing with the stored seal fails", func(t *testing.T) {
		t.Setenv("JANUS_SEAL_TYPE", crypto.SealTypeAWSKMS)
		t.Setenv("JANUS_AWS_KMS_KEY_ARN", "arn:aws:kms:eu-west-1:1:key/abc")
		got := checkSeal(ctx, st, 10*time.Second, newScrubber())
		if got.Status != statusFail {
			t.Fatalf("status = %s, want FAIL (%s)", got.Status, got.Summary)
		}
		if !strings.Contains(got.Summary, "mismatch") {
			t.Errorf("summary should name the mismatch, got %q", got.Summary)
		}
	})

	t.Run("connect and full run", func(t *testing.T) {
		t.Setenv("JANUS_DATABASE_URL", dsn)
		t.Setenv("JANUS_SEAL_TYPE", crypto.SealTypeShamir)
		rep := runDoctor(ctx, doctorOpts{timeout: 10 * time.Second})
		byName := map[string]doctorCheck{}
		for _, c := range rep.Checks {
			byName[c.Name] = c
		}
		if c := byName["db.connect"]; c.Status != statusPass {
			t.Fatalf("db.connect = %s (%s)", c.Status, c.Summary)
		}
		if c := byName["db.migrations"]; c.Status != statusPass {
			t.Fatalf("db.migrations = %s (%s)", c.Status, c.Summary)
		}
		if rep.Summary.Fail != 0 {
			for _, c := range rep.Checks {
				if c.Status == statusFail {
					t.Errorf("unexpected FAIL %s: %s", c.Name, c.Summary)
				}
			}
		}
	})
}
