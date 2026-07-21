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

	// Readers contend the RLock and tolerate ErrSealed, which the writer
	// below may cause at any moment. Any OTHER error is a bug.
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				if _, err := k.WrapProjectKEK(kek, "p"); err != nil && !errors.Is(err, ErrSealed) {
					t.Errorf("WrapProjectKEK: unexpected error %v", err)
				}
				_ = k.Sealed()
			}
		}()
	}

	// A single writer repeatedly seals and unseals, contending the exclusive
	// Lock against the readers. Under -race this exercises the RWMutex
	// discipline the keyring depends on. Single writer, so Unseal never
	// races itself into ErrAlreadyUnsealed.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 200; j++ {
			k.Seal()
			if err := k.Unseal(testKey(0xAA)); err != nil {
				t.Errorf("Unseal: %v", err)
			}
		}
	}()

	wg.Wait()
}

func TestAuthKeyWrapUnwrap(t *testing.T) {
	k := NewKeyring()

	// Sealed: both operations refuse.
	if _, err := k.WrapAuthKey(testKey(0x11)); !errors.Is(err, ErrSealed) {
		t.Fatalf("sealed wrap: %v", err)
	}
	if _, err := k.UnwrapAuthKey(Ciphertext{}); !errors.Is(err, ErrSealed) {
		t.Fatalf("sealed unwrap: %v", err)
	}

	if err := k.Unseal(testKey(0xAA)); err != nil {
		t.Fatal(err)
	}

	authKey := testKey(0x33)
	ct, err := k.WrapAuthKey(authKey)
	if err != nil {
		t.Fatal(err)
	}
	got, err := k.UnwrapAuthKey(ct)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, authKey) {
		t.Fatal("round-trip mismatch")
	}
	zero(got)

	// Wrong-size key refused at wrap.
	if _, err := k.WrapAuthKey([]byte("short")); !errors.Is(err, ErrInvalidKeySize) {
		t.Fatalf("short key: %v", err)
	}

	// A project-KEK ciphertext must not unwrap as the auth key (AAD binding).
	kek, _ := k.WrapProjectKEK(testKey(0x44), "some-project")
	if _, err := k.UnwrapAuthKey(kek); !errors.Is(err, ErrDecryptFailed) {
		t.Fatalf("cross-AAD unwrap: %v", err)
	}
}

func TestOIDCClientSecretWrapRoundTrip(t *testing.T) {
	k := NewKeyring()
	if err := k.Unseal(testKey(0xAA)); err != nil {
		t.Fatal(err)
	}
	secret := []byte("super-secret-oidc-client-value-of-arbitrary-length")

	ct, err := k.WrapOIDCClientSecret(secret)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	got, err := k.UnwrapOIDCClientSecret(ct)
	if err != nil {
		t.Fatalf("unwrap: %v", err)
	}
	if string(got) != string(secret) {
		t.Fatalf("round-trip mismatch: got %q", got)
	}

	ct.Data[0] ^= 0xff
	if _, err := k.UnwrapOIDCClientSecret(ct); err == nil {
		t.Fatal("expected error unwrapping tampered ciphertext")
	}
}

func TestOIDCClientSecretWrapSealed(t *testing.T) {
	k := NewKeyring() // sealed
	if _, err := k.WrapOIDCClientSecret([]byte("x")); err != ErrSealed {
		t.Fatalf("want ErrSealed, got %v", err)
	}
	if _, err := k.UnwrapOIDCClientSecret(Ciphertext{Nonce: make([]byte, NonceSize)}); err != ErrSealed {
		t.Fatalf("want ErrSealed, got %v", err)
	}
}

func TestNotificationConfigWrapRoundTrip(t *testing.T) {
	k := NewKeyring()
	if err := k.Unseal(testKey(0xAA)); err != nil {
		t.Fatal(err)
	}
	cfg := []byte(`{"url":"https://hooks.example/x","hmac_key":"k"}`)

	ct, err := k.WrapNotificationConfig("chan-1", cfg)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	got, err := k.UnwrapNotificationConfig("chan-1", ct)
	if err != nil {
		t.Fatalf("unwrap: %v", err)
	}
	if string(got) != string(cfg) {
		t.Fatalf("round-trip mismatch: got %q", got)
	}

	// AAD binds the channel id: a blob unwrapped under a different id fails.
	if _, err := k.UnwrapNotificationConfig("chan-2", ct); err == nil {
		t.Fatal("expected error unwrapping under a different channel id")
	}

	ct.Data[0] ^= 0xff
	if _, err := k.UnwrapNotificationConfig("chan-1", ct); err == nil {
		t.Fatal("expected error unwrapping tampered ciphertext")
	}
}

