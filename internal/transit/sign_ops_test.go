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

// TestVerifyHonorsMinDecryptionVersion proves that raising min_decryption_version
// retires old signing versions: a signature made under a version below the floor
// must not verify even though that version row still exists (symmetric with
// Decrypt's ErrVersionTooOld).
func TestVerifyHonorsMinDecryptionVersion(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	k := uniqueName("sig")
	_, _ = svc.CreateKey(ctx, k, TypeEd25519)

	sigV1, err := svc.Sign(ctx, k, []byte("payload"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Rotate(ctx, k); err != nil {
		t.Fatal(err)
	}
	// Before raising the floor, the v1 signature still verifies.
	if ok, err := svc.Verify(ctx, k, []byte("payload"), sigV1); err != nil || !ok {
		t.Fatalf("v1 before floor: ok=%v err=%v", ok, err)
	}
	// Raise the floor to 2; the v1 signature is now rejected, not merely invalid.
	two := 2
	if err := svc.UpdateConfig(ctx, k, &two, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Verify(ctx, k, []byte("payload"), sigV1); err != ErrVersionTooOld {
		t.Fatalf("v1 after floor: want ErrVersionTooOld, got %v", err)
	}
	// A fresh signature under the new latest version still verifies.
	sigV2, err := svc.Sign(ctx, k, []byte("payload"))
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := svc.Verify(ctx, k, []byte("payload"), sigV2); err != nil || !ok {
		t.Fatalf("v2 after floor: ok=%v err=%v", ok, err)
	}
}

// TestRewrapPreservesAssociatedData proves rewrap can roll AAD-bound ciphertext
// forward when the caller re-supplies the same associated_data, and that a
// mismatched (or absent) aad fails closed.
func TestRewrapPreservesAssociatedData(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	k := uniqueName("enc")
	_, _ = svc.CreateKey(ctx, k, TypeAES)

	aad := []byte("tenant-42")
	ctV1, err := svc.Encrypt(ctx, k, []byte("secret"), aad)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Rotate(ctx, k); err != nil {
		t.Fatal(err)
	}
	// Rewrap without the aad fails closed (generic ErrBadCiphertext).
	if _, err := svc.Rewrap(ctx, k, ctV1, nil); err != ErrBadCiphertext {
		t.Fatalf("rewrap without aad: want ErrBadCiphertext, got %v", err)
	}
	// Rewrap with the matching aad rolls forward; the result decrypts under the
	// same aad and yields the original plaintext.
	ctV2, err := svc.Rewrap(ctx, k, ctV1, aad)
	if err != nil {
		t.Fatal(err)
	}
	pt, err := svc.Decrypt(ctx, k, ctV2, aad)
	if err != nil || string(pt) != "secret" {
		t.Fatalf("decrypt rewrapped: %q %v", pt, err)
	}
}
