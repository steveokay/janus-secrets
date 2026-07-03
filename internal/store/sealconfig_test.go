package store

import (
	"context"
	"errors"
	"testing"

	"github.com/steveokay/janus-secrets/internal/crypto"
)

func TestPostgresSealConfigStore(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	store := NewSealConfigStore(s)

	// Empty store.
	if _, err := store.Get(ctx); !errors.Is(err, crypto.ErrNoSealConfig) {
		t.Fatalf("empty: got %v, want ErrNoSealConfig", err)
	}

	// Put then Get.
	cfg := &crypto.SealConfig{
		Type:             crypto.SealTypeShamir,
		Threshold:        3,
		Shares:           5,
		KeyCheckValue:    []byte{1, 2, 3},
		WrappedMasterKey: []byte{9, 8, 7},
	}
	if err := store.Put(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != crypto.SealTypeShamir || got.Threshold != 3 || got.Shares != 5 ||
		string(got.KeyCheckValue) != "\x01\x02\x03" || string(got.WrappedMasterKey) != "\x09\x08\x07" {
		t.Fatalf("round trip mismatch: %+v", got)
	}

	// Put again overwrites the single row.
	cfg.Threshold = 2
	if err := store.Put(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	got, _ = store.Get(ctx)
	if got.Threshold != 2 {
		t.Fatalf("overwrite: threshold = %d, want 2", got.Threshold)
	}
}

// Compile-time check that we satisfy the crypto interface.
var _ crypto.SealConfigStore = (*PostgresSealConfigStore)(nil)