func TestNotificationConfigWrapSealed(t *testing.T) {
	k := NewKeyring() // sealed
	if _, err := k.WrapNotificationConfig("c", []byte("x")); err != ErrSealed {
		t.Fatalf("want ErrSealed, got %v", err)
	}
	if _, err := k.UnwrapNotificationConfig("c", Ciphertext{Nonce: make([]byte, NonceSize)}); err != ErrSealed {
		t.Fatalf("want ErrSealed, got %v", err)
	}
}

func TestKeyringDoubleSeal(t *testing.T) {
	k := NewKeyring()
	k.Seal() // sealing an already-sealed keyring must not panic
	if err := k.Unseal(testKey(0xAA)); err != nil {
		t.Fatal(err)
	}
	k.Seal()
	k.Seal() // idempotent
	if !k.Sealed() {
		t.Fatal("keyring should be sealed")
	}
}

func TestRotateMasterRewrapsAndSwaps(t *testing.T) {
	kr := NewKeyring()
	m1, _ := GenerateKey()
	if err := kr.Unseal(m1); err != nil {
		t.Fatal(err)
	}
	kek, _ := GenerateKey()
	wrapped, err := kr.WrapProjectKEK(kek, "p1")
	if err != nil {
		t.Fatal(err)
	}
	old := wrapped.Marshal()

	m2, _ := GenerateKey()
	var newBlob []byte
	persisted := false
	err = kr.RotateMaster(m2,
		func(unwrap func([]byte, []byte) ([]byte, error), wrap func([]byte, []byte) ([]byte, error)) error {
			pt, uerr := unwrap(old, ProjectKEKAAD("p1"))
			if uerr != nil {
				return uerr
			}
			defer zero(pt)
			nb, werr := wrap(pt, ProjectKEKAAD("p1"))
			newBlob = nb
			return werr
		},
		func() error { persisted = true; return nil },
	)
	if err != nil {
		t.Fatalf("RotateMaster: %v", err)
	}
	if !persisted {
		t.Fatal("persist not called")
	}
	ct, _ := ParseCiphertext(newBlob)
	got, err := kr.UnwrapProjectKEK(ct, "p1")
	if err != nil {
		t.Fatalf("unwrap after rotate: %v", err)
	}
	if !bytes.Equal(got, kek) {
		t.Fatal("re-wrapped KEK mismatch")
	}
	if _, err := kr.UnwrapProjectKEK(mustParse(t, old), "p1"); err == nil {
		t.Fatal("old blob still unwraps after rotation — master not swapped")
	}
}

func TestRotateMasterPersistFailureKeepsOldMaster(t *testing.T) {
	kr := NewKeyring()
	m1, _ := GenerateKey()
	_ = kr.Unseal(m1)
	kek, _ := GenerateKey()
	wrapped, _ := kr.WrapProjectKEK(kek, "p1")

	m2, _ := GenerateKey()
	wantErr := errors.New("db down")
	err := kr.RotateMaster(m2,
		func(unwrap func([]byte, []byte) ([]byte, error), wrap func([]byte, []byte) ([]byte, error)) error {
			return nil
		},
		func() error { return wantErr },
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("want persist error, got %v", err)
	}
	if _, err := kr.UnwrapProjectKEK(wrapped, "p1"); err != nil {
		t.Fatalf("old master lost after failed persist: %v", err)
	}
}

func TestRotateMasterSealed(t *testing.T) {
	kr := NewKeyring()
	m2, _ := GenerateKey()
	err := kr.RotateMaster(m2,
		func(_ func([]byte, []byte) ([]byte, error), _ func([]byte, []byte) ([]byte, error)) error { return nil },
		func() error { return nil })
	if !errors.Is(err, ErrSealed) {
		t.Fatalf("want ErrSealed, got %v", err)
	}
}

func TestRotateMasterRewrapErrorKeepsOldMaster(t *testing.T) {
	kr := NewKeyring()
	m1, _ := GenerateKey()
	_ = kr.Unseal(m1)
	kek, _ := GenerateKey()
	wrapped, _ := kr.WrapProjectKEK(kek, "p1")

	m2, _ := GenerateKey()
	wantErr := errors.New("rewrap boom")
	persisted := false
	err := kr.RotateMaster(m2,
		func(unwrap func([]byte, []byte) ([]byte, error), wrap func([]byte, []byte) ([]byte, error)) error {
			return wantErr
		},
		func() error { persisted = true; return nil },
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("want rewrap error, got %v", err)
	}
	if persisted {
		t.Fatal("persist must NOT run after a rewrap error")
	}
	// Old master retained: original blob still unwraps.
	if _, err := kr.UnwrapProjectKEK(wrapped, "p1"); err != nil {
		t.Fatalf("old master lost after failed rewrap: %v", err)
	}
}

