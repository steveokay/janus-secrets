package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// mockIdP is a minimal OIDC provider for tests: discovery, JWKS, and a token
// endpoint that mints a signed ID token for scripted claims.
type mockIdP struct {
	srv        *httptest.Server
	key        *rsa.PrivateKey
	keyID      string
	clientID   string
	sub, email string
	emailVer   bool
	nonce      string
}

func newMockIdP(t *testing.T, clientID string) *mockIdP {
	return newMockIdPServer(t, clientID, false)
}

// newMockIdPTLS serves the same provider over HTTPS with httptest's self-signed
// certificate — the stand-in for an issuer whose certificate is signed by a
// private CA (a self-hosted Kubernetes API server). Nothing in the system roots
// signs it, so it can only be verified via a supplied ca_cert.
func newMockIdPTLS(t *testing.T, clientID string) *mockIdP {
	return newMockIdPServer(t, clientID, true)
}

func newMockIdPServer(t *testing.T, clientID string, useTLS bool) *mockIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	m := &mockIdP{key: key, keyID: "test-key-1", clientID: clientID}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		writeJSONT(w, map[string]any{
			"issuer":                                m.srv.URL,
			"authorization_endpoint":                m.srv.URL + "/authorize",
			"token_endpoint":                        m.srv.URL + "/token",
			"jwks_uri":                              m.srv.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		writeJSONT(w, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
			Key: key.Public(), KeyID: m.keyID, Algorithm: "RS256", Use: "sig",
		}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		writeJSONT(w, map[string]any{
			"access_token": "at", "token_type": "Bearer",
			"id_token": m.signIDToken(t),
		})
	})
	if useTLS {
		m.srv = httptest.NewTLSServer(mux)
	} else {
		m.srv = httptest.NewServer(mux)
	}
	t.Cleanup(m.srv.Close)
	return m
}

// caPEM returns the server's own certificate PEM-encoded. httptest's test
// certificate is self-signed with IsCA set, so it is its own trust anchor.
func (m *mockIdP) caPEM(t *testing.T) string {
	t.Helper()
	cert := m.srv.Certificate()
	if cert == nil {
		t.Fatal("mock IdP is not serving TLS")
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}))
}

func (m *mockIdP) signIDToken(t *testing.T) string {
	t.Helper()
	sig, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: m.key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", m.keyID))
	if err != nil {
		t.Fatal(err)
	}
	claims := map[string]any{
		"iss": m.srv.URL, "sub": m.sub, "aud": m.clientID,
		"exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(),
		"email": m.email, "email_verified": m.emailVer, "nonce": m.nonce,
	}
	raw, err := jwt.Signed(sig).Claims(claims).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// signClaims signs an arbitrary claim set (for CI-federation tests) and returns
// the compact JWT. Mirrors signIDToken but takes explicit claims.
func (m *mockIdP) signClaims(t *testing.T, claims map[string]any) string {
	t.Helper()
	sig, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: m.key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", m.keyID))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := jwt.Signed(sig).Claims(claims).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func writeJSONT(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
