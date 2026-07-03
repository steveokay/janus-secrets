# Repo Scaffold + Crypto Layer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bootstrap the Keyhaven repo and build the complete, fully-tested `internal/crypto` package: AES-256-GCM envelope-encryption primitives, key wrapping with AAD binding, an in-memory Keyring, and Shamir + AWS KMS unseal.

**Architecture:** `internal/crypto` is a pure, storage-blind library (spec: `docs/superpowers/specs/2026-07-02-scaffold-crypto-layer-design.md`). It exposes value types (`Ciphertext`), pure functions (`Encrypt`/`Decrypt`/`WrapKey`/`UnwrapKey`), a stateful `Keyring` (sealed/unsealed), and an `Unsealer` interface with two implementations. Persistence of seal metadata hides behind a tiny `SealConfigStore` interface with a file-based impl for now.

**Tech Stack:** Go (latest stable), stdlib `crypto/*` only for primitives, vendored HashiCorp `shamir` (MPL-2.0), `aws-sdk-go-v2/service/kms` (the single new dependency; KMS does its crypto server-side).

**Conventions used throughout:**
- Module path: `github.com/steveokay/janus-secrets`
- All commands run from the repo root. On Windows use Git Bash (the Bash tool).
- Coverage note: Go's cover tool measures **statement** coverage; the spec's "100% branch coverage" is enforced as 100% statement coverage on `internal/crypto` (not the vendored `internal/crypto/shamir` subpackage, which keeps its upstream tests). To make impossible error branches testable, the package uses two test-injection points: `randReader` (an `io.Reader` var) and `aeadForKey` (a func var). Tests override them and restore with `defer`.
- Zero plaintext key material in any error message. Every error is one of the exported sentinels or an error from the OS/SDK that cannot contain key bytes.

---

### Task 1: Repo scaffold

**Files:**
- Create: `go.mod`
- Create: `.gitignore`
- Create: `cmd/keyhaven/main.go`
- Create: `Makefile`
- Create: `docker-compose.yml`
- Create: `.github/workflows/ci.yml`

- [ ] **Step 1: Create `go.mod`**

```
module github.com/steveokay/janus-secrets

go 1.24
```

- [ ] **Step 2: Create `.gitignore`**

```
bin/
coverage.out
crypto.out
*.test
.env
node_modules/
dist/
.idea/
.vscode/
```

- [ ] **Step 3: Create `cmd/keyhaven/main.go`**

```go
package main

import (
	"fmt"
	"os"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println("keyhaven", version)
		return
	}
	fmt.Fprintln(os.Stderr, "keyhaven server not yet implemented; see CLAUDE.md build phases")
	os.Exit(1)
}
```

- [ ] **Step 4: Create `Makefile`**

Note: recipe lines must be indented with a TAB, not spaces.

```makefile
.PHONY: dev test lint build migrate cover

test:
	go test -race ./...

lint:
	go vet ./...

build:
	go build -o bin/keyhaven ./cmd/keyhaven

cover:
	go test -coverprofile=crypto.out ./internal/crypto
	go tool cover -func=crypto.out | tail -1

dev:
	@echo "make dev: not yet implemented (arrives with the API milestone)"; exit 1

migrate:
	@echo "make migrate: not yet implemented (arrives with the store milestone)"; exit 1
```

- [ ] **Step 5: Create `docker-compose.yml`**

```yaml
services:
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: keyhaven
      POSTGRES_PASSWORD: keyhaven-dev
      POSTGRES_DB: keyhaven
    ports:
      - "127.0.0.1:5432:5432"
    volumes:
      - pgdata:/var/lib/postgresql/data

volumes:
  pgdata:
```

- [ ] **Step 6: Create `.github/workflows/ci.yml`**

(The 100%-coverage gate is added in Task 9, once the package exists.)

```yaml
name: ci

on:
  push:
    branches: [main, 'milestone-*']
  pull_request:

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: stable
      - run: go build ./...
      - run: go vet ./...
      - run: go test -race ./...
      - name: govulncheck
        run: go run golang.org/x/vuln/cmd/govulncheck@latest ./...
      - name: gosec
        run: go run github.com/securego/gosec/v2/cmd/gosec@v2.20.0 -exclude-dir=internal/crypto/shamir ./...
```

- [ ] **Step 7: Verify it builds**

Run: `go build ./... && go vet ./...`
Expected: no output, exit 0.

Run: `go run ./cmd/keyhaven version`
Expected: `keyhaven dev`

- [ ] **Step 8: Commit**

```bash
git add go.mod .gitignore cmd/ Makefile docker-compose.yml .github/
git commit -m "chore: scaffold repo (module, entrypoint, Makefile, compose, CI)"
```

---

### Task 2: Errors + AEAD primitives (`aead.go`)

**Files:**
- Create: `internal/crypto/errors.go`
- Create: `internal/crypto/aead.go`
- Test: `internal/crypto/aead_test.go`
- Test: `internal/crypto/testhelpers_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/crypto/testhelpers_test.go` — shared helpers used by every test file in this package:

```go
package crypto

import (
	"bytes"
	"errors"
)

// testKey returns a deterministic 32-byte key filled with b.
func testKey(b byte) []byte { return bytes.Repeat([]byte{b}, KeySize) }

// failReader always errors. Used to force crypto/rand failure paths.
type failReader struct{}

func (failReader) Read([]byte) (int, error) { return 0, errors.New("simulated rand failure") }

// failAfterReader succeeds n times, then errors. Used to fail a later
// random read inside a multi-step operation (e.g. KCV creation after
// key generation succeeded).
type failAfterReader struct{ n int }

func (r *failAfterReader) Read(p []byte) (int, error) {
	if r.n <= 0 {
		return 0, errors.New("simulated rand failure")
	}
	r.n--
	for i := range p {
		p[i] = 0xAB
	}
	return len(p), nil
}
```

Create `internal/crypto/aead_test.go`:

```go
package crypto

import (
	"bytes"
	"crypto/cipher"
	"errors"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := testKey(0x42)
	tests := []struct {
		name      string
		plaintext []byte
		aad       []byte
	}{
		{"basic", []byte("secret value"), []byte("aad")},
		{"empty plaintext", nil, []byte("aad")},
		{"nil aad", []byte("secret"), nil},
		{"large plaintext", bytes.Repeat([]byte("x"), 1<<16), []byte("aad")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ct, err := Encrypt(key, tt.plaintext, tt.aad)
			if err != nil {
				t.Fatalf("Encrypt: %v", err)
			}
			if len(ct.Nonce) != NonceSize {
				t.Fatalf("nonce length = %d, want %d", len(ct.Nonce), NonceSize)
			}
			got, err := Decrypt(key, ct, tt.aad)
			if err != nil {
				t.Fatalf("Decrypt: %v", err)
			}
			if !bytes.Equal(got, tt.plaintext) {
				t.Fatal("round trip mismatch")
			}
		})
	}
}

func TestDecryptFailures(t *testing.T) {
	key := testKey(0x42)
	aad := []byte("aad")
	ct, err := Encrypt(key, []byte("payload"), aad)
	if err != nil {
		t.Fatal(err)
	}

	// flip returns a deep copy of ct with f applied.
	flip := func(f func(*Ciphertext)) Ciphertext {
		c := Ciphertext{
			KeyVersion: ct.KeyVersion,
			Nonce:      append([]byte(nil), ct.Nonce...),
			Data:       append([]byte(nil), ct.Data...),
		}
		f(&c)
		return c
	}

	tests := []struct {
		name    string
		key     []byte
		ct      Ciphertext
		aad     []byte
		wantErr error
	}{
		{"wrong key", testKey(0x43), ct, aad, ErrDecryptFailed},
		{"wrong aad", key, ct, []byte("other"), ErrDecryptFailed},
		{"nonce bit flip", key, flip(func(c *Ciphertext) { c.Nonce[0] ^= 1 }), aad, ErrDecryptFailed},
		{"body bit flip", key, flip(func(c *Ciphertext) { c.Data[0] ^= 1 }), aad, ErrDecryptFailed},
		{"tag bit flip", key, flip(func(c *Ciphertext) { c.Data[len(c.Data)-1] ^= 1 }), aad, ErrDecryptFailed},
		{"truncated data", key, flip(func(c *Ciphertext) { c.Data = c.Data[:8] }), aad, ErrDecryptFailed},
		{"empty data", key, flip(func(c *Ciphertext) { c.Data = nil }), aad, ErrDecryptFailed},
		{"bad nonce length", key, flip(func(c *Ciphertext) { c.Nonce = c.Nonce[:4] }), aad, ErrDecryptFailed},
		{"bad key size", testKey(0x42)[:16], ct, aad, ErrInvalidKeySize},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Decrypt(tt.key, tt.ct, tt.aad); !errors.Is(err, tt.wantErr) {
				t.Fatalf("got %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestEncryptFailures(t *testing.T) {
	if _, err := Encrypt(make([]byte, 16), []byte("p"), nil); !errors.Is(err, ErrInvalidKeySize) {
		t.Fatalf("short key: got %v, want ErrInvalidKeySize", err)
	}

	restore := randReader
	randReader = failReader{}
	defer func() { randReader = restore }()
	if _, err := Encrypt(testKey(0x42), []byte("p"), nil); err == nil {
		t.Fatal("rand failure: want error, got nil")
	}
}

func TestAEADConstructorFailure(t *testing.T) {
	restore := aeadForKey
	aeadForKey = func([]byte) (cipher.AEAD, error) { return nil, errors.New("boom") }
	defer func() { aeadForKey = restore }()

	if _, err := Encrypt(testKey(0x42), []byte("p"), nil); !errors.Is(err, ErrInvalidKeySize) {
		t.Fatalf("Encrypt: got %v, want ErrInvalidKeySize", err)
	}
	if _, err := Decrypt(testKey(0x42), Ciphertext{Nonce: make([]byte, NonceSize), Data: []byte("x")}, nil); !errors.Is(err, ErrDecryptFailed) {
		t.Fatalf("Decrypt: got %v, want ErrDecryptFailed", err)
	}
}

func TestNonceUniqueness(t *testing.T) {
	const n = 100_000
	key := testKey(0x42)
	seen := make(map[[NonceSize]byte]struct{}, n)
	for i := 0; i < n; i++ {
		ct, err := Encrypt(key, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		var nonce [NonceSize]byte
		copy(nonce[:], ct.Nonce)
		if _, dup := seen[nonce]; dup {
			t.Fatal("nonce collision detected")
		}
		seen[nonce] = struct{}{}
	}
}

func TestSamePlaintextDifferentCiphertext(t *testing.T) {
	key := testKey(0x42)
	a, err := Encrypt(key, []byte("same"), nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Encrypt(key, []byte("same"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a.Data, b.Data) {
		t.Fatal("two encryptions of identical plaintext produced identical ciphertext")
	}
}

func TestCiphertextMarshalRoundTrip(t *testing.T) {
	key := testKey(0x42)
	ct, err := Encrypt(key, []byte("payload"), []byte("aad"))
	if err != nil {
		t.Fatal(err)
	}
	ct.KeyVersion = 7

	parsed, err := ParseCiphertext(ct.Marshal())
	if err != nil {
		t.Fatal(err)
	}
	if parsed.KeyVersion != 7 {
		t.Fatalf("KeyVersion = %d, want 7", parsed.KeyVersion)
	}
	if !bytes.Equal(parsed.Nonce, ct.Nonce) || !bytes.Equal(parsed.Data, ct.Data) {
		t.Fatal("marshal round trip mismatch")
	}
	got, err := Decrypt(key, parsed, []byte("aad"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte("payload")) {
		t.Fatal("decrypt after parse mismatch")
	}
}

func TestParseCiphertextFailures(t *testing.T) {
	key := testKey(0x42)
	ct, err := Encrypt(key, []byte("payload"), nil)
	if err != nil {
		t.Fatal(err)
	}
	valid := ct.Marshal()

	badVersion := append([]byte(nil), valid...)
	badVersion[0] = 0xFF

	tests := []struct {
		name string
		in   []byte
	}{
		{"nil", nil},
		{"empty", []byte{}},
		{"too short", valid[:10]},
		{"unknown format version", badVersion},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseCiphertext(tt.in); !errors.Is(err, ErrDecryptFailed) {
				t.Fatalf("got %v, want ErrDecryptFailed", err)
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/crypto/`
Expected: compile error (`Encrypt`, `KeySize`, etc. undefined).

