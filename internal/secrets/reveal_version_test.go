package secrets

import (
	"context"
	"testing"
)

// TestRevealConfigVersion confirms that revealing a pinned version returns that
// version's plaintext, not the latest.
func TestRevealConfigVersion(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	_, cfg := mkChain(t, s)

	if _, err := s.SetSecrets(ctx, cfg, []SecretChange{{Key: "K", Value: []byte("v1")}}, "v1", "t"); err != nil {
		t.Fatalf("SetSecrets v1: %v", err)
	}
	if _, err := s.SetSecrets(ctx, cfg, []SecretChange{{Key: "K", Value: []byte("v2")}}, "v2", "t"); err != nil {
		t.Fatalf("SetSecrets v2: %v", err)
	}

	got, err := s.RevealConfigVersion(ctx, cfg, 1)
	if err != nil {
		t.Fatalf("RevealConfigVersion: %v", err)
	}
	sec, ok := got["K"]
	if !ok {
		t.Fatalf("key K missing from v1 reveal")
	}
	if string(sec.Value) != "v1" {
		t.Fatalf("v1 reveal = %q, want %q", string(sec.Value), "v1")
	}

	// And the latest still yields v2.
	latest, err := s.RevealConfigVersion(ctx, cfg, 2)
	if err != nil {
		t.Fatalf("RevealConfigVersion v2: %v", err)
	}
	if string(latest["K"].Value) != "v2" {
		t.Fatalf("v2 reveal = %q, want %q", string(latest["K"].Value), "v2")
	}
}
