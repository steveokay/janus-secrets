package crypto

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

// stubStore lets tests inject store failures.
type stubStore struct {
	cfg    *SealConfig
	getErr error
	putErr error
}

func (s *stubStore) Get(context.Context) (*SealConfig, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	if s.cfg == nil {
		return nil, ErrNoSealConfig
	}
	return s.cfg, nil
}

func (s *stubStore) Put(_ context.Context, cfg *SealConfig) error {
	if s.putErr != nil {
		return s.putErr
	}
	s.cfg = cfg
	return nil
}

func fileStore(t *testing.T) *FileSealConfigStore {
	t.Helper()
	return &FileSealConfigStore{Path: filepath.Join(t.TempDir(), "seal.json")}
}

func TestShamirInitAndUnseal(t *testing.T) {
	ctx := context.Background()
	store := fileStore(t)
	u := NewShamirUnsealer(store, 5, 3)

	res, err := u.Init(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Shares) != 5 {
		t.Fatalf("shares = %d, want 5", len(res.Shares))
	}
	cfg, err := store.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Type != SealTypeShamir || cfg.Threshold != 3 || cfg.Shares != 5 {
		t.Fatalf("persisted config: %+v", cfg)
	}

	// Unseal with shares 0, 2, 4 (any k of n works).
	u2 := NewShamirUnsealer(store, 0, 0)
	for _, i := range []int{0, 2, 4} {
		p, err := u2.SubmitShare(ctx, res.Shares[i])
		if err != nil {
			t.Fatal(err)
		}
		if p.Required != 3 {
			t.Fatalf("Required = %d, want 3", p.Required)
		}
	}
	master, err := u2.Unseal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(master) != KeySize {
		t.Fatalf("master size = %d, want %d", len(master), KeySize)
	}

	// The recovered master key actually drives a keyring.
	k := NewKeyring()
	if err := k.Unseal(master); err != nil {
		t.Fatal(err)
	}
	if _, err := k.WrapProjectKEK(testKey(0x0B), "p"); err != nil {
		t.Fatal(err)
	}
}

func TestShamirDefaults(t *testing.T) {
	ctx := context.Background()
	u := NewShamirUnsealer(fileStore(t), 0, 0)
	res, err := u.Init(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Shares) != DefaultShamirShares {
		t.Fatalf("shares = %d, want %d", len(res.Shares), DefaultShamirShares)
	}
}