- [ ] **Step 3: Write the implementation**

Create `internal/crypto/errors.go`:

```go
// Package crypto implements Keyhaven's envelope encryption: AES-256-GCM
// primitives, key wrapping with AAD binding, the in-memory keyring, and
// the Shamir and AWS KMS unseal mechanisms.
//
// Error discipline: no key material, plaintext, or share bytes ever appear
// in an error message. Callers get one of the sentinel errors below.
package crypto

import "errors"

var (
	ErrSealed             = errors.New("keyring is sealed")
	ErrAlreadyUnsealed    = errors.New("keyring is already unsealed")
	ErrInvalidKeySize     = errors.New("invalid key size")
	ErrDecryptFailed      = errors.New("decryption failed")
	ErrInvalidShare       = errors.New("invalid share")
	ErrDuplicateShare     = errors.New("duplicate share")
	ErrNotEnoughShares    = errors.New("not enough shares")
	ErrKeyCheckFailed     = errors.New("key check value mismatch")
	ErrNoSealConfig       = errors.New("seal configuration not found")
	ErrAlreadyInitialized = errors.New("seal already initialized")
	ErrInvalidSealConfig  = errors.New("seal configuration type mismatch")
)
```

Create `internal/crypto/aead.go`:

```go
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"io"
)

const (
	// KeySize is the size of all symmetric keys (AES-256).
	KeySize = 32
	// NonceSize is the standard GCM nonce size.
	NonceSize = 12
	// ciphertextFormatVersion is the serialization format version byte.
	ciphertextFormatVersion = 1
	// minMarshaledLen: format version (1) + key version (4) + nonce (12) + GCM tag (16).
	minMarshaledLen = 1 + 4 + NonceSize + 16
)

// Test injection points. Production code never reassigns these; tests
// override them to exercise otherwise-unreachable error branches.
var (
	randReader io.Reader = rand.Reader
	aeadForKey           = newAEAD
)

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Ciphertext is an AES-256-GCM ciphertext with its nonce and the version of
// the key that encrypted it. Data includes the GCM tag.
type Ciphertext struct {
	KeyVersion uint32
	Nonce      []byte
	Data       []byte
}

// Encrypt seals plaintext under a 32-byte key with a fresh random nonce.
// aad is authenticated but not encrypted; Decrypt must present the same aad.
func Encrypt(key, plaintext, aad []byte) (Ciphertext, error) {
	if len(key) != KeySize {
		return Ciphertext{}, ErrInvalidKeySize
	}
	aead, err := aeadForKey(key)
	if err != nil {
		return Ciphertext{}, ErrInvalidKeySize
	}
	nonce := make([]byte, NonceSize)
	if _, err := io.ReadFull(randReader, nonce); err != nil {
		return Ciphertext{}, err
	}
	return Ciphertext{
		Nonce: nonce,
		Data:  aead.Seal(nil, nonce, plaintext, aad),
	}, nil
}

// Decrypt opens ct. Any failure — wrong key, wrong aad, tampering,
// malformed input — is reported as ErrDecryptFailed with no detail.
func Decrypt(key []byte, ct Ciphertext, aad []byte) ([]byte, error) {
	if len(key) != KeySize {
		return nil, ErrInvalidKeySize
	}
	if len(ct.Nonce) != NonceSize {
		return nil, ErrDecryptFailed
	}
	aead, err := aeadForKey(key)
	if err != nil {
		return nil, ErrDecryptFailed
	}
	plaintext, err := aead.Open(nil, ct.Nonce, ct.Data, aad)
	if err != nil {
		return nil, ErrDecryptFailed
	}
	return plaintext, nil
}

// Marshal encodes ct as: formatVersion(1) | keyVersion(4, big-endian) | nonce(12) | data.
func (c Ciphertext) Marshal() []byte {
	out := make([]byte, 0, 1+4+len(c.Nonce)+len(c.Data))
	out = append(out, ciphertextFormatVersion)
	out = binary.BigEndian.AppendUint32(out, c.KeyVersion)
	out = append(out, c.Nonce...)
	out = append(out, c.Data...)
	return out
}

// ParseCiphertext decodes a blob produced by Marshal. Malformed input is
// reported as ErrDecryptFailed (fail closed, no detail).
func ParseCiphertext(b []byte) (Ciphertext, error) {
	if len(b) < minMarshaledLen {
		return Ciphertext{}, ErrDecryptFailed
	}
	if b[0] != ciphertextFormatVersion {
		return Ciphertext{}, ErrDecryptFailed
	}
	return Ciphertext{
		KeyVersion: binary.BigEndian.Uint32(b[1:5]),
		Nonce:      append([]byte(nil), b[5:5+NonceSize]...),
		Data:       append([]byte(nil), b[5+NonceSize:]...),
	}, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -race ./internal/crypto/`
