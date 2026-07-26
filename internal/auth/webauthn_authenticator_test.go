package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"testing"

	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
)

// This file is a minimal software authenticator: enough of a FIDO2 device to
// produce real attestation objects and real assertion signatures so the server
// side of the ceremony can be exercised end to end in Go. It exists only for
// tests — the production path never constructs authenticator data.
//
// What it CANNOT test is the browser half of WebAuthn: navigator.credentials
// .create()/.get(), the user-agent's own RP-ID/origin checks, the platform
// consent prompt, and the user-verification gesture. Those need a real browser
// (or a CDP virtual authenticator) and are called out as unverified in the PR.

// authenticator data flag bits (WebAuthn L3 §6.1).
const (
	flagUserPresent  byte = 0x01
	flagUserVerified byte = 0x04
	flagAttestedData byte = 0x40
)

// virtAuthenticator is a single-credential software authenticator.
type virtAuthenticator struct {
	key       *ecdsa.PrivateKey
	credID    []byte
	aaguid    []byte
	signCount uint32
}

func newVirtAuthenticator(t *testing.T) *virtAuthenticator {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	credID := make([]byte, 32)
	if _, err := rand.Read(credID); err != nil {
		t.Fatalf("credential id: %v", err)
	}
	return &virtAuthenticator{key: key, credID: credID, aaguid: make([]byte, 16), signCount: 1}
}

// coseKey encodes the public key as a COSE_Key ES256 map, the format an
// authenticator embeds in attested credential data.
func (a *virtAuthenticator) coseKey(t *testing.T) []byte {
	t.Helper()
	x := make([]byte, 32)
	y := make([]byte, 32)
	a.key.PublicKey.X.FillBytes(x)
	a.key.PublicKey.Y.FillBytes(y)
	// 1: kty=EC2(2), 3: alg=ES256(-7), -1: crv=P-256(1), -2: x, -3: y
	m := map[int]any{1: 2, 3: -7, -1: 1, -2: x, -3: y}
	b, err := webauthncbor.Marshal(m)
	if err != nil {
		t.Fatalf("cbor cose key: %v", err)
	}
	return b
}

// authData builds authenticator data for the given RP ID. When attested is true
// the attested-credential-data block (AAGUID + credential id + COSE key) is
// appended, as it is during registration.
func (a *virtAuthenticator) authData(t *testing.T, rpID string, flags byte, count uint32, attested bool) []byte {
	t.Helper()
	h := sha256.Sum256([]byte(rpID))
	out := make([]byte, 0, 128)
	out = append(out, h[:]...)
	if attested {
		flags |= flagAttestedData
	}
	out = append(out, flags)
	var c [4]byte
	binary.BigEndian.PutUint32(c[:], count)
	out = append(out, c[:]...)
	if attested {
		out = append(out, a.aaguid...)
		var l [2]byte
		binary.BigEndian.PutUint16(l[:], uint16(len(a.credID))) //nolint:gosec // fixed 32-byte test id
		out = append(out, l[:]...)
		out = append(out, a.credID...)
		out = append(out, a.coseKey(t)...)
	}
	return out
}

func clientDataJSON(t *testing.T, ceremony, challenge, origin string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"type": ceremony, "challenge": challenge, "origin": origin, "crossOrigin": false,
	})
	if err != nil {
		t.Fatalf("client data: %v", err)
	}
	return b
}

func b64u(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// attestationResponse produces the PublicKeyCredential JSON a browser would POST
// after navigator.credentials.create(). The attestation format is "none", which
// is what the server requests (no MDS is wired, so a signed statement would be
// unverifiable metadata).
func (a *virtAuthenticator) attestationResponse(t *testing.T, opts virtOpts) []byte {
	t.Helper()
	cd := clientDataJSON(t, "webauthn.create", opts.challenge, opts.origin)
	ad := a.authData(t, opts.rpID, opts.flags, a.signCount, true)
	att, err := webauthncbor.Marshal(map[string]any{
		"fmt": "none", "attStmt": map[string]any{}, "authData": ad,
	})
	if err != nil {
		t.Fatalf("cbor attestation: %v", err)
	}
	body, err := json.Marshal(map[string]any{
		"id": b64u(a.credID), "rawId": b64u(a.credID), "type": "public-key",
		"response": map[string]any{
			"clientDataJSON":    b64u(cd),
			"attestationObject": b64u(att),
		},
	})
	if err != nil {
		t.Fatalf("marshal attestation response: %v", err)
	}
	return body
}

// assertionResponse produces the PublicKeyCredential JSON a browser would POST
// after navigator.credentials.get(), signing over authData || SHA-256(clientData)
// exactly as a real authenticator does.
func (a *virtAuthenticator) assertionResponse(t *testing.T, opts virtOpts) []byte {
	t.Helper()
	cd := clientDataJSON(t, "webauthn.get", opts.challenge, opts.origin)
	// A real authenticator bumps its counter on every assertion. Tests that are
	// exercising clone detection override it explicitly.
	count := opts.signCount
	if count == nil {
		a.signCount++
		count = &a.signCount
	}
	ad := a.authData(t, opts.rpID, opts.flags, *count, false)
	sum := sha256.Sum256(cd)
	digest := sha256.Sum256(append(append([]byte{}, ad...), sum[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, a.key, digest[:])
	if err != nil {
		t.Fatalf("sign assertion: %v", err)
	}
	// The credential id presented may be overridden, so a test can express the
	// credential-substitution attack (a credential id belonging to one account
	// paired with another account's user handle).
	id := opts.rawID
	if id == nil {
		id = a.credID
	}
	body, err := json.Marshal(map[string]any{
		"id": b64u(id), "rawId": b64u(id), "type": "public-key",
		"response": map[string]any{
			"clientDataJSON":    b64u(cd),
			"authenticatorData": b64u(ad),
			"signature":         b64u(sig),
			"userHandle":        b64u(opts.userHandle),
		},
	})
	if err != nil {
		t.Fatalf("marshal assertion response: %v", err)
	}
	return body
}

// virtOpts parameterises a ceremony response so tests can deliberately break one
// binding at a time (wrong origin, wrong RP ID, stale counter, missing UV).
type virtOpts struct {
	challenge  string
	origin     string
	rpID       string
	flags      byte
	userHandle []byte
	signCount  *uint32
	// rawID overrides the credential id presented (nil = the authenticator's
	// own). Only the discoverable-login tests need it.
	rawID []byte
}

// challengeFrom pulls the challenge out of a serialized
// PublicKeyCredentialCreationOptions / RequestOptions payload.
func challengeFrom(t *testing.T, opts json.RawMessage) string {
	t.Helper()
	var v struct {
		Challenge string `json:"challenge"`
	}
	if err := json.Unmarshal(opts, &v); err != nil {
		t.Fatalf("decode options: %v", err)
	}
	if v.Challenge == "" {
		t.Fatal("options carried no challenge")
	}
	return v.Challenge
}