func TestRotateMasterInvalidNewKeySize(t *testing.T) {
	kr := NewKeyring()
	m1, _ := GenerateKey()
	_ = kr.Unseal(m1)
	kek, _ := GenerateKey()
	wrapped, _ := kr.WrapProjectKEK(kek, "p1")

	err := kr.RotateMaster([]byte("too-short"),
		func(_ func([]byte, []byte) ([]byte, error), _ func([]byte, []byte) ([]byte, error)) error { return nil },
		func() error { return nil },
	)
	if !errors.Is(err, ErrInvalidKeySize) {
		t.Fatalf("got %v, want ErrInvalidKeySize", err)
	}
	// Old master untouched: original blob still unwraps.
	if _, err := kr.UnwrapProjectKEK(wrapped, "p1"); err != nil {
		t.Fatalf("old master lost after size rejection: %v", err)
	}
}

// TestRotateMasterUnwrapParseError exercises the unwrap closure's
// ParseCiphertext error path: feeding invalid ciphertext bytes to unwrap
// must return ErrDecryptFailed.
func TestRotateMasterUnwrapParseError(t *testing.T) {
	kr := NewKeyring()
	m1, _ := GenerateKey()
	_ = kr.Unseal(m1)

	m2, _ := GenerateKey()
	err := kr.RotateMaster(m2,
		func(unwrap func([]byte, []byte) ([]byte, error), _ func([]byte, []byte) ([]byte, error)) error {
			_, uerr := unwrap([]byte("not a ciphertext"), ProjectKEKAAD("p1"))
			if !errors.Is(uerr, ErrDecryptFailed) {
				t.Fatalf("unwrap: got %v, want ErrDecryptFailed", uerr)
			}
			return uerr
		},
		func() error { t.Fatal("persist must not run"); return nil },
	)
	if !errors.Is(err, ErrDecryptFailed) {
		t.Fatalf("RotateMaster: got %v, want ErrDecryptFailed", err)
	}
}

// TestRotateMasterWrapEncryptError forces the wrap closure's Encrypt to fail
// by making the GCM nonce read fail. unwrap's Decrypt reads no randomness, so
// the failing reader only affects wrap.
func TestRotateMasterWrapEncryptError(t *testing.T) {
	kr := NewKeyring()
	m1, _ := GenerateKey()
	_ = kr.Unseal(m1)
	kek, _ := GenerateKey()
	wrapped, _ := kr.WrapProjectKEK(kek, "p1")
	old := wrapped.Marshal()
	m2, _ := GenerateKey() // generate before installing the failing reader

	restore := randReader
	randReader = failReader{} // Decrypt reads no rand; wrap's Encrypt nonce read fails.
	defer func() { randReader = restore }()

	err := kr.RotateMaster(m2,
		func(unwrap func([]byte, []byte) ([]byte, error), wrap func([]byte, []byte) ([]byte, error)) error {
			pt, uerr := unwrap(old, ProjectKEKAAD("p1"))
			if uerr != nil {
				return uerr
			}
			defer zero(pt)
			_, werr := wrap(pt, ProjectKEKAAD("p1"))
			if werr == nil {
				t.Fatal("wrap: want Encrypt error, got nil")
			}
			return werr
		},
		func() error { t.Fatal("persist must not run"); return nil },
	)
	if err == nil {
		t.Fatal("RotateMaster: want wrap Encrypt error, got nil")
	}
}

func mustParse(t *testing.T, b []byte) Ciphertext {
	t.Helper()
	ct, err := ParseCiphertext(b)
	if err != nil {
		t.Fatal(err)
	}
	return ct
}

func TestSyncFingerprint(t *testing.T) {
	k := NewKeyring()
	master := make([]byte, KeySize)
	for i := range master {
		master[i] = byte(i)
	}
	if err := k.Unseal(master); err != nil {
		t.Fatal(err)
	}

	a := k.SyncFingerprint([]byte("hello"))
	b := k.SyncFingerprint([]byte("hello"))
	c := k.SyncFingerprint([]byte("world"))
	if !bytes.Equal(a, b) {
		t.Fatal("fingerprint must be deterministic")
	}
	if bytes.Equal(a, c) {
		t.Fatal("fingerprint must vary with input")
	}
	if len(a) != 32 {
		t.Fatalf("want 32-byte HMAC-SHA256, got %d", len(a))
	}

	k.Seal()
	if k.SyncFingerprint([]byte("hello")) != nil {
		t.Fatal("sealed keyring must return nil fingerprint")
	}
}
