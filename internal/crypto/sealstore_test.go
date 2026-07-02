package crypto

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFileSealConfigStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := &FileSealConfigStore{Path: filepath.Join(t.TempDir(), "seal.json")}

	if _, err := store.Get(ctx); !errors.Is(err, ErrNoSealConfig) {
		t.Fatalf("empty store: got %v, want ErrNoSealConfig", err)
	}

	cfg := &SealConfig{
		Type:          SealTypeShamir,
		Threshold:     3,
		Shares:        5,
		KeyCheckValue: []byte{1, 2, 3},
	}
	if err := store.Put(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != cfg.Type || got.Threshold != 3 || got.Shares != 5 || len(got.KeyCheckValue) != 3 {
		t.Fatalf("round trip mismatch: %+v", got)
	}

	// File must be private.
	info, err := os.Stat(store.Path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 && os.PathSeparator == '/' {
		t.Fatalf("seal config file is group/world accessible: %v", perm)
	}
}

func TestFileSealConfigStoreErrors(t *testing.T) {
	ctx := context.Background()

	// Get: path exists but is unreadable as a config file (it's a directory).
	dir := t.TempDir()
	store := &FileSealConfigStore{Path: dir}
	if _, err := store.Get(ctx); err == nil {
		t.Fatal("Get on directory: want error, got nil")
	}

	// Get: corrupted JSON.
	badPath := filepath.Join(t.TempDir(), "seal.json")
	if err := os.WriteFile(badPath, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (&FileSealConfigStore{Path: badPath}).Get(ctx); err == nil {
		t.Fatal("corrupt JSON: want error, got nil")
	}

	// Put: parent directory does not exist.
	missing := &FileSealConfigStore{Path: filepath.Join(t.TempDir(), "nope", "seal.json")}
	if err := missing.Put(ctx, &SealConfig{Type: SealTypeShamir}); err == nil {
		t.Fatal("missing parent dir: want error, got nil")
	}

	// Put: rename target is an existing directory.
	if err := store.Put(ctx, &SealConfig{Type: SealTypeShamir}); err == nil {
		t.Fatal("rename onto directory: want error, got nil")
	}

	// Put: marshal failure (injected).
	restore := marshalSealConfig
	marshalSealConfig = func(any) ([]byte, error) { return nil, errors.New("boom") }
	defer func() { marshalSealConfig = restore }()
	ok := &FileSealConfigStore{Path: filepath.Join(t.TempDir(), "seal.json")}
	if err := ok.Put(ctx, &SealConfig{Type: SealTypeShamir}); err == nil {
		t.Fatal("marshal failure: want error, got nil")
	}
}
