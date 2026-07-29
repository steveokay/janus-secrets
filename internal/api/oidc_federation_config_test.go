package api

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

func TestOIDCFederationConfigRBAC(t *testing.T) {
	ts, srv, adminEmail, adminPassword, _ := authStackFull(t)
	ctx := t.Context()
	owner := login(t, ts.URL, adminEmail, adminPassword)

	vid, vpw, err := srv.auth.CreateUser(ctx, "viewer@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if code := doAuthed(t, "PUT", ts.URL+"/v1/instance/members/"+vid, owner, "", `{"role":"viewer"}`, nil); code != 204 {
		t.Fatalf("grant viewer: %d", code)
	}
	viewer := login(t, ts.URL, "viewer@example.com", vpw)

	body := `{"issuer":"https://token.actions.githubusercontent.com","audience":"janus","enabled":true}`
	if code := doAuthed(t, "PUT", ts.URL+"/v1/sys/oidc/federation", owner, "", body, nil); code != 200 {
		t.Fatalf("owner PUT: %d", code)
	}
	if code := doAuthed(t, "PUT", ts.URL+"/v1/sys/oidc/federation", viewer, "", body, nil); code != 403 {
		t.Fatalf("viewer PUT: want 403, got %d", code)
	}
	var got struct {
		Audience string `json:"audience"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/sys/oidc/federation", owner, "", "", &got); code != 200 || got.Audience != "janus" {
		t.Fatalf("owner GET: %d %+v", code, got)
	}
	if code := doAuthed(t, "DELETE", ts.URL+"/v1/sys/oidc/federation", owner, "", "", nil); code != 204 {
		t.Fatalf("owner DELETE: %d", code)
	}

	// --- multi-issuer trust set (roadmap 7.3) ---
	type issuerRow struct {
		ID       string `json:"id"`
		Issuer   string `json:"issuer"`
		Audience string `json:"audience"`
		Preset   string `json:"preset"`
	}
	const issuersPath = "/v1/sys/oidc/federation/issuers"
	gh := `{"issuer":"https://token.actions.githubusercontent.com","audience":"janus","preset":"github","enabled":true}`
	k8s := `{"issuer":"https://oidc.eks.eu-west-1.amazonaws.com/id/EXAMPLE","audience":"janus","preset":"kubernetes","enabled":true}`
	if code := doAuthed(t, "POST", ts.URL+issuersPath, viewer, "", k8s, nil); code != 403 {
		t.Fatalf("viewer POST issuer: want 403, got %d", code)
	}
	for _, body := range []string{gh, k8s} {
		if code := doAuthed(t, "POST", ts.URL+issuersPath, owner, "", body, nil); code != 200 {
			t.Fatalf("owner POST issuer: %d", code)
		}
	}
	// Bad preset and missing issuer are rejected.
	if code := doAuthed(t, "POST", ts.URL+issuersPath, owner, "",
		`{"issuer":"https://x.example","audience":"janus","preset":"nope","enabled":true}`, nil); code != 400 {
		t.Fatalf("bad preset: want 400, got %d", code)
	}
	if code := doAuthed(t, "POST", ts.URL+issuersPath, owner, "",
		`{"issuer":"","audience":"janus","enabled":true}`, nil); code != 400 {
		t.Fatalf("empty issuer: want 400, got %d", code)
	}
	var list []issuerRow
	if code := doAuthed(t, "GET", ts.URL+issuersPath, owner, "", "", &list); code != 200 || len(list) != 2 {
		t.Fatalf("list issuers: %d len=%d", code, len(list))
	}
	// The legacy single-issuer PUT refuses to silently drop the other issuer.
	if code := doAuthed(t, "PUT", ts.URL+"/v1/sys/oidc/federation", owner, "", body, nil); code != 409 {
		t.Fatalf("legacy PUT with two issuers: want 409, got %d", code)
	}
	if code := doAuthed(t, "DELETE", ts.URL+issuersPath+"/"+list[0].ID, viewer, "", "", nil); code != 403 {
		t.Fatalf("viewer DELETE issuer: want 403, got %d", code)
	}
	if code := doAuthed(t, "DELETE", ts.URL+issuersPath+"/"+list[0].ID, owner, "", "", nil); code != 204 {
		t.Fatalf("owner DELETE issuer: %d", code)
	}
	if code := doAuthed(t, "DELETE", ts.URL+issuersPath+"/"+list[0].ID, owner, "", "", nil); code != 404 {
		t.Fatalf("second DELETE issuer: want 404, got %d", code)
	}
}

// testCAPEM returns a throwaway self-signed CA, PEM-encoded — a well-formed
// bundle for the API-boundary tests (what it signs is irrelevant here; the
// verification behaviour is proven in internal/auth).
func testCAPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "janus-api-test-ca"},
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

// TestOIDCFederationIssuerCACert covers the ca_cert field at the API boundary: a
// malformed bundle is a 400 at WRITE time (not a silent federation_denied on the
// first exchange), a well-formed one round-trips, and an empty one clears it.
func TestOIDCFederationIssuerCACert(t *testing.T) {
	ts, _, adminEmail, adminPassword, _ := authStackFull(t)
	owner := login(t, ts.URL, adminEmail, adminPassword)
	const issuersPath = "/v1/sys/oidc/federation/issuers"
	const issuer = "https://kubernetes.default.svc"

	body := func(caPEM string) string {
		b, err := json.Marshal(map[string]any{
			"issuer": issuer, "audience": "janus", "preset": "kubernetes",
			"ca_cert": caPEM, "enabled": true,
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	// A FRESH destination per read: ca_cert is omitempty, and decoding an array
	// into an already-populated slice reuses its elements, so a stale value would
	// survive a response that omits the field.
	issuers := func() []struct {
		CACert string `json:"ca_cert"`
	} {
		t.Helper()
		var list []struct {
			CACert string `json:"ca_cert"`
		}
		if code := doAuthed(t, "GET", ts.URL+issuersPath, owner, "", "", &list); code != 200 {
			t.Fatalf("list issuers: %d", code)
		}
		return list
	}

	// Malformed PEM is rejected before anything is stored.
	if code := doAuthed(t, "POST", ts.URL+issuersPath, owner, "",
		body("-----BEGIN CERTIFICATE-----\nnot base64\n-----END CERTIFICATE-----\n"), nil); code != 400 {
		t.Fatalf("malformed ca_cert: want 400, got %d", code)
	}
	if list := issuers(); len(list) != 0 {
		t.Fatalf("rejected write must not persist: %+v", list)
	}

	ca := testCAPEM(t)
	var got struct {
		CACert string `json:"ca_cert"`
	}
	if code := doAuthed(t, "POST", ts.URL+issuersPath, owner, "", body(ca), &got); code != 200 {
		t.Fatalf("valid ca_cert: %d", code)
	}
	if got.CACert == "" {
		t.Fatal("ca_cert not returned by the write")
	}
	if list := issuers(); len(list) != 1 || list[0].CACert == "" {
		t.Fatalf("ca_cert not persisted: %+v", list)
	}

	// Clearing it must be expressible through the same endpoint.
	if code := doAuthed(t, "POST", ts.URL+issuersPath, owner, "", body(""), nil); code != 200 {
		t.Fatalf("clear ca_cert: %d", code)
	}
	if list := issuers(); len(list) != 1 || list[0].CACert != "" {
		t.Fatalf("ca_cert not cleared: %+v", list)
	}
}
