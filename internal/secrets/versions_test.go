package secrets

import (
	"bytes"
	"context"
	"testing"
)

func TestVersionOpsAndHistory(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	_, configID := mkChain(t, s)

	// v1: A=a1, B=b1
	if _, err := s.SetSecrets(ctx, configID, []SecretChange{
		{Key: "A", Value: []byte("a1")}, {Key: "B", Value: []byte("b1")},
	}, "v1", "u"); err != nil {
		t.Fatal(err)
	}
	// v2: A=a2 (change), C=c1 (add), B deleted
	if _, err := s.SetSecrets(ctx, configID, []SecretChange{
		{Key: "A", Value: []byte("a2")}, {Key: "C", Value: []byte("c1")}, {Key: "B", Delete: true},
	}, "v2", "u"); err != nil {
		t.Fatal(err)
	}

	// Masked list: metadata only, no values.
	_, metas, err := s.ListSecrets(ctx, configID)
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 2 { // A, C live
		t.Fatalf("ListSecrets live keys = %d, want 2", len(metas))
	}

	// ListVersions.
	versions, err := s.ListVersions(ctx, configID)
	if err != nil || len(versions) != 2 {
		t.Fatalf("ListVersions: len=%d err=%v", len(versions), err)
	}

	// DiffVersions(1,2): added=[C], changed=[A], removed=[B].
	d, err := s.DiffVersions(ctx, configID, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Added) != 1 || d.Added[0] != "C" || len(d.Changed) != 1 || d.Changed[0] != "A" || len(d.Removed) != 1 || d.Removed[0] != "B" {
		t.Fatalf("diff mismatch: %+v", d)
	}

	// KeyHistory for A: versions 1 and 2, masked.
	hist, err := s.KeyHistory(ctx, configID, "A")
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 2 || hist[0].ValueVersion != 1 || hist[1].ValueVersion != 2 {
		t.Fatalf("A history: %+v", hist)
	}

	// GetSecretVersion decrypts a historical value (A v1 == a1).
	old, err := s.GetSecretVersion(ctx, configID, "A", 1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(old.Value, []byte("a1")) {
		t.Fatalf("A v1 = %q, want a1", old.Value)
	}
}

func TestRollbackReusesCiphertext(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	_, configID := mkChain(t, s)

	if _, err := s.SetSecrets(ctx, configID, []SecretChange{{Key: "A", Value: []byte("a1")}}, "v1", "u"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetSecrets(ctx, configID, []SecretChange{{Key: "A", Value: []byte("a2")}}, "v2", "u"); err != nil {
		t.Fatal(err)
	}

	// Rollback to v1 → v3.
	rb, err := s.Rollback(ctx, configID, 1, "rollback", "u")
	if err != nil {
		t.Fatal(err)
	}
	if rb.Version != 3 {
		t.Fatalf("rollback version = %d, want 3", rb.Version)
	}
	// The rolled-back value still decrypts cleanly to a1 (ciphertext reused, no
	// re-encryption; AAD bound to value_version survives).
	got, err := s.GetSecret(ctx, configID, "A")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Value, []byte("a1")) || got.ValueVersion != 1 {
		t.Fatalf("A after rollback = %q v%d, want a1 v1", got.Value, got.ValueVersion)
	}
}
