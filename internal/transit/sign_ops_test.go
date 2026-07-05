package transit

import (
	"context"
	"encoding/base64"
	"testing"
)

func TestSignVerifyAndDatakey(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	sigKey := uniqueName("sig")
	encKey := uniqueName("enc")

	_, _ = svc.CreateKey(ctx, sigKey, TypeEd25519)
	sig, err := svc.Sign(ctx, sigKey, []byte("payload"))
	if err != nil {
		t.Fatal(err)
	}
	ok, err := svc.Verify(ctx, sigKey, []byte("payload"), sig)
	if err != nil || !ok {
		t.Fatalf("verify: %v %v", ok, err)
	}
	bad, _ := svc.Verify(ctx, sigKey, []byte("other"), sig)
	if bad {
		t.Fatal("wrong message must not verify")
	}
	// Sign on an aes key → wrong type.
	_, _ = svc.CreateKey(ctx, encKey, TypeAES)
	if _, err := svc.Sign(ctx, encKey, []byte("x")); err != ErrWrongKeyType {
		t.Fatalf("sign on aes: want ErrWrongKeyType, got %v", err)
	}
	// Datakey: wrapped decrypts back to the returned plaintext.
	pt, ct, err := svc.DataKey(ctx, encKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(pt) != 32 {
		t.Fatalf("datakey plaintext len = %d, want 32", len(pt))
	}
	got, err := svc.Decrypt(ctx, encKey, ct, nil)
	if err != nil || base64.StdEncoding.EncodeToString(got) != base64.StdEncoding.EncodeToString(pt) {
		t.Fatalf("wrapped datakey must decrypt to plaintext: %v", err)
	}
}