func TestShamirInitFailures(t *testing.T) {
	ctx := context.Background()

	t.Run("already initialized", func(t *testing.T) {
		store := fileStore(t)
		u := NewShamirUnsealer(store, 5, 3)
		if _, err := u.Init(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := u.Init(ctx); !errors.Is(err, ErrAlreadyInitialized) {
			t.Fatalf("got %v, want ErrAlreadyInitialized", err)
		}
	})

	t.Run("store get error propagates", func(t *testing.T) {
		u := NewShamirUnsealer(&stubStore{getErr: errors.New("db down")}, 5, 3)
		if _, err := u.Init(ctx); err == nil {
			t.Fatal("want error, got nil")
		}
	})

	t.Run("invalid params rejected by split", func(t *testing.T) {
		u := NewShamirUnsealer(fileStore(t), 2, 3) // shares < threshold
		if _, err := u.Init(ctx); err == nil {
			t.Fatal("want error, got nil")
		}
	})

	t.Run("rand failure", func(t *testing.T) {
		restore := randReader
		randReader = failReader{}
		defer func() { randReader = restore }()
		u := NewShamirUnsealer(fileStore(t), 5, 3)
		if _, err := u.Init(ctx); err == nil {
			t.Fatal("want error, got nil")
		}
	})

	t.Run("kcv rand failure after keygen", func(t *testing.T) {
		restore := randReader
		randReader = &failAfterReader{n: 1} // keygen read succeeds, KCV nonce read fails
		defer func() { randReader = restore }()
		u := NewShamirUnsealer(fileStore(t), 5, 3)
		if _, err := u.Init(ctx); err == nil {
			t.Fatal("want error, got nil")
		}
	})

	t.Run("store put error propagates", func(t *testing.T) {
		u := NewShamirUnsealer(&stubStore{putErr: errors.New("disk full")}, 5, 3)
		if _, err := u.Init(ctx); err == nil {
			t.Fatal("want error, got nil")
		}
	})
}

func TestShamirSubmitShareFailures(t *testing.T) {
	ctx := context.Background()

	t.Run("before init", func(t *testing.T) {
		u := NewShamirUnsealer(fileStore(t), 5, 3)
		if _, err := u.SubmitShare(ctx, []byte{1, 2, 3}); !errors.Is(err, ErrNoSealConfig) {
			t.Fatalf("got %v, want ErrNoSealConfig", err)
		}
	})

	t.Run("wrong seal type", func(t *testing.T) {
		store := &stubStore{cfg: &SealConfig{Type: SealTypeAWSKMS}}
		u := NewShamirUnsealer(store, 5, 3)
		if _, err := u.SubmitShare(ctx, []byte{1, 2, 3}); !errors.Is(err, ErrInvalidSealConfig) {
			t.Fatalf("got %v, want ErrInvalidSealConfig", err)
		}
	})

	store := fileStore(t)
	u := NewShamirUnsealer(store, 5, 3)
	res, err := u.Init(ctx)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("too short", func(t *testing.T) {
		if _, err := u.SubmitShare(ctx, []byte{1}); !errors.Is(err, ErrInvalidShare) {
			t.Fatalf("got %v, want ErrInvalidShare", err)
		}
	})

	t.Run("duplicate", func(t *testing.T) {
		if _, err := u.SubmitShare(ctx, res.Shares[0]); err != nil {
			t.Fatal(err)
		}
		if _, err := u.SubmitShare(ctx, res.Shares[0]); !errors.Is(err, ErrDuplicateShare) {
			t.Fatalf("got %v, want ErrDuplicateShare", err)
		}
	})

	t.Run("progress counts", func(t *testing.T) {
		p, err := u.SubmitShare(ctx, res.Shares[1])
		if err != nil {
			t.Fatal(err)
		}
		if p.Submitted != 2 || p.Required != 3 {
			t.Fatalf("progress = %+v, want {2 3}", p)
		}
	})
}

func TestShamirUnsealFailures(t *testing.T) {
	ctx := context.Background()

	t.Run("before init", func(t *testing.T) {
		u := NewShamirUnsealer(fileStore(t), 5, 3)
		if _, err := u.Unseal(ctx); !errors.Is(err, ErrNoSealConfig) {
			t.Fatalf("got %v, want ErrNoSealConfig", err)
		}
	})

	store := fileStore(t)
	setup := NewShamirUnsealer(store, 5, 3)
	res, err := setup.Init(ctx)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("not enough shares", func(t *testing.T) {
		u := NewShamirUnsealer(store, 0, 0)
		if _, err := u.SubmitShare(ctx, res.Shares[0]); err != nil {
			t.Fatal(err)
		}
		if _, err := u.Unseal(ctx); !errors.Is(err, ErrNotEnoughShares) {
			t.Fatalf("got %v, want ErrNotEnoughShares", err)
		}
	})

	t.Run("tampered share fails key check", func(t *testing.T) {
		u := NewShamirUnsealer(store, 0, 0)
		bad := append([]byte(nil), res.Shares[0]...)
		bad[5] ^= 1
		for _, s := range [][]byte{bad, res.Shares[1], res.Shares[2]} {
			if _, err := u.SubmitShare(ctx, s); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := u.Unseal(ctx); !errors.Is(err, ErrKeyCheckFailed) {
			t.Fatalf("got %v, want ErrKeyCheckFailed", err)
		}
	})

	t.Run("combine error maps to ErrInvalidShare", func(t *testing.T) {
		// Distinct shares whose x-coordinate (last byte) collides make
		// shamir.Combine fail with a duplicate-part error.
		u := NewShamirUnsealer(store, 0, 0)
		crafted := [][]byte{{1, 2, 9}, {3, 4, 9}, {5, 6, 7}}
		for _, s := range crafted {
			if _, err := u.SubmitShare(ctx, s); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := u.Unseal(ctx); !errors.Is(err, ErrInvalidShare) {
			t.Fatalf("got %v, want ErrInvalidShare", err)
		}
	})

	t.Run("wrong-length reconstruction fails key check", func(t *testing.T) {
		// Valid distinct 3-byte shares combine into a 2-byte "secret",
		// which is not a 32-byte master key.
		u := NewShamirUnsealer(store, 0, 0)
		crafted := [][]byte{{1, 2, 1}, {3, 4, 2}, {5, 6, 3}}
		for _, s := range crafted {
			if _, err := u.SubmitShare(ctx, s); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := u.Unseal(ctx); !errors.Is(err, ErrKeyCheckFailed) {
			t.Fatalf("got %v, want ErrKeyCheckFailed", err)
		}
	})

	t.Run("state resets after successful unseal", func(t *testing.T) {
		u := NewShamirUnsealer(store, 0, 0)
		for _, i := range []int{0, 1, 2} {
			if _, err := u.SubmitShare(ctx, res.Shares[i]); err != nil {
				t.Fatal(err)
			}
		}
		master, err := u.Unseal(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(master) != KeySize {
			t.Fatalf("master size = %d, want %d", len(master), KeySize)
		}
		// Submitted shares were consumed; unsealing again needs new shares.
		if _, err := u.Unseal(ctx); !errors.Is(err, ErrNotEnoughShares) {
			t.Fatalf("got %v, want ErrNotEnoughShares", err)
		}
	})
}
