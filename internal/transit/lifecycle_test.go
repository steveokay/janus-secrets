package transit

import (
	"bytes"
	"context"
	"testing"
)

func TestRotateRewrapMinVersionTrim(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	k := uniqueName("k")
	_, _ = svc.CreateKey(ctx, k, TypeAES)

	ctV1, _ := svc.Encrypt(ctx, k, []byte("secret"), nil)

	m, err := svc.Rotate(ctx, k)
	if err != nil || m.LatestVersion != 2 {
		t.Fatalf("rotate: %+v %v", m, err)
	}
	// Old ciphertext still decrypts (v1 >= min 1).
	if pt, err := svc.Decrypt(ctx, k, ctV1, nil); err != nil || !bytes.Equal(pt, []byte("secret")) {
		t.Fatalf("old decrypt: %q %v", pt, err)
	}
	// Rewrap upgrades to latest, no plaintext exposed.
	ctV2, err := svc.Rewrap(ctx, k, ctV1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ctV2 == ctV1 {
		t.Fatal("rewrap should produce a new envelope")
	}
	// Raise the decryption floor to 2; v1 ciphertext now rejected, v2 ok.
	two := 2
	if err := svc.UpdateConfig(ctx, k, &two, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Decrypt(ctx, k, ctV1, nil); err != ErrVersionTooOld {
		t.Fatalf("v1 after floor: want ErrVersionTooOld, got %v", err)
	}
	if _, err := svc.Decrypt(ctx, k, ctV2, nil); err != nil {
		t.Fatalf("v2 after floor: %v", err)
	}
	// Trim below 2 removes v1; must not exceed min_decryption_version.
	if err := svc.Trim(ctx, k, 3); err != ErrValidation {
		t.Fatalf("trim above floor: want ErrValidation, got %v", err)
	}
	if err := svc.Trim(ctx, k, 2); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteRequiresDeletionAllowed(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	k := uniqueName("del")
	if _, err := svc.CreateKey(ctx, k, TypeAES); err != nil {
		t.Fatal(err)
	}

	// Deletion is refused until deletion_allowed is set.
	if err := svc.Delete(ctx, k); err != ErrDeletionNotAllowed {
		t.Fatalf("delete without allow: want ErrDeletionNotAllowed, got %v", err)
	}

	yes := true
	if err := svc.UpdateConfig(ctx, k, nil, &yes); err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(ctx, k); err != nil {
		t.Fatalf("delete after allow: %v", err)
	}
	// Gone now.
	if _, err := svc.Get(ctx, k); err != ErrKeyNotFound {
		t.Fatalf("get after delete: want ErrKeyNotFound, got %v", err)
	}
}

func TestGetAndListRoundTrip(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	k := uniqueName("meta")
	if _, err := svc.CreateKey(ctx, k, TypeEd25519); err != nil {
		t.Fatal(err)
	}

	m, err := svc.Get(ctx, k)
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != k || m.Type != TypeEd25519 || m.LatestVersion != 1 {
		t.Fatalf("get: %+v", m)
	}

	list, err := svc.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range list {
		if e.Name == k {
			found = true
		}
	}
	if !found {
		t.Fatalf("list missing %q: %+v", k, list)
	}

	// Missing key → ErrKeyNotFound.
	if _, err := svc.Get(ctx, uniqueName("nope")); err != ErrKeyNotFound {
		t.Fatalf("get missing: want ErrKeyNotFound, got %v", err)
	}
}
