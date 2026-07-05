package transit

import (
	"bytes"
	"context"
	"testing"
)

func TestCreateEncryptDecryptRoundTrip(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	app := uniqueName("app")
	if _, err := svc.CreateKey(ctx, app, TypeAES); err != nil {
		t.Fatal(err)
	}
	ct, err := svc.Encrypt(ctx, app, []byte("hello"), nil)
	if err != nil {
		t.Fatal(err)
	}
	pt, err := svc.Decrypt(ctx, app, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pt, []byte("hello")) {
		t.Fatalf("roundtrip: %q", pt)
	}

	// Tamper → ErrBadCiphertext.
	bad := ct[:len(ct)-1] + "A"
	if _, err := svc.Decrypt(ctx, app, bad, nil); err == nil {
		t.Fatal("tampered ciphertext must fail")
	}
	// AAD mismatch fails closed.
	ct2, _ := svc.Encrypt(ctx, app, []byte("hi"), []byte("ctx-a"))
	if _, err := svc.Decrypt(ctx, app, ct2, []byte("ctx-b")); err == nil {
		t.Fatal("associated-data mismatch must fail")
	}
	// Wrong key type: create ed25519, try encrypt.
	sig := uniqueName("sig")
	if _, err := svc.CreateKey(ctx, sig, TypeEd25519); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Encrypt(ctx, sig, []byte("x"), nil); err != ErrWrongKeyType {
		t.Fatalf("encrypt on ed25519: want ErrWrongKeyType, got %v", err)
	}
}
