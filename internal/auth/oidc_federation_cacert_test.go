package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/steveokay/janus-secrets/internal/store"
)

// storeFedConfig is the repository-level row for an issuer, used to change a
// stored bundle WITHOUT going through the service methods that flush the
// verifier cache.
func storeFedConfig(issuer, caPEM string) store.OIDCFederationConfig {
	return store.OIDCFederationConfig{
		Issuer: issuer, Audience: "janus", Preset: PresetKubernetes,
		CACert: caPEM, Enabled: true,
	}
}

// unrelatedCAPEM returns a freshly generated, self-signed CA that signs nothing
// in these tests. It stands for "well-formed PEM, wrong trust anchor" — the
// shape of an operator pasting the wrong bundle.
func unrelatedCAPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "janus-test-unrelated-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

// TestFederationCACertValidation asserts a bundle is judged at WRITE time. An
// operator who pastes the wrong thing must be told immediately; deferring the
// check to the first exchange turns a typo into an indistinguishable
// federation_denied on a workload's cold start.
func TestFederationCACertValidation(t *testing.T) {
	valid := unrelatedCAPEM(t)

	cases := []struct {
		name    string
		pem     string
		wantErr bool
	}{
		{"empty means system roots", "", false},
		{"whitespace only means system roots", "   \n\t ", false},
		{"a real certificate", valid, false},
		{"leading and trailing whitespace tolerated", "\n  " + valid + "  \n", false},
		{"two certificates concatenated", valid + unrelatedCAPEM(t), false},
		{"not PEM at all", "not a certificate", true},
		{"truncated PEM block", strings.SplitAfter(valid, "\n")[0] + "AAAA\n", true},
		{"PEM of the wrong type", string(pem.EncodeToMemory(&pem.Block{
			Type: "PRIVATE KEY", Bytes: []byte("nope"),
		})), true},
		{"base64 without PEM armour", "TUlJQmtqQ0NBVGVnQXdJQkFnSVFB", true},
		{"oversized bundle", strings.Repeat("A", maxFederationCACertBytes+1), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateFederationCACert(tc.pem)
			if tc.wantErr {
				if !errors.Is(err, ErrFederationCACertInvalid) {
					t.Fatalf("want ErrFederationCACertInvalid, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// TestFederationCACertRejectedAtWrite proves the write endpoints refuse a
// malformed bundle rather than storing it, on BOTH the multi-issuer path and the
// legacy single-issuer one.
func TestFederationCACertRejectedAtWrite(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	if _, err := svc.PutFederationIssuer(ctx, FederationIssuerInput{
		Issuer: issuerGitHubActions, Audience: "janus", Preset: PresetGitHub,
		CACert:  "-----BEGIN CERTIFICATE-----\nnot base64\n-----END CERTIFICATE-----\n",
		Enabled: true,
	}); !errors.Is(err, ErrFederationCACertInvalid) {
		t.Fatalf("PutFederationIssuer: want ErrFederationCACertInvalid, got %v", err)
	}
	if err := svc.SetFederationConfig(ctx, FederationConfigInput{
		Issuer: issuerGitHubActions, Audience: "janus", CACert: "garbage", Enabled: true,
	}); !errors.Is(err, ErrFederationCACertInvalid) {
		t.Fatalf("SetFederationConfig: want ErrFederationCACertInvalid, got %v", err)
	}
	// Nothing was stored by either rejected write.
	if list, err := svc.ListFederationIssuers(ctx); err != nil || len(list) != 0 {
		t.Fatalf("rejected writes must not persist: %v %+v", err, list)
	}
}

// TestFederationCACertVerifiesPrivateIssuer is the end-to-end proof: an issuer
// whose certificate chains to nothing in the system roots cannot be used, and
// becomes usable the moment its CA is supplied — with no other change.
func TestFederationCACertVerifiesPrivateIssuer(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	idp := newMockIdPTLS(t, "janus")
	_, configID := mkScope(t)

	put := func(t *testing.T, caPEM string) {
		t.Helper()
		if _, err := svc.PutFederationIssuer(ctx, FederationIssuerInput{
			Issuer: idp.srv.URL, Audience: "janus", Preset: PresetKubernetes,
			CACert: caPEM, Enabled: true,
		}); err != nil {
			t.Fatalf("put issuer: %v", err)
		}
	}

	put(t, "")
	if _, err := svc.CreateFederationBinding(ctx, FederationBindingInput{
		Name: "k8s", Issuer: idp.srv.URL,
		MatchClaims: map[string]string{
			"kubernetes.io.namespace":           "prod",
			"kubernetes.io.serviceaccount.name": "atlas-api",
		},
		ScopeKind: "config", ScopeID: configID, Access: "read", TTLSeconds: 900, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	token := idp.signClaims(t, map[string]any{
		"iss": idp.srv.URL, "aud": []string{"janus"},
		"sub": "system:serviceaccount:prod:atlas-api",
		"exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(),
		"kubernetes.io": map[string]any{
			"namespace":      "prod",
			"serviceaccount": map[string]any{"name": "atlas-api"},
		},
	})

	// 1. System roots only: discovery cannot verify the issuer's certificate.
	_, err := svc.FederateCILogin(ctx, token)
	if err == nil {
		t.Fatal("exchange succeeded against an issuer signed by an untrusted CA")
	}
	var verr *tls.CertificateVerificationError
	if !errors.As(err, &verr) {
		t.Fatalf("want a TLS certificate verification failure, got %T: %v", err, err)
	}

	// 2. The wrong CA is still a failure — supplying *a* bundle is not enough,
	//    it has to be the one that actually signed the issuer.
	put(t, unrelatedCAPEM(t))
	if _, err := svc.FederateCILogin(ctx, token); err == nil {
		t.Fatal("exchange succeeded with a CA that did not sign the issuer")
	}

	// 3. The issuer's own CA: the exchange completes and mints a scoped token.
	put(t, idp.caPEM(t))
	res, err := svc.FederateCILogin(ctx, token)
	if err != nil {
		t.Fatalf("exchange with the issuer's CA: %v", err)
	}
	if res.Binding != "k8s" || res.Token == "" {
		t.Fatalf("result: %+v", res)
	}
	if _, scope, err := svc.VerifyServiceToken(ctx, res.Token); err != nil || scope.ID != configID {
		t.Fatalf("verify minted: %v %+v", err, scope)
	}
}

// TestFederationCACertInvalidatesVerifierCache covers the failure mode that
// would make this feature useless in practice: a cached verifier holds the HTTP
// client — and therefore the trust anchors — it was built with, so a corrected
// bundle that does not reach the cache never takes effect.
//
// The row is changed through the REPOSITORY, deliberately bypassing the service
// methods that call invalidateFederationVerifier(), so what is under test is the
// cache's own identity comparison rather than the explicit flush. Both matter:
// the flush handles this process, the comparison handles a row changed by a
// restore, a second instance, or a hand-edit.
func TestFederationCACertInvalidatesVerifierCache(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	idp := newMockIdPTLS(t, "janus")

	if _, err := svc.PutFederationIssuer(ctx, FederationIssuerInput{
		Issuer: idp.srv.URL, Audience: "janus", Preset: PresetKubernetes,
		CACert: idp.caPEM(t), Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	// Cold build succeeds and populates the cache.
	first, err := svc.federationVerifierFor(ctx, idp.srv.URL)
	if err != nil {
		t.Fatalf("cold build: %v", err)
	}
	// A second call with nothing changed must be served from the cache — this is
	// what proves the next assertion is about the CA and not about caching being
	// broken outright.
	second, err := svc.federationVerifierFor(ctx, idp.srv.URL)
	if err != nil {
		t.Fatalf("cached build: %v", err)
	}
	if first != second {
		t.Fatal("verifier was rebuilt although nothing changed")
	}

	// Swap the bundle underneath the cache for one that does not sign the issuer.
	if _, err := svc.oidcFedConfig.Upsert(ctx, storeFedConfig(idp.srv.URL, unrelatedCAPEM(t))); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.federationVerifierFor(ctx, idp.srv.URL); err == nil {
		t.Fatal("stale verifier served after the CA bundle changed")
	}

	// Clearing the bundle must likewise take effect: back to the system roots,
	// which do not trust this issuer.
	if _, err := svc.oidcFedConfig.Upsert(ctx, storeFedConfig(idp.srv.URL, "")); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.federationVerifierFor(ctx, idp.srv.URL); err == nil {
		t.Fatal("stale verifier served after the CA bundle was cleared")
	}
}

// TestFederationMutationsFlushVerifierCache asserts every issuer-mutating
// service method drops the cache, so a correction applies on the next exchange
// in this process without waiting for the identity comparison to notice.
func TestFederationMutationsFlushVerifierCache(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	idp := newMockIdPTLS(t, "janus")
	ca := idp.caPEM(t)

	warm := func(t *testing.T) {
		t.Helper()
		if _, err := svc.federationVerifierFor(ctx, idp.srv.URL); err != nil {
			t.Fatalf("warm cache: %v", err)
		}
		svc.fedMu.Lock()
		defer svc.fedMu.Unlock()
		if len(svc.fedCache) == 0 {
			t.Fatal("cache did not populate")
		}
	}
	assertFlushed := func(t *testing.T, what string) {
		t.Helper()
		svc.fedMu.Lock()
		defer svc.fedMu.Unlock()
		if len(svc.fedCache) != 0 {
			t.Fatalf("%s left %d cached verifier(s)", what, len(svc.fedCache))
		}
	}

	issuer, err := svc.PutFederationIssuer(ctx, FederationIssuerInput{
		Issuer: idp.srv.URL, Audience: "janus", Preset: PresetKubernetes,
		CACert: ca, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	warm(t)
	if _, err := svc.PutFederationIssuer(ctx, FederationIssuerInput{
		Issuer: idp.srv.URL, Audience: "janus", Preset: PresetKubernetes,
		CACert: ca, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	assertFlushed(t, "PutFederationIssuer")

	warm(t)
	if err := svc.DeleteFederationIssuer(ctx, issuer.ID); err != nil {
		t.Fatal(err)
	}
	assertFlushed(t, "DeleteFederationIssuer")

	if err := svc.SetFederationConfig(ctx, FederationConfigInput{
		Issuer: idp.srv.URL, Audience: "janus", Preset: PresetKubernetes,
		CACert: ca, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	warm(t)
	if err := svc.SetFederationConfig(ctx, FederationConfigInput{
		Issuer: idp.srv.URL, Audience: "janus", Preset: PresetKubernetes,
		CACert: ca, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	assertFlushed(t, "SetFederationConfig")

	warm(t)
	if err := svc.DeleteFederationConfig(ctx); err != nil {
		t.Fatal(err)
	}
	assertFlushed(t, "DeleteFederationConfig")
}

// TestFederationCACertRoundTrip asserts the bundle survives storage and is
// returned in the view — an admin editing an issuer must not have to re-paste a
// bundle from memory, which is how a wrong one gets saved.
func TestFederationCACertRoundTrip(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	ca := unrelatedCAPEM(t)

	v, err := svc.PutFederationIssuer(ctx, FederationIssuerInput{
		Issuer: "https://kubernetes.default.svc", Audience: "janus",
		Preset: PresetKubernetes, CACert: ca, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if v.CACert != strings.TrimSpace(ca) {
		t.Fatalf("ca_cert not returned by the write: %q", v.CACert)
	}
	list, err := svc.ListFederationIssuers(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v len=%d", err, len(list))
	}
	if list[0].CACert != strings.TrimSpace(ca) {
		t.Fatalf("ca_cert not persisted: %q", list[0].CACert)
	}

	// Clearing it must be expressible: an empty bundle means the system roots,
	// and a "preserve on empty" rule would make the fallback unreachable.
	if _, err := svc.PutFederationIssuer(ctx, FederationIssuerInput{
		Issuer: "https://kubernetes.default.svc", Audience: "janus",
		Preset: PresetKubernetes, CACert: "", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	list, err = svc.ListFederationIssuers(ctx)
	if err != nil || len(list) != 1 || list[0].CACert != "" {
		t.Fatalf("ca_cert not cleared: %v %+v", err, list)
	}
}

// TestFederationHTTPClientNeverSkipsVerification pins the two properties that
// must never regress: the shared client is reused when no bundle is set (so
// public issuers keep one connection pool), and a per-issuer client is built
// with real verification — never InsecureSkipVerify — when one is.
func TestFederationHTTPClientNeverSkipsVerification(t *testing.T) {
	svc, _, _ := newTestService(t)

	shared, err := svc.federationHTTPClient("")
	if err != nil {
		t.Fatal(err)
	}
	if shared != svc.oidcHTTP {
		t.Fatal("no bundle must reuse the shared process client")
	}

	hc, err := svc.federationHTTPClient(unrelatedCAPEM(t))
	if err != nil {
		t.Fatal(err)
	}
	if hc == svc.oidcHTTP {
		t.Fatal("a bundle must NOT mutate the shared client's trust roots")
	}
	tr, ok := hc.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport is %T, cannot pin roots", hc.Transport)
	}
	if tr.TLSClientConfig == nil || tr.TLSClientConfig.RootCAs == nil {
		t.Fatal("per-issuer client did not pin the supplied roots")
	}
	if tr.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("InsecureSkipVerify must never be set")
	}
	if tr.TLSClientConfig.MinVersion < tls.VersionTLS12 {
		t.Fatalf("TLS floor regressed to %#04x", tr.TLSClientConfig.MinVersion)
	}
	// The SSRF guard must still be in place: a guarded client dials through a
	// Control function, so the dialer is never the zero value.
	if tr.DialContext == nil {
		t.Fatal("per-issuer client bypassed the nethard-guarded dialer")
	}
}