Expected: PASS (TestNonceUniqueness takes a moment; that's fine).

- [ ] **Step 5: Commit**

```bash
git add internal/crypto/
git commit -m "feat(crypto): AES-256-GCM primitives, ciphertext serialization, error sentinels"
```

---

### Task 3: Key generation and wrapping (`keys.go`)

**Files:**
- Create: `internal/crypto/keys.go`
- Test: `internal/crypto/keys_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/crypto/keys_test.go`:

```go
package crypto

import (
	"bytes"
	"errors"
	"testing"
)

func TestGenerateKey(t *testing.T) {
	k1, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	k2, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if len(k1) != KeySize || len(k2) != KeySize {
		t.Fatalf("key sizes = %d, %d; want %d", len(k1), len(k2), KeySize)
	}
	if bytes.Equal(k1, k2) {
		t.Fatal("two generated keys are identical")
	}
}

func TestGenerateKeyRandFailure(t *testing.T) {
	restore := randReader
	randReader = failReader{}
	defer func() { randReader = restore }()
	if _, err := GenerateKey(); err == nil {
		t.Fatal("want error, got nil")
	}
}

func TestWrapUnwrapKey(t *testing.T) {
	wrapping := testKey(0x01)
	material := testKey(0x02)
	aad := ProjectKEKAAD("proj-123")

	wrapped, err := WrapKey(wrapping, material, aad)
	if err != nil {
		t.Fatal(err)
	}
	got, err := UnwrapKey(wrapping, wrapped, aad)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, material) {
		t.Fatal("unwrap mismatch")
	}
}

func TestWrapKeyRejectsBadMaterial(t *testing.T) {
	if _, err := WrapKey(testKey(0x01), []byte("short"), nil); !errors.Is(err, ErrInvalidKeySize) {
		t.Fatalf("got %v, want ErrInvalidKeySize", err)
	}
}

func TestUnwrapKeyFailures(t *testing.T) {
	wrapping := testKey(0x01)
	wrapped, err := WrapKey(wrapping, testKey(0x02), ProjectKEKAAD("proj-a"))
	if err != nil {
		t.Fatal(err)
	}

	// AAD binding: a KEK wrapped for project A must not unwrap as project B.
	if _, err := UnwrapKey(wrapping, wrapped, ProjectKEKAAD("proj-b")); !errors.Is(err, ErrDecryptFailed) {
		t.Fatalf("cross-project unwrap: got %v, want ErrDecryptFailed", err)
	}

	// A valid decryption that yields non-key-sized material is rejected.
	notAKey, err := Encrypt(wrapping, []byte("only 16 bytes!!!"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UnwrapKey(wrapping, notAKey, nil); !errors.Is(err, ErrDecryptFailed) {
		t.Fatalf("short unwrapped material: got %v, want ErrDecryptFailed", err)
	}
}

func TestAADHelpers(t *testing.T) {
	if bytes.Equal(ProjectKEKAAD("a"), ProjectKEKAAD("b")) {
		t.Fatal("ProjectKEKAAD not distinct per project")
	}
	a := DEKAAD("p1", "DB_URL", 1)
	tests := [][]byte{
		DEKAAD("p2", "DB_URL", 1),
		DEKAAD("p1", "API_KEY", 1),
		DEKAAD("p1", "DB_URL", 2),
	}
	for i, other := range tests {
		if bytes.Equal(a, other) {
			t.Fatalf("DEKAAD case %d not distinct", i)
		}
	}
}

// TestAADInjective guards against delimiter-ambiguity collisions: the AAD
// encoding must be injective even when the user-influenced fields contain
// the delimiter characters.
func TestAADInjective(t *testing.T) {
	dek := [][2][]byte{
		{DEKAAD("p1", "a:b", 1), DEKAAD("p1:a", "b", 1)},
		{DEKAAD("p", "x", 1), DEKAAD("p", "x:v1", 0)},
		{DEKAAD("a", "b", 1), DEKAAD("a:b", "", 1)},
	}
	for i, pair := range dek {
		if bytes.Equal(pair[0], pair[1]) {
			t.Fatalf("DEKAAD collision case %d: distinct locations share an AAD", i)
		}
	}
	if bytes.Equal(ProjectKEKAAD("a:b"), ProjectKEKAAD("a")) {
		t.Fatal("ProjectKEKAAD collision: distinct projects share an AAD")
	}
	if bytes.Equal(ProjectKEKAAD("p"), DEKAAD("p", "", 0)) {
		t.Fatal("KEK/DEK AAD families overlap")
	}
}

func TestZero(t *testing.T) {
	b := []byte{1, 2, 3}
	zero(b)
	if !bytes.Equal(b, []byte{0, 0, 0}) {
		t.Fatal("zero did not clear bytes")
	}
	zero(nil) // must not panic
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/crypto/`
Expected: compile error (`GenerateKey` undefined).

- [ ] **Step 3: Write the implementation**

Create `internal/crypto/keys.go`:

```go
package crypto

import (
	"encoding/binary"
	"io"
)

// GenerateKey returns 32 bytes of cryptographically random key material.
func GenerateKey() ([]byte, error) {
	key := make([]byte, KeySize)
	if _, err := io.ReadFull(randReader, key); err != nil {
		return nil, err
	}
	return key, nil
}

// WrapKey encrypts 32-byte key material under wrappingKey. aad binds the
// wrapped key to its storage location (see ProjectKEKAAD / DEKAAD).
func WrapKey(wrappingKey, keyMaterial, aad []byte) (Ciphertext, error) {
	if len(keyMaterial) != KeySize {
		return Ciphertext{}, ErrInvalidKeySize
	}
	return Encrypt(wrappingKey, keyMaterial, aad)
}

// UnwrapKey decrypts a wrapped key and verifies the result is key-sized.
func UnwrapKey(wrappingKey []byte, ct Ciphertext, aad []byte) ([]byte, error) {
	key, err := Decrypt(wrappingKey, ct, aad)
	if err != nil {
		return nil, err
	}
	if len(key) != KeySize {
		zero(key)
		return nil, ErrDecryptFailed
	}
	return key, nil
}

// AAD field encoding is length-prefixed so it is injective over
// user-influenced fields (project IDs / secret paths may contain ':').
func appendField(b []byte, field string) []byte {
	b = binary.BigEndian.AppendUint64(b, uint64(len(field)))
	return append(b, field...)
}

// ProjectKEKAAD binds a wrapped project KEK to its project. A KEK ciphertext
// copied onto another project's row will fail to unwrap.
func ProjectKEKAAD(projectID string) []byte {
	return appendField([]byte("keyhaven:kek:project"), projectID)
}

// DEKAAD binds a wrapped DEK to a project, secret path, and value version.
func DEKAAD(projectID, secretPath string, version uint64) []byte {
	b := []byte("keyhaven:dek")
	b = appendField(b, projectID)
	b = appendField(b, secretPath)
	return binary.BigEndian.AppendUint64(b, version)
}

// zero overwrites b with zeros. Best-effort in Go: the GC may have copied
// the bytes; this narrows the window, it does not guarantee erasure.
func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -race ./internal/crypto/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/crypto/
git commit -m "feat(crypto): key generation, wrap/unwrap with AAD binding"
```

---

### Task 4: Keyring (`keyring.go`)

**Files:**
- Create: `internal/crypto/keyring.go`
- Test: `internal/crypto/keyring_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/crypto/keyring_test.go`:

```go
package crypto

import (
	"bytes"
	"errors"
	"sync"
	"testing"
)

func TestKeyringSealedRejectsEverything(t *testing.T) {
	k := NewKeyring()
	if !k.Sealed() {
		t.Fatal("new keyring should be sealed")
	}
	if _, err := k.WrapProjectKEK(testKey(0x01), "p"); !errors.Is(err, ErrSealed) {
		t.Fatalf("WrapProjectKEK: got %v, want ErrSealed", err)
	}
	if _, err := k.UnwrapProjectKEK(Ciphertext{}, "p"); !errors.Is(err, ErrSealed) {
		t.Fatalf("UnwrapProjectKEK: got %v, want ErrSealed", err)
	}
	if _, _, err := k.NewDEK(testKey(0x01), nil); !errors.Is(err, ErrSealed) {
		t.Fatalf("NewDEK: got %v, want ErrSealed", err)
	}
}

func TestKeyringUnsealValidation(t *testing.T) {
	k := NewKeyring()
	if err := k.Unseal([]byte("short")); !errors.Is(err, ErrInvalidKeySize) {
		t.Fatalf("got %v, want ErrInvalidKeySize", err)
	}
	if err := k.Unseal(testKey(0xAA)); err != nil {
		t.Fatal(err)
	}
	if err := k.Unseal(testKey(0xAA)); !errors.Is(err, ErrAlreadyUnsealed) {
		t.Fatalf("double unseal: got %v, want ErrAlreadyUnsealed", err)
	}
}

func TestKeyringLifecycle(t *testing.T) {
	k := NewKeyring()
	master := testKey(0xAA)
	if err := k.Unseal(master); err != nil {
		t.Fatal(err)
	}
	if k.Sealed() {
		t.Fatal("keyring should be unsealed")
	}

	kek := testKey(0x0B)
	wrapped, err := k.WrapProjectKEK(kek, "proj-1")
	if err != nil {
		t.Fatal(err)
	}
	got, err := k.UnwrapProjectKEK(wrapped, "proj-1")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, kek) {
		t.Fatal("KEK round trip mismatch")
	}

	// AAD binding at the keyring level.
	if _, err := k.UnwrapProjectKEK(wrapped, "proj-2"); !errors.Is(err, ErrDecryptFailed) {
		t.Fatalf("cross-project: got %v, want ErrDecryptFailed", err)
	}

	dek, wrappedDEK, err := k.NewDEK(kek, DEKAAD("proj-1", "DB_URL", 1))
	if err != nil {
		t.Fatal(err)
	}
	if len(dek) != KeySize {
		t.Fatalf("DEK size = %d, want %d", len(dek), KeySize)
	}
	gotDEK, err := UnwrapKey(kek, wrappedDEK, DEKAAD("proj-1", "DB_URL", 1))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotDEK, dek) {
		t.Fatal("DEK round trip mismatch")
	}

	// Seal returns it to the sealed state; ops fail again.
	k.Seal()
	if !k.Sealed() {
		t.Fatal("keyring should be sealed after Seal")
	}
	if _, err := k.WrapProjectKEK(kek, "proj-1"); !errors.Is(err, ErrSealed) {
		t.Fatalf("after Seal: got %v, want ErrSealed", err)
	}

	// Seal/unseal cycle works.
	if err := k.Unseal(testKey(0xAA)); err != nil {
		t.Fatal(err)
	}
	if _, err := k.UnwrapProjectKEK(wrapped, "proj-1"); err != nil {
		t.Fatalf("after re-unseal: %v", err)
	}
}

func TestKeyringCopiesMasterKey(t *testing.T) {
	k := NewKeyring()
	master := testKey(0xAA)
	if err := k.Unseal(master); err != nil {
		t.Fatal(err)
	}
	wrapped, err := k.WrapProjectKEK(testKey(0x0B), "p")
	if err != nil {
		t.Fatal(err)
	}
	zero(master) // caller destroys its copy; keyring must still work
	if _, err := k.UnwrapProjectKEK(wrapped, "p"); err != nil {
		t.Fatalf("keyring shared caller's slice: %v", err)
	}
}

func TestKeyringNewDEKFailures(t *testing.T) {
	k := NewKeyring()
	if err := k.Unseal(testKey(0xAA)); err != nil {
		t.Fatal(err)
	}

	// Bad project KEK size surfaces from WrapKey.
	if _, _, err := k.NewDEK([]byte("short"), nil); !errors.Is(err, ErrInvalidKeySize) {
		t.Fatalf("got %v, want ErrInvalidKeySize", err)
	}

	// Rand failure during DEK generation.
	restore := randReader
	randReader = failReader{}
	defer func() { randReader = restore }()
	if _, _, err := k.NewDEK(testKey(0x0B), nil); err == nil {
		t.Fatal("want error, got nil")
	}
}

func TestKeyringConcurrent(t *testing.T) {
	k := NewKeyring()
	if err := k.Unseal(testKey(0xAA)); err != nil {
		t.Fatal(err)
	}
	kek := testKey(0x0B)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_, _ = k.WrapProjectKEK(kek, "p")
				_ = k.Sealed()
			}
		}()
	}
	wg.Wait()
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/crypto/`
Expected: compile error (`NewKeyring` undefined).

- [ ] **Step 3: Write the implementation**

Create `internal/crypto/keyring.go`:

```go
package crypto

import "sync"

// Keyring holds the master key in memory after unseal. It is the only
// stateful component in this package. All operations on a sealed keyring
// return ErrSealed (the API layer maps this to HTTP 503).
type Keyring struct {
	mu     sync.RWMutex
	master []byte // nil iff sealed
}

func NewKeyring() *Keyring { return &Keyring{} }

// Unseal installs the master key. The slice is copied; the caller should
// zero its copy afterwards.
func (k *Keyring) Unseal(master []byte) error {
	if len(master) != KeySize {
		return ErrInvalidKeySize
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.master != nil {
		return ErrAlreadyUnsealed
	}
	k.master = append([]byte(nil), master...)
	return nil
}

// Seal zeroizes the master key (best-effort) and returns to the sealed state.
func (k *Keyring) Seal() {
	k.mu.Lock()
	defer k.mu.Unlock()
	zero(k.master)
	k.master = nil
}

func (k *Keyring) Sealed() bool {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return k.master == nil
}

// WrapProjectKEK wraps a project KEK under the master key, bound to projectID.
func (k *Keyring) WrapProjectKEK(kek []byte, projectID string) (Ciphertext, error) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	if k.master == nil {
		return Ciphertext{}, ErrSealed
	}
	return WrapKey(k.master, kek, ProjectKEKAAD(projectID))
}

// UnwrapProjectKEK unwraps a project KEK previously wrapped for projectID.
func (k *Keyring) UnwrapProjectKEK(ct Ciphertext, projectID string) ([]byte, error) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	if k.master == nil {
		return nil, ErrSealed
	}
	return UnwrapKey(k.master, ct, ProjectKEKAAD(projectID))
}

// NewDEK generates a fresh DEK and wraps it under projectKEK in one call,
// minimizing the plaintext DEK's lifetime. Refuses to run while sealed.
func (k *Keyring) NewDEK(projectKEK, aad []byte) ([]byte, Ciphertext, error) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	if k.master == nil {
		return nil, Ciphertext{}, ErrSealed
	}
	dek, err := GenerateKey()
	if err != nil {
		return nil, Ciphertext{}, err
	}
	wrapped, err := WrapKey(projectKEK, dek, aad)
	if err != nil {
		zero(dek)
		return nil, Ciphertext{}, err
	}
	return dek, wrapped, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -race ./internal/crypto/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/crypto/
git commit -m "feat(crypto): in-memory keyring with sealed/unsealed state machine"
```

---

### Task 5: Vendor HashiCorp shamir

**Files:**
- Create: `internal/crypto/shamir/shamir.go` (vendored)
- Create: `internal/crypto/shamir/tables.go` (vendored)
- Create: `internal/crypto/shamir/shamir_test.go` (vendored)
- Create: `internal/crypto/shamir/LICENSE` (MPL-2.0)
- Create: `internal/crypto/shamir/README.md` (provenance)

- [ ] **Step 1: Download the vendored files (pinned tag)**

```bash
mkdir -p internal/crypto/shamir
curl -fsSL https://raw.githubusercontent.com/hashicorp/vault/v1.15.6/shamir/shamir.go -o internal/crypto/shamir/shamir.go
curl -fsSL https://raw.githubusercontent.com/hashicorp/vault/v1.15.6/shamir/tables.go -o internal/crypto/shamir/tables.go
curl -fsSL https://raw.githubusercontent.com/hashicorp/vault/v1.15.6/shamir/shamir_test.go -o internal/crypto/shamir/shamir_test.go
curl -fsSL https://raw.githubusercontent.com/hashicorp/vault/v1.15.6/LICENSE -o internal/crypto/shamir/LICENSE
```

If any URL 404s (file layout differs at that tag), list the directory via
`curl -fsSL https://api.github.com/repos/hashicorp/vault/contents/shamir?ref=v1.15.6`
and download whatever `.go` files exist there instead.

- [ ] **Step 2: Verify the vendored code is self-contained**

Run: `grep -n '"github.com' internal/crypto/shamir/*.go`
Expected: no matches (stdlib imports only). If any `github.com/hashicorp/*` import appears, STOP and report — do not improvise a replacement.

Run: `head -5 internal/crypto/shamir/shamir.go`
Expected: package `shamir` with HashiCorp copyright/SPDX header intact. Do not strip headers — MPL-2.0 requires them.

- [ ] **Step 3: Create `internal/crypto/shamir/README.md`**

```markdown
# Vendored: HashiCorp Vault shamir package

Source: https://github.com/hashicorp/vault/tree/v1.15.6/shamir
License: MPL-2.0 (see LICENSE in this directory; original headers retained)

Vendored per project policy (CLAUDE.md): no third-party crypto dependencies
in go.mod, and no hand-rolled crypto primitives. Do not modify these files
except to track upstream.
```

- [ ] **Step 4: Run the upstream tests**

Run: `go test ./internal/crypto/shamir/`
Expected: PASS

Run: `go vet ./internal/crypto/shamir/`
Expected: clean. (gosec is configured to skip this dir; upstream uses `math/rand` for x-coordinate shuffling, which is not a security issue here and stays as-is.)

- [ ] **Step 5: Commit**

```bash
git add internal/crypto/shamir/
git commit -m "feat(crypto): vendor HashiCorp shamir package (vault v1.15.6, MPL-2.0)"
```

---

### Task 6: Unsealer contract, key check value, seal-config store

**Files:**
- Create: `internal/crypto/unseal.go`
- Create: `internal/crypto/sealstore.go`
- Test: `internal/crypto/unseal_test.go`
- Test: `internal/crypto/sealstore_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/crypto/unseal_test.go`:

```go
package crypto

import (
	"errors"
	"testing"
)

func TestKCVRoundTrip(t *testing.T) {
	master := testKey(0xAA)
	kcv, err := makeKCV(master)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyKCV(master, kcv); err != nil {
		t.Fatal(err)
	}
}

func TestKCVFailures(t *testing.T) {
	master := testKey(0xAA)
	kcv, err := makeKCV(master)
	if err != nil {
		t.Fatal(err)
	}

	tampered := append([]byte(nil), kcv...)
	tampered[len(tampered)-1] ^= 1

	tests := []struct {
		name   string
		master []byte
		kcv    []byte
	}{
		{"wrong master", testKey(0xAB), kcv},
		{"tampered kcv", master, tampered},
		{"garbage kcv", master, []byte("not a ciphertext")},
		{"nil kcv", master, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := verifyKCV(tt.master, tt.kcv); !errors.Is(err, ErrKeyCheckFailed) {
				t.Fatalf("got %v, want ErrKeyCheckFailed", err)
			}
		})
	}
}

func TestMakeKCVRandFailure(t *testing.T) {
	restore := randReader
	randReader = failReader{}
	defer func() { randReader = restore }()
	if _, err := makeKCV(testKey(0xAA)); err == nil {
		t.Fatal("want error, got nil")
	}
}
```

Create `internal/crypto/sealstore_test.go`:

```go
package crypto

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFileSealConfigStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := &FileSealConfigStore{Path: filepath.Join(t.TempDir(), "seal.json")}

	if _, err := store.Get(ctx); !errors.Is(err, ErrNoSealConfig) {
		t.Fatalf("empty store: got %v, want ErrNoSealConfig", err)
	}

	cfg := &SealConfig{
		Type:          SealTypeShamir,
		Threshold:     3,
		Shares:        5,
		KeyCheckValue: []byte{1, 2, 3},
	}
	if err := store.Put(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != cfg.Type || got.Threshold != 3 || got.Shares != 5 || len(got.KeyCheckValue) != 3 {
		t.Fatalf("round trip mismatch: %+v", got)
	}

	// File must be private.
	info, err := os.Stat(store.Path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 && os.PathSeparator == '/' {
		t.Fatalf("seal config file is group/world accessible: %v", perm)
	}
}

func TestFileSealConfigStoreErrors(t *testing.T) {
	ctx := context.Background()

	// Get: path exists but is unreadable as a config file (it's a directory).
	dir := t.TempDir()
	store := &FileSealConfigStore{Path: dir}
	if _, err := store.Get(ctx); err == nil {
		t.Fatal("Get on directory: want error, got nil")
	}

	// Get: corrupted JSON.
	badPath := filepath.Join(t.TempDir(), "seal.json")
	if err := os.WriteFile(badPath, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (&FileSealConfigStore{Path: badPath}).Get(ctx); err == nil {
		t.Fatal("corrupt JSON: want error, got nil")
	}

	// Put: parent directory does not exist.
	missing := &FileSealConfigStore{Path: filepath.Join(t.TempDir(), "nope", "seal.json")}
	if err := missing.Put(ctx, &SealConfig{Type: SealTypeShamir}); err == nil {
		t.Fatal("missing parent dir: want error, got nil")
	}

	// Put: rename target is an existing directory.
	if err := store.Put(ctx, &SealConfig{Type: SealTypeShamir}); err == nil {
		t.Fatal("rename onto directory: want error, got nil")
	}

	// Put: marshal failure (injected).
	restore := marshalSealConfig
	marshalSealConfig = func(any) ([]byte, error) { return nil, errors.New("boom") }
	defer func() { marshalSealConfig = restore }()
	ok := &FileSealConfigStore{Path: filepath.Join(t.TempDir(), "seal.json")}
	if err := ok.Put(ctx, &SealConfig{Type: SealTypeShamir}); err == nil {
		t.Fatal("marshal failure: want error, got nil")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/crypto/`
Expected: compile error (`makeKCV`, `SealConfig`, etc. undefined).

- [ ] **Step 3: Write the implementation**

Create `internal/crypto/unseal.go`:

```go
package crypto

import (
	"context"
	"crypto/subtle"
)

// Seal types recorded in SealConfig.
const (
	SealTypeShamir = "shamir"
	SealTypeAWSKMS = "awskms"
)

// Unsealer recovers the master key at startup. Implementations: Shamir
// (interactive share submission via the concrete *ShamirUnsealer) and
// AWS KMS (fully automatic).
type Unsealer interface {
	// Init generates a new master key on first boot and persists seal
	// metadata. Returns ErrAlreadyInitialized if seal config already exists.
	Init(ctx context.Context) (*InitResult, error)
	// Unseal recovers and verifies the master key. The caller feeds it to
	// Keyring.Unseal and then zeroes the returned slice.
	Unseal(ctx context.Context) ([]byte, error)
}

// InitResult is what Init hands back to the operator exactly once.
type InitResult struct {
	// Shares are the Shamir key shares. Nil for KMS-based seals.
	Shares [][]byte
}

// SealConfig is the persisted, non-secret seal metadata. The key check
// value and wrapped master key are ciphertexts, safe at rest.
type SealConfig struct {
	Type             string `json:"type"`
	Threshold        int    `json:"threshold,omitempty"`
	Shares           int    `json:"shares,omitempty"`
	KeyCheckValue    []byte `json:"key_check_value"`
	WrappedMasterKey []byte `json:"wrapped_master_key,omitempty"`
}

// SealConfigStore persists SealConfig. Get returns ErrNoSealConfig when
// nothing has been initialized yet.
type SealConfigStore interface {
	Get(ctx context.Context) (*SealConfig, error)
	Put(ctx context.Context, cfg *SealConfig) error
}

// The key check value is a known constant encrypted under the master key at
// Init. Verifying it on unseal rejects a wrong-but-well-formed master key
// (e.g. a Shamir reconstruction from a wrong share) before it is used.
var (
	kcvPlaintext = []byte("keyhaven-key-check-v1")
	kcvAAD       = []byte("keyhaven:kcv")
)

func makeKCV(master []byte) ([]byte, error) {
	ct, err := Encrypt(master, kcvPlaintext, kcvAAD)
	if err != nil {
		return nil, err
	}
	return ct.Marshal(), nil
}

func verifyKCV(master, kcv []byte) error {
	ct, err := ParseCiphertext(kcv)
	if err != nil {
		return ErrKeyCheckFailed
	}
	got, err := Decrypt(master, ct, kcvAAD)
	if err != nil {
		return ErrKeyCheckFailed
	}
	if subtle.ConstantTimeCompare(got, kcvPlaintext) != 1 {
		return ErrKeyCheckFailed
	}
	return nil
}
```

Create `internal/crypto/sealstore.go`:

```go
package crypto

import (
	"context"
	"encoding/json"
	"errors"
	"os"
)

// marshalSealConfig is a test injection point (json.Marshal cannot fail on
// SealConfig in practice, but the branch must be coverable).
var marshalSealConfig = json.Marshal

// FileSealConfigStore persists seal config as a private JSON file. Used for
// tests and single-binary bootstrap; a Postgres-backed implementation
// arrives with the store milestone.
type FileSealConfigStore struct {
	Path string
}

func (f *FileSealConfigStore) Get(_ context.Context) (*SealConfig, error) {
	// #nosec G304 -- Path is operator-supplied server configuration, not user input.
	b, err := os.ReadFile(f.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoSealConfig
	}
	if err != nil {
		return nil, err
	}
	var cfg SealConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (f *FileSealConfigStore) Put(_ context.Context, cfg *SealConfig) error {
	b, err := marshalSealConfig(cfg)
	if err != nil {
		return err
	}
	tmp := f.Path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, f.Path)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -race ./internal/crypto/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/crypto/
git commit -m "feat(crypto): unsealer contract, key check value, file seal-config store"
```

---

### Task 7: Shamir unsealer (`shamir.go`)

**Files:**
- Create: `internal/crypto/shamir.go`
- Test: `internal/crypto/shamir_unsealer_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/crypto/shamir_unsealer_test.go`:

```go
package crypto

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

// stubStore lets tests inject store failures.
type stubStore struct {
	cfg    *SealConfig
	getErr error
	putErr error
}

func (s *stubStore) Get(context.Context) (*SealConfig, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	if s.cfg == nil {
		return nil, ErrNoSealConfig
	}
	return s.cfg, nil
}

func (s *stubStore) Put(_ context.Context, cfg *SealConfig) error {
	if s.putErr != nil {
		return s.putErr
	}
	s.cfg = cfg
	return nil
}

func fileStore(t *testing.T) *FileSealConfigStore {
	t.Helper()
	return &FileSealConfigStore{Path: filepath.Join(t.TempDir(), "seal.json")}
}

func TestShamirInitAndUnseal(t *testing.T) {
	ctx := context.Background()
	store := fileStore(t)
	u := NewShamirUnsealer(store, 5, 3)

	res, err := u.Init(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Shares) != 5 {
		t.Fatalf("shares = %d, want 5", len(res.Shares))
	}
	cfg, err := store.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Type != SealTypeShamir || cfg.Threshold != 3 || cfg.Shares != 5 {
		t.Fatalf("persisted config: %+v", cfg)
	}

	// Unseal with shares 0, 2, 4 (any k of n works).
	u2 := NewShamirUnsealer(store, 0, 0)
	for _, i := range []int{0, 2, 4} {
		p, err := u2.SubmitShare(ctx, res.Shares[i])
		if err != nil {
			t.Fatal(err)
		}
		if p.Required != 3 {
			t.Fatalf("Required = %d, want 3", p.Required)
		}
	}
	master, err := u2.Unseal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(master) != KeySize {
		t.Fatalf("master size = %d, want %d", len(master), KeySize)
	}

	// The recovered master key actually drives a keyring.
	k := NewKeyring()
	if err := k.Unseal(master); err != nil {
		t.Fatal(err)
	}
	if _, err := k.WrapProjectKEK(testKey(0x0B), "p"); err != nil {
		t.Fatal(err)
	}
}

func TestShamirDefaults(t *testing.T) {
	ctx := context.Background()
	u := NewShamirUnsealer(fileStore(t), 0, 0)
	res, err := u.Init(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Shares) != DefaultShamirShares {
		t.Fatalf("shares = %d, want %d", len(res.Shares), DefaultShamirShares)
	}
}

func TestShamirInitFailures(t *testing.T) {
	ctx := context.Background()

	t.Run("already initialized", func(t *testing.T) {
		store := fileStore(t)
		u := NewShamirUnsealer(store, 5, 3)
		if _, err := u.Init(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := u.Init(ctx); !errors.Is(err, ErrAlreadyInitialized) {
			t.Fatalf("got %v, want ErrAlreadyInitialized", err)
		}
	})

	t.Run("store get error propagates", func(t *testing.T) {
		u := NewShamirUnsealer(&stubStore{getErr: errors.New("db down")}, 5, 3)
		if _, err := u.Init(ctx); err == nil {
			t.Fatal("want error, got nil")
		}
	})

	t.Run("invalid params rejected by split", func(t *testing.T) {
		u := NewShamirUnsealer(fileStore(t), 2, 3) // shares < threshold
		if _, err := u.Init(ctx); err == nil {
			t.Fatal("want error, got nil")
		}
	})

	t.Run("rand failure", func(t *testing.T) {
		restore := randReader
		randReader = failReader{}
		defer func() { randReader = restore }()
		u := NewShamirUnsealer(fileStore(t), 5, 3)
		if _, err := u.Init(ctx); err == nil {
			t.Fatal("want error, got nil")
		}
	})

	t.Run("kcv rand failure after keygen", func(t *testing.T) {
		restore := randReader
		randReader = &failAfterReader{n: 1} // keygen read succeeds, KCV nonce read fails
		defer func() { randReader = restore }()
		u := NewShamirUnsealer(fileStore(t), 5, 3)
		if _, err := u.Init(ctx); err == nil {
			t.Fatal("want error, got nil")
		}
	})

	t.Run("store put error propagates", func(t *testing.T) {
		u := NewShamirUnsealer(&stubStore{putErr: errors.New("disk full")}, 5, 3)
		if _, err := u.Init(ctx); err == nil {
			t.Fatal("want error, got nil")
		}
	})
}

func TestShamirSubmitShareFailures(t *testing.T) {
	ctx := context.Background()

	t.Run("before init", func(t *testing.T) {
		u := NewShamirUnsealer(fileStore(t), 5, 3)
		if _, err := u.SubmitShare(ctx, []byte{1, 2, 3}); !errors.Is(err, ErrNoSealConfig) {
			t.Fatalf("got %v, want ErrNoSealConfig", err)
		}
	})

	t.Run("wrong seal type", func(t *testing.T) {
		store := &stubStore{cfg: &SealConfig{Type: SealTypeAWSKMS}}
		u := NewShamirUnsealer(store, 5, 3)
		if _, err := u.SubmitShare(ctx, []byte{1, 2, 3}); !errors.Is(err, ErrInvalidSealConfig) {
			t.Fatalf("got %v, want ErrInvalidSealConfig", err)
		}
	})

	store := fileStore(t)
	u := NewShamirUnsealer(store, 5, 3)
	res, err := u.Init(ctx)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("too short", func(t *testing.T) {
		if _, err := u.SubmitShare(ctx, []byte{1}); !errors.Is(err, ErrInvalidShare) {
			t.Fatalf("got %v, want ErrInvalidShare", err)
		}
	})

	t.Run("duplicate", func(t *testing.T) {
		if _, err := u.SubmitShare(ctx, res.Shares[0]); err != nil {
			t.Fatal(err)
		}
		if _, err := u.SubmitShare(ctx, res.Shares[0]); !errors.Is(err, ErrDuplicateShare) {
			t.Fatalf("got %v, want ErrDuplicateShare", err)
		}
	})

	t.Run("progress counts", func(t *testing.T) {
		p, err := u.SubmitShare(ctx, res.Shares[1])
		if err != nil {
			t.Fatal(err)
		}
		if p.Submitted != 2 || p.Required != 3 {
			t.Fatalf("progress = %+v, want {2 3}", p)
		}
	})
}

func TestShamirUnsealFailures(t *testing.T) {
	ctx := context.Background()

	t.Run("before init", func(t *testing.T) {
		u := NewShamirUnsealer(fileStore(t), 5, 3)
		if _, err := u.Unseal(ctx); !errors.Is(err, ErrNoSealConfig) {
			t.Fatalf("got %v, want ErrNoSealConfig", err)
		}
	})

	store := fileStore(t)
	setup := NewShamirUnsealer(store, 5, 3)
	res, err := setup.Init(ctx)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("not enough shares", func(t *testing.T) {
		u := NewShamirUnsealer(store, 0, 0)
		if _, err := u.SubmitShare(ctx, res.Shares[0]); err != nil {
			t.Fatal(err)
		}
		if _, err := u.Unseal(ctx); !errors.Is(err, ErrNotEnoughShares) {
			t.Fatalf("got %v, want ErrNotEnoughShares", err)
		}
	})

	t.Run("tampered share fails key check", func(t *testing.T) {
		u := NewShamirUnsealer(store, 0, 0)
		bad := append([]byte(nil), res.Shares[0]...)
		bad[5] ^= 1
		for _, s := range [][]byte{bad, res.Shares[1], res.Shares[2]} {
			if _, err := u.SubmitShare(ctx, s); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := u.Unseal(ctx); !errors.Is(err, ErrKeyCheckFailed) {
			t.Fatalf("got %v, want ErrKeyCheckFailed", err)
		}
	})

	t.Run("combine error maps to ErrInvalidShare", func(t *testing.T) {
		// Distinct shares whose x-coordinate (last byte) collides make
		// shamir.Combine fail with a duplicate-part error.
		u := NewShamirUnsealer(store, 0, 0)
		crafted := [][]byte{{1, 2, 9}, {3, 4, 9}, {5, 6, 7}}
		for _, s := range crafted {
			if _, err := u.SubmitShare(ctx, s); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := u.Unseal(ctx); !errors.Is(err, ErrInvalidShare) {
			t.Fatalf("got %v, want ErrInvalidShare", err)
		}
	})

	t.Run("wrong-length reconstruction fails key check", func(t *testing.T) {
		// Valid distinct 3-byte shares combine into a 2-byte "secret",
		// which is not a 32-byte master key.
		u := NewShamirUnsealer(store, 0, 0)
		crafted := [][]byte{{1, 2, 1}, {3, 4, 2}, {5, 6, 3}}
		for _, s := range crafted {
			if _, err := u.SubmitShare(ctx, s); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := u.Unseal(ctx); !errors.Is(err, ErrKeyCheckFailed) {
			t.Fatalf("got %v, want ErrKeyCheckFailed", err)
		}
	})

	t.Run("state resets after successful unseal", func(t *testing.T) {
		u := NewShamirUnsealer(store, 0, 0)
		for _, i := range []int{0, 1, 2} {
			if _, err := u.SubmitShare(ctx, res.Shares[i]); err != nil {
				t.Fatal(err)
			}
		}
		master, err := u.Unseal(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(master) != KeySize {
			t.Fatalf("master size = %d, want %d", len(master), KeySize)
		}
		// Submitted shares were consumed; unsealing again needs new shares.
		if _, err := u.Unseal(ctx); !errors.Is(err, ErrNotEnoughShares) {
			t.Fatalf("got %v, want ErrNotEnoughShares", err)
		}
	})
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/crypto/`
Expected: compile error (`NewShamirUnsealer` undefined).

- [ ] **Step 3: Write the implementation**

Create `internal/crypto/shamir.go`:

```go
package crypto

import (
	"context"
	"encoding/hex"
	"errors"
	"sync"

	"github.com/steveokay/janus-secrets/internal/crypto/shamir"
)

const (
	DefaultShamirShares    = 5
	DefaultShamirThreshold = 3
)

// ShamirUnsealer implements Unsealer with k-of-n secret sharing. Share
// submission is interactive: callers hold the concrete type and call
// SubmitShare until the threshold is reached, then Unseal.
type ShamirUnsealer struct {
	store     SealConfigStore
	shares    int
	threshold int

	mu        sync.Mutex
	submitted map[string][]byte
}

// Progress reports how many shares have been accepted so far.
type Progress struct {
	Submitted int
	Required  int
}

// NewShamirUnsealer creates a Shamir unsealer. shares/threshold are used by
// Init; passing 0, 0 selects the 3-of-5 default. Invalid combinations are
// rejected by Init (via shamir.Split), never persisted.
func NewShamirUnsealer(store SealConfigStore, shares, threshold int) *ShamirUnsealer {
	if shares == 0 && threshold == 0 {
		shares, threshold = DefaultShamirShares, DefaultShamirThreshold
	}
	return &ShamirUnsealer{store: store, shares: shares, threshold: threshold}
}

func (s *ShamirUnsealer) Init(ctx context.Context) (*InitResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.store.Get(ctx)
	if err == nil {
		return nil, ErrAlreadyInitialized
	}
	if !errors.Is(err, ErrNoSealConfig) {
		return nil, err
	}

	master, err := GenerateKey()
	if err != nil {
		return nil, err
	}
	defer zero(master)

	parts, err := shamir.Split(master, s.shares, s.threshold)
	if err != nil {
		return nil, err
	}
	kcv, err := makeKCV(master)
	if err != nil {
		return nil, err
	}
	cfg := &SealConfig{
		Type:          SealTypeShamir,
		Threshold:     s.threshold,
		Shares:        s.shares,
		KeyCheckValue: kcv,
	}
	if err := s.store.Put(ctx, cfg); err != nil {
		return nil, err
	}
	return &InitResult{Shares: parts}, nil
}

// SubmitShare accepts one share and reports progress toward the threshold.
func (s *ShamirUnsealer) SubmitShare(ctx context.Context, share []byte) (Progress, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cfg, err := s.loadConfig(ctx)
	if err != nil {
		return Progress{}, err
	}
	if len(share) < 2 {
		return Progress{}, ErrInvalidShare
	}
	key := hex.EncodeToString(share)
	if s.submitted == nil {
		s.submitted = make(map[string][]byte)
	}
	if _, ok := s.submitted[key]; ok {
		return Progress{Submitted: len(s.submitted), Required: cfg.Threshold}, ErrDuplicateShare
	}
	s.submitted[key] = append([]byte(nil), share...)
	return Progress{Submitted: len(s.submitted), Required: cfg.Threshold}, nil
}

// Unseal reconstructs the master key from submitted shares and verifies it
// against the key check value. On success the submitted shares are zeroized
// and cleared.
func (s *ShamirUnsealer) Unseal(ctx context.Context) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cfg, err := s.loadConfig(ctx)
	if err != nil {
		return nil, err
	}
	if len(s.submitted) < cfg.Threshold {
		return nil, ErrNotEnoughShares
	}
	parts := make([][]byte, 0, len(s.submitted))
	for _, p := range s.submitted {
		parts = append(parts, p)
	}
	master, err := shamir.Combine(parts)
	if err != nil {
		return nil, ErrInvalidShare
	}
	if len(master) != KeySize {
		zero(master)
		return nil, ErrKeyCheckFailed
	}
	if err := verifyKCV(master, cfg.KeyCheckValue); err != nil {
		zero(master)
		return nil, err
	}
	for _, p := range s.submitted {
		zero(p)
	}
	s.submitted = nil
	return master, nil
}

func (s *ShamirUnsealer) loadConfig(ctx context.Context) (*SealConfig, error) {
	cfg, err := s.store.Get(ctx)
	if err != nil {
		return nil, err
	}
	if cfg.Type != SealTypeShamir {
		return nil, ErrInvalidSealConfig
	}
	return cfg, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -race ./internal/crypto/`
Expected: PASS. If "combine error maps to ErrInvalidShare" fails because the vendored shamir's x-coordinate is the FIRST byte rather than the last at this tag, adjust the crafted shares in that subtest so two of them share the x-coordinate byte position the implementation actually uses (check `shamir.go`), and re-run.

- [ ] **Step 5: Commit**

```bash
git add internal/crypto/
git commit -m "feat(crypto): Shamir unsealer with interactive share submission and KCV verification"
```

---

### Task 8: KMS unsealer (`kms.go`) + AWS adapter (`kms_aws.go`)

**Files:**
- Create: `internal/crypto/kms.go`
- Create: `internal/crypto/kms_aws.go`
- Test: `internal/crypto/kms_test.go`
- Modify: `go.mod` / `go.sum` (adds aws-sdk-go-v2)

- [ ] **Step 1: Add the AWS SDK dependency**

```bash
go get github.com/aws/aws-sdk-go-v2/service/kms@latest
go get github.com/aws/aws-sdk-go-v2@latest
```

- [ ] **Step 2: Write the failing tests**

Create `internal/crypto/kms_test.go`:

```go
package crypto

import (
	"bytes"
	"context"
	"errors"
	"testing"

	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
)

// fakeKMS implements KMSClient with a reversible transform (prefix), plus
// injectable failures and response overrides.
type fakeKMS struct {
	encErr  error
	decErr  error
	decOverride []byte // if set, Decrypt returns this instead
}

func (f *fakeKMS) Encrypt(_ context.Context, plaintext []byte) ([]byte, error) {
	if f.encErr != nil {
		return nil, f.encErr
	}
	return append([]byte("kms:"), plaintext...), nil
}

func (f *fakeKMS) Decrypt(_ context.Context, ciphertext []byte) ([]byte, error) {
	if f.decErr != nil {
		return nil, f.decErr
	}
	if f.decOverride != nil {
		return f.decOverride, nil
	}
	return bytes.TrimPrefix(ciphertext, []byte("kms:")), nil
}

func TestKMSInitAndUnseal(t *testing.T) {
	ctx := context.Background()
	store := fileStore(t)
	u := NewKMSUnsealer(store, &fakeKMS{})

	res, err := u.Init(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Shares != nil {
		t.Fatal("KMS init must not return shares")
	}
	cfg, err := store.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Type != SealTypeAWSKMS || len(cfg.WrappedMasterKey) == 0 || len(cfg.KeyCheckValue) == 0 {
		t.Fatalf("persisted config: %+v", cfg)
	}

	master, err := u.Unseal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	k := NewKeyring()
	if err := k.Unseal(master); err != nil {
		t.Fatal(err)
	}
	if _, err := k.WrapProjectKEK(testKey(0x0B), "p"); err != nil {
		t.Fatal(err)
	}
}

func TestKMSInitFailures(t *testing.T) {
	ctx := context.Background()

	t.Run("already initialized", func(t *testing.T) {
		store := fileStore(t)
		u := NewKMSUnsealer(store, &fakeKMS{})
		if _, err := u.Init(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := u.Init(ctx); !errors.Is(err, ErrAlreadyInitialized) {
			t.Fatalf("got %v, want ErrAlreadyInitialized", err)
		}
	})

	t.Run("store get error", func(t *testing.T) {
		u := NewKMSUnsealer(&stubStore{getErr: errors.New("db down")}, &fakeKMS{})
		if _, err := u.Init(ctx); err == nil {
			t.Fatal("want error, got nil")
		}
	})

	t.Run("rand failure", func(t *testing.T) {
		restore := randReader
		randReader = failReader{}
		defer func() { randReader = restore }()
		u := NewKMSUnsealer(fileStore(t), &fakeKMS{})
		if _, err := u.Init(ctx); err == nil {
			t.Fatal("want error, got nil")
		}
	})

	t.Run("kms encrypt failure", func(t *testing.T) {
		u := NewKMSUnsealer(fileStore(t), &fakeKMS{encErr: errors.New("kms down")})
		if _, err := u.Init(ctx); err == nil {
			t.Fatal("want error, got nil")
		}
	})

	t.Run("kcv rand failure after keygen", func(t *testing.T) {
		restore := randReader
		randReader = &failAfterReader{n: 1}
		defer func() { randReader = restore }()
		u := NewKMSUnsealer(fileStore(t), &fakeKMS{})
		if _, err := u.Init(ctx); err == nil {
			t.Fatal("want error, got nil")
		}
	})

	t.Run("store put error", func(t *testing.T) {
		u := NewKMSUnsealer(&stubStore{putErr: errors.New("disk full")}, &fakeKMS{})
		if _, err := u.Init(ctx); err == nil {
			t.Fatal("want error, got nil")
		}
	})
}

func TestKMSUnsealFailures(t *testing.T) {
	ctx := context.Background()

	t.Run("before init", func(t *testing.T) {
		u := NewKMSUnsealer(fileStore(t), &fakeKMS{})
		if _, err := u.Unseal(ctx); !errors.Is(err, ErrNoSealConfig) {
			t.Fatalf("got %v, want ErrNoSealConfig", err)
		}
	})

	t.Run("wrong seal type", func(t *testing.T) {
		store := &stubStore{cfg: &SealConfig{Type: SealTypeShamir}}
		u := NewKMSUnsealer(store, &fakeKMS{})
		if _, err := u.Unseal(ctx); !errors.Is(err, ErrInvalidSealConfig) {
			t.Fatalf("got %v, want ErrInvalidSealConfig", err)
		}
	})

	// Shared initialized store for the remaining cases.
	store := fileStore(t)
	if _, err := NewKMSUnsealer(store, &fakeKMS{}).Init(ctx); err != nil {
		t.Fatal(err)
	}

	t.Run("kms decrypt failure", func(t *testing.T) {
		u := NewKMSUnsealer(store, &fakeKMS{decErr: errors.New("denied")})
		if _, err := u.Unseal(ctx); err == nil {
			t.Fatal("want error, got nil")
		}
	})

	t.Run("wrong-length master", func(t *testing.T) {
		u := NewKMSUnsealer(store, &fakeKMS{decOverride: []byte("short")})
		if _, err := u.Unseal(ctx); !errors.Is(err, ErrKeyCheckFailed) {
			t.Fatalf("got %v, want ErrKeyCheckFailed", err)
		}
	})

	t.Run("wrong master fails key check", func(t *testing.T) {
		u := NewKMSUnsealer(store, &fakeKMS{decOverride: testKey(0xEE)})
		if _, err := u.Unseal(ctx); !errors.Is(err, ErrKeyCheckFailed) {
			t.Fatalf("got %v, want ErrKeyCheckFailed", err)
		}
	})
}

// fakeAWSAPI implements AWSKMSAPI for adapter tests.
type fakeAWSAPI struct {
	encOut *awskms.EncryptOutput
	encErr error
	decOut *awskms.DecryptOutput
	decErr error

	gotKeyID string
}

func (f *fakeAWSAPI) Encrypt(_ context.Context, in *awskms.EncryptInput, _ ...func(*awskms.Options)) (*awskms.EncryptOutput, error) {
	if in.KeyId != nil {
		f.gotKeyID = *in.KeyId
	}
	return f.encOut, f.encErr
}

func (f *fakeAWSAPI) Decrypt(_ context.Context, in *awskms.DecryptInput, _ ...func(*awskms.Options)) (*awskms.DecryptOutput, error) {
	if in.KeyId != nil {
		f.gotKeyID = *in.KeyId
	}
	return f.decOut, f.decErr
}

func TestAWSKMSClientAdapter(t *testing.T) {
	ctx := context.Background()

	t.Run("encrypt", func(t *testing.T) {
		api := &fakeAWSAPI{encOut: &awskms.EncryptOutput{CiphertextBlob: []byte("blob")}}
		c := NewAWSKMSClient(api, "key-arn")
		got, err := c.Encrypt(ctx, []byte("pt"))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, []byte("blob")) || api.gotKeyID != "key-arn" {
			t.Fatalf("got %q, keyID %q", got, api.gotKeyID)
		}
	})

	t.Run("encrypt error", func(t *testing.T) {
		c := NewAWSKMSClient(&fakeAWSAPI{encErr: errors.New("denied")}, "key-arn")
		if _, err := c.Encrypt(ctx, []byte("pt")); err == nil {
			t.Fatal("want error, got nil")
		}
	})

	t.Run("decrypt", func(t *testing.T) {
		api := &fakeAWSAPI{decOut: &awskms.DecryptOutput{Plaintext: []byte("pt")}}
		c := NewAWSKMSClient(api, "key-arn")
		got, err := c.Decrypt(ctx, []byte("blob"))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, []byte("pt")) || api.gotKeyID != "key-arn" {
			t.Fatalf("got %q, keyID %q", got, api.gotKeyID)
		}
	})

	t.Run("decrypt error", func(t *testing.T) {
		c := NewAWSKMSClient(&fakeAWSAPI{decErr: errors.New("denied")}, "key-arn")
		if _, err := c.Decrypt(ctx, []byte("blob")); err == nil {
			t.Fatal("want error, got nil")
		}
	})
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/crypto/`
Expected: compile error (`NewKMSUnsealer` undefined).

- [ ] **Step 4: Write the implementation**

Create `internal/crypto/kms.go`:

```go
package crypto

import (
	"context"
	"errors"
)

// KMSClient is the minimal contract for a cloud KMS used for auto-unseal.
// The production implementation is AWSKMSClient; tests use a fake.
type KMSClient interface {
	Encrypt(ctx context.Context, plaintext []byte) ([]byte, error)
	Decrypt(ctx context.Context, ciphertext []byte) ([]byte, error)
}

// KMSUnsealer implements Unsealer via a cloud KMS: the master key is
// generated locally, stored wrapped by the KMS key, and recovered with a
// single Decrypt call at startup — no operator interaction.
type KMSUnsealer struct {
	store  SealConfigStore
	client KMSClient
}

func NewKMSUnsealer(store SealConfigStore, client KMSClient) *KMSUnsealer {
	return &KMSUnsealer{store: store, client: client}
}

func (u *KMSUnsealer) Init(ctx context.Context) (*InitResult, error) {
	_, err := u.store.Get(ctx)
	if err == nil {
		return nil, ErrAlreadyInitialized
	}
	if !errors.Is(err, ErrNoSealConfig) {
		return nil, err
	}

	master, err := GenerateKey()
	if err != nil {
		return nil, err
	}
	defer zero(master)

	wrapped, err := u.client.Encrypt(ctx, master)
	if err != nil {
		return nil, err
	}
	kcv, err := makeKCV(master)
	if err != nil {
		return nil, err
	}
	cfg := &SealConfig{
		Type:             SealTypeAWSKMS,
		KeyCheckValue:    kcv,
		WrappedMasterKey: wrapped,
	}
	if err := u.store.Put(ctx, cfg); err != nil {
		return nil, err
	}
	return &InitResult{}, nil
}

func (u *KMSUnsealer) Unseal(ctx context.Context) ([]byte, error) {
	cfg, err := u.store.Get(ctx)
	if err != nil {
		return nil, err
	}
	if cfg.Type != SealTypeAWSKMS {
		return nil, ErrInvalidSealConfig
	}
	master, err := u.client.Decrypt(ctx, cfg.WrappedMasterKey)
	if err != nil {
		return nil, err
	}
	if len(master) != KeySize {
		zero(master)
		return nil, ErrKeyCheckFailed
	}
	if err := verifyKCV(master, cfg.KeyCheckValue); err != nil {
		zero(master)
		return nil, err
	}
	return master, nil
}
```

Create `internal/crypto/kms_aws.go`:

```go
package crypto

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"
)

// AWSKMSAPI is the subset of the AWS KMS SDK client this package uses.
// *kms.Client satisfies it; tests substitute a fake.
type AWSKMSAPI interface {
	Encrypt(ctx context.Context, in *kms.EncryptInput, opts ...func(*kms.Options)) (*kms.EncryptOutput, error)
	Decrypt(ctx context.Context, in *kms.DecryptInput, opts ...func(*kms.Options)) (*kms.DecryptOutput, error)
}

// AWSKMSClient adapts the AWS SDK to the KMSClient interface, pinned to a
// single KMS key.
type AWSKMSClient struct {
	api   AWSKMSAPI
	keyID string
}

// NewAWSKMSClient wraps an AWS KMS client (typically kms.NewFromConfig(cfg))
// for use with KMSUnsealer. keyID is a key ID, ARN, or alias.
func NewAWSKMSClient(api AWSKMSAPI, keyID string) *AWSKMSClient {
	return &AWSKMSClient{api: api, keyID: keyID}
}

func (c *AWSKMSClient) Encrypt(ctx context.Context, plaintext []byte) ([]byte, error) {
	out, err := c.api.Encrypt(ctx, &kms.EncryptInput{
		KeyId:     aws.String(c.keyID),
		Plaintext: plaintext,
	})
	if err != nil {
		return nil, err
	}
	return out.CiphertextBlob, nil
}

func (c *AWSKMSClient) Decrypt(ctx context.Context, ciphertext []byte) ([]byte, error) {
	out, err := c.api.Decrypt(ctx, &kms.DecryptInput{
		// KeyId is optional for symmetric decrypt but pinning it prevents
		// decrypting blobs from an unexpected key.
		KeyId:          aws.String(c.keyID),
		CiphertextBlob: ciphertext,
	})
	if err != nil {
		return nil, err
	}
	return out.Plaintext, nil
}
```

Run `go mod tidy` to settle go.sum.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test -race ./internal/crypto/`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/crypto/ go.mod go.sum
git commit -m "feat(crypto): KMS auto-unseal with AWS adapter behind fake-able interface"
```

---

### Task 9: Leak test, coverage gate, final verification

**Files:**
- Test: `internal/crypto/leak_test.go`
- Modify: `.github/workflows/ci.yml` (add coverage gate)

- [ ] **Step 1: Write the leak test**

Create `internal/crypto/leak_test.go`:

```go
package crypto

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"regexp"
	"strings"
	"testing"
)

// TestNoSecretsInErrorMessages drives a broad set of error paths with known
// key material and plaintext, then asserts none of it — raw, hex, or base64 —
// appears in any error message. This seeds the project-wide leak test
// required by CLAUDE.md.
func TestNoSecretsInErrorMessages(t *testing.T) {
	ctx := context.Background()
	key := testKey(0x5A)
	plaintext := []byte("SUPER-SECRET-VALUE-DO-NOT-LEAK")

	var errs []error
	collect := func(err error) {
		if err != nil {
			errs = append(errs, err)
		}
	}

	// AEAD failures around real key material and plaintext.
	ct, err := Encrypt(key, plaintext, []byte("aad"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Decrypt(testKey(0x5B), ct, []byte("aad"))
	collect(err)
	_, err = Decrypt(key, ct, []byte("wrong-aad"))
	collect(err)
	_, err = Decrypt(key[:16], ct, []byte("aad"))
	collect(err)
	_, err = ParseCiphertext(plaintext)
	collect(err)

	// Wrapping failures.
	_, err = WrapKey(key, plaintext, nil)
	collect(err)
	_, err = UnwrapKey(testKey(0x5B), ct, []byte("aad"))
	collect(err)

	// Keyring failures.
	kr := NewKeyring()
	_, err = kr.WrapProjectKEK(key, "p")
	collect(err)
	collect(kr.Unseal(plaintext))

	// Unseal failures.
	collect(verifyKCV(key, plaintext))
	su := NewShamirUnsealer(fileStore(t), 5, 3)
	res, err := su.Init(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = su.SubmitShare(ctx, res.Shares[0])
	collect(err)
	_, err = su.SubmitShare(ctx, res.Shares[0]) // duplicate
	collect(err)
	_, err = su.Unseal(ctx) // not enough
	collect(err)

	if len(errs) < 10 {
		t.Fatalf("expected to collect at least 10 errors, got %d — error paths lost?", len(errs))
	}

	forbidden := []string{
		string(plaintext),
		hex.EncodeToString(key),
		base64.StdEncoding.EncodeToString(key),
		hex.EncodeToString(res.Shares[0]),
	}
	// Any long token in an error message is suspicious even if it isn't a
	// known secret: sentinel messages are short English phrases.
	longToken := regexp.MustCompile(`[A-Za-z0-9+/=_-]{24,}`)

	for _, e := range errs {
		msg := e.Error()
		for _, f := range forbidden {
			if strings.Contains(msg, f) {
				t.Errorf("error message contains secret material: %q", msg)
			}
		}
		if longToken.MatchString(msg) {
			t.Errorf("error message contains suspicious long token: %q", msg)
		}
	}
}

// TestSentinelMessagesAreClean asserts every exported sentinel is a short,
// fixed English phrase with no interpolation.
func TestSentinelMessagesAreClean(t *testing.T) {
	sentinels := []error{
		ErrSealed, ErrAlreadyUnsealed, ErrInvalidKeySize, ErrDecryptFailed,
		ErrInvalidShare, ErrDuplicateShare, ErrNotEnoughShares,
		ErrKeyCheckFailed, ErrNoSealConfig, ErrAlreadyInitialized,
		ErrInvalidSealConfig,
	}
	re := regexp.MustCompile(`^[a-z][a-z0-9 -]{5,60}$`)
	for _, s := range sentinels {
		if !re.MatchString(s.Error()) {
			t.Errorf("sentinel message %q does not look like a fixed phrase", s.Error())
		}
	}
}
```

- [ ] **Step 2: Run the leak test**

Run: `go test -race -run 'TestNoSecrets|TestSentinel' ./internal/crypto/ -v`
Expected: PASS

- [ ] **Step 3: Check coverage and close any gaps**

Run: `make cover`
Expected: the final line reports `total: (statements) 100.0%`.

If below 100.0%: run `go tool cover -html=crypto.out -o coverage.html`, open it, and add table-driven test cases for each uncovered line (they will be error branches; use `stubStore`, `failReader`, `failAfterReader`, or `aeadForKey` overrides as the existing tests do). Do not delete defensive code to inflate coverage. Repeat until 100.0%.

- [ ] **Step 4: Add the coverage gate to CI**

In `.github/workflows/ci.yml`, add this step after the `go test -race ./...` step:

```yaml
      - name: enforce 100% coverage on internal/crypto
        run: |
          go test -coverprofile=crypto.out ./internal/crypto
          pct=$(go tool cover -func=crypto.out | tail -1 | awk '{print $3}')
          echo "internal/crypto coverage: $pct"
          if [ "$pct" != "100.0%" ]; then
            echo "coverage gate failed: internal/crypto must be 100.0%"
            exit 1
          fi
```

- [ ] **Step 5: Full local verification**

Run: `go build ./... && go vet ./... && go test -race ./...`
Expected: all PASS.

Run: `go run golang.org/x/vuln/cmd/govulncheck@latest ./...`
Expected: no vulnerabilities.

Run: `go run github.com/securego/gosec/v2/cmd/gosec@v2.20.0 -exclude-dir=internal/crypto/shamir ./...`
Expected: 0 issues. If gosec flags something in our code, fix the code (do not suppress) unless it is the documented `#nosec G304` in sealstore.go.

- [ ] **Step 6: Commit**

```bash
git add internal/crypto/leak_test.go .github/workflows/ci.yml
git commit -m "test(crypto): secret-leak assertions and 100% coverage gate in CI"
```

---

## Completion criteria

- `go test -race ./...` passes.
- `make cover` reports 100.0% on `internal/crypto`.
- govulncheck and gosec are clean.
- `docker compose up` starts Postgres (nothing connects to it yet — expected).
- No secret material appears in any error string (leak tests pass).
