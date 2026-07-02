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
