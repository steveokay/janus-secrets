package api

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/steveokay/janus-secrets/internal/crypto"
)

// TestTLSConfig_Validate covers the paired-field and mutual-exclusion rules.
func TestTLSConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     TLSConfig
		wantErr bool
	}{
		{name: "empty is plain http", cfg: TLSConfig{}, wantErr: false},
		{name: "both static certs", cfg: TLSConfig{CertFile: "c.pem", KeyFile: "k.pem"}, wantErr: false},
		{name: "cert without key", cfg: TLSConfig{CertFile: "c.pem"}, wantErr: true},
		{name: "key without cert", cfg: TLSConfig{KeyFile: "k.pem"}, wantErr: true},
		{name: "acme only", cfg: TLSConfig{ACMEDomains: []string{"a.example"}}, wantErr: false},
		{
			name:    "static and acme mutually exclusive",
			cfg:     TLSConfig{CertFile: "c.pem", KeyFile: "k.pem", ACMEDomains: []string{"a.example"}},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() err=%v wantErr=%v", err, tt.wantErr)
			}
		})
	}
}

// TestTLSConfig_ModeHelpers exercises the small mode predicates.
func TestTLSConfig_ModeHelpers(t *testing.T) {
	static := TLSConfig{CertFile: "c", KeyFile: "k"}
	if !static.Enabled() || !static.IsStaticCerts() || static.IsACME() {
		t.Fatalf("static: enabled=%v staticCerts=%v acme=%v", static.Enabled(), static.IsStaticCerts(), static.IsACME())
	}
	acme := TLSConfig{ACMEDomains: []string{"a"}}
	if !acme.Enabled() || acme.IsStaticCerts() || !acme.IsACME() {
		t.Fatalf("acme: enabled=%v staticCerts=%v acme=%v", acme.Enabled(), acme.IsStaticCerts(), acme.IsACME())
	}
	off := TLSConfig{}
	if off.Enabled() || off.IsStaticCerts() || off.IsACME() {
		t.Fatalf("off should be all-false")
	}
}

// TestTLSConfig_ACMECacheDir asserts the default cache dir and override.
func TestTLSConfig_ACMECacheDir(t *testing.T) {
	if got := (TLSConfig{}).acmeCacheDir(); got != "./.janus-acme" {
		t.Fatalf("default cache dir = %q", got)
	}
	if got := (TLSConfig{ACMECache: "/var/janus"}).acmeCacheDir(); got != "/var/janus" {
		t.Fatalf("override cache dir = %q", got)
	}
}

// TestListenAndServe_TLSValidationFails asserts a misconfigured TLS setup
// aborts serving with a clear error (before opening any listener).
func TestListenAndServe_TLSValidationFails(t *testing.T) {
	seals := &memSealStore{}
	srv := New(
		Config{ListenAddr: "127.0.0.1:0", SealType: crypto.SealTypeShamir, TLS: TLSConfig{CertFile: "only-cert.pem"}},
		crypto.NewKeyring(), crypto.NewShamirUnsealer(seals, 0, 0), seals, nil, nil, nil, nil, nil,
		nil, nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	err := srv.ListenAndServe(context.Background())
	if err == nil {
		t.Fatal("expected a validation error, got nil")
	}
}

// TestListenAndServe_StaticCertHTTPS boots the real server on a self-signed
// cert and performs an end-to-end HTTPS request, asserting the negotiated
// connection is at least TLS 1.2.
func TestListenAndServe_StaticCertHTTPS(t *testing.T) {
	certPEM, keyPEM := selfSignedCert(t, "127.0.0.1")
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}

	// Pick a free port so the assembled https URL is deterministic.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	seals := &memSealStore{}
	srv := New(
		Config{
			ListenAddr: addr,
			SealType:   crypto.SealTypeShamir,
			TLS:        TLSConfig{CertFile: certPath, KeyFile: keyPath},
		},
		crypto.NewKeyring(), crypto.NewShamirUnsealer(seals, 0, 0), seals, nil, nil, nil, nil, nil,
		nil, nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.ListenAndServe(ctx) }()
	defer func() {
		cancel()
		<-done
	}()

	// Trust the self-signed cert.
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(certPEM) {
		t.Fatal("failed to add self-signed cert to pool")
	}
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		},
	}

	// Retry briefly while the listener comes up.
	var resp *http.Response
	deadline := time.Now().Add(3 * time.Second)
	for {
		resp, err = client.Get("https://" + addr + "/v1/sys/live")
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("HTTPS request never succeeded: %v", err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	defer resp.Body.Close()

	if resp.TLS == nil {
		t.Fatal("response carried no TLS connection state")
	}
	if resp.TLS.Version < tls.VersionTLS12 {
		t.Fatalf("negotiated TLS version %x is below TLS 1.2", resp.TLS.Version)
	}
}

// TestRedirectToHTTPS asserts the static-cert redirect helper builds a 301 to
// the https:// URL, stripping any :port and preserving path + query.
func TestRedirectToHTTPS(t *testing.T) {
	tests := []struct {
		name string
		host string
		uri  string
		want string
	}{
		{name: "host with port", host: "janus.example:80", uri: "/a/b?x=1", want: "https://janus.example/a/b?x=1"},
		{name: "host no port", host: "janus.example", uri: "/", want: "https://janus.example/"},
		{name: "query preserved", host: "h:8080", uri: "/p?q=v&r=2", want: "https://h/p?q=v&r=2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://"+tt.host+tt.uri, nil)
			req.Host = tt.host
			rec := httptest.NewRecorder()
			redirectToHTTPS(rec, req)
			if rec.Code != http.StatusMovedPermanently {
				t.Fatalf("status = %d, want 301", rec.Code)
			}
			if got := rec.Header().Get("Location"); got != tt.want {
				t.Fatalf("Location = %q, want %q", got, tt.want)
			}
		})
	}
}

// selfSignedCert generates an in-memory self-signed ECDSA cert/key for host,
// returning PEM blocks. Kept hermetic — no external CA, no ACME.
func selfSignedCert(t *testing.T, host string) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(host); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM
}
