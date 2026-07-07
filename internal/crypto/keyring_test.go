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
