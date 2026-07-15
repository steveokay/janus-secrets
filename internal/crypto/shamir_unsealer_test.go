package crypto

import (
	"bytes"
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

	t.Run("Reset recovers from a poisoned share", func(t *testing.T) {
		u := NewShamirUnsealer(store, 0, 0)
		// A tampered share poisons every reconstruction attempt.
		bad := append([]byte(nil), res.Shares[0]...)
		bad[5] ^= 1
		for _, s := range [][]byte{bad, res.Shares[1], res.Shares[2]} {
			if _, err := u.SubmitShare(ctx, s); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := u.Unseal(ctx); !errors.Is(err, ErrKeyCheckFailed) {
			t.Fatalf("poisoned: got %v, want ErrKeyCheckFailed", err)
		}
		// Reset clears the accumulated shares; resubmitting good ones works.
		u.Reset()
		for _, i := range []int{0, 1, 2} {
			if _, err := u.SubmitShare(ctx, res.Shares[i]); err != nil {
				t.Fatal(err)
			}
		}
		master, err := u.Unseal(ctx)
		if err != nil {
			t.Fatalf("after Reset: %v", err)
		}
		if len(master) != KeySize {
			t.Fatalf("master size = %d, want %d", len(master), KeySize)
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

func TestShamirSubmittedSharesAccessor(t *testing.T) {
	ctx := context.Background()
	store := fileStore(t)
	u := NewShamirUnsealer(store, 0, 0)
	res, err := u.Init(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := u.SubmittedShares(); got != 0 {
		t.Fatalf("SubmittedShares before submit = %d, want 0", got)
	}
	if _, err := u.SubmitShare(ctx, res.Shares[0]); err != nil {
		t.Fatal(err)
	}
	if got := u.SubmittedShares(); got != 1 {
		t.Fatalf("SubmittedShares after one submit = %d, want 1", got)
	}
}

func TestShamirReconstructAndVerify(t *testing.T) {
	st := fileStore(t)
	u := NewShamirUnsealer(st, 5, 3)
	res, err := u.Init(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := st.Get(context.Background())

	master, err := ReconstructAndVerifyShamir(cfg, res.Shares[:3])
	if err != nil {
		t.Fatalf("verify with 3 shares: %v", err)
	}
	if len(master) != KeySize {
		t.Fatal("bad master length")
	}
	zero(master)

	if _, err := ReconstructAndVerifyShamir(cfg, res.Shares[:2]); !errors.Is(err, ErrNotEnoughShares) {
		t.Fatalf("want ErrNotEnoughShares, got %v", err)
	}
	bad := [][]byte{res.Shares[0], res.Shares[1]}
	junk := append([]byte(nil), res.Shares[2]...)
	junk[len(junk)-1] ^= 0xFF
	bad = append(bad, junk)
	if _, err := ReconstructAndVerifyShamir(cfg, bad); err == nil {
		t.Fatal("wrong share passed verification")
	}
}

func TestReconstructAndVerifyShamirBranches(t *testing.T) {
	master := testKey(0x44)
	kcv, err := makeKCV(master)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("nil cfg", func(t *testing.T) {
		if _, err := ReconstructAndVerifyShamir(nil, [][]byte{master}); !errors.Is(err, ErrInvalidSealConfig) {
			t.Fatalf("got %v, want ErrInvalidSealConfig", err)
		}
	})

	t.Run("wrong seal type", func(t *testing.T) {
		cfg := &SealConfig{Type: SealTypeAWSKMS, Threshold: 1}
		if _, err := ReconstructAndVerifyShamir(cfg, [][]byte{master}); !errors.Is(err, ErrInvalidSealConfig) {
			t.Fatalf("got %v, want ErrInvalidSealConfig", err)
		}
	})

	t.Run("threshold 1 wrong share count", func(t *testing.T) {
		cfg := &SealConfig{Type: SealTypeShamir, Threshold: 1, Shares: 1, KeyCheckValue: kcv}
		two := [][]byte{master, master}
		if _, err := ReconstructAndVerifyShamir(cfg, two); !errors.Is(err, ErrInvalidShare) {
			t.Fatalf("got %v, want ErrInvalidShare", err)
		}
	})

	t.Run("threshold 1 success", func(t *testing.T) {
		cfg := &SealConfig{Type: SealTypeShamir, Threshold: 1, Shares: 1, KeyCheckValue: kcv}
		got, err := ReconstructAndVerifyShamir(cfg, [][]byte{master})
		if err != nil {
			t.Fatalf("threshold-1 verify: %v", err)
		}
		if !bytes.Equal(got, master) {
			t.Fatal("threshold-1 reconstruction mismatch")
		}
		zero(got)
	})

	t.Run("threshold 1 wrong-length share fails key check", func(t *testing.T) {
		cfg := &SealConfig{Type: SealTypeShamir, Threshold: 1, Shares: 1, KeyCheckValue: kcv}
		short := []byte("not-32-bytes")
		if _, err := ReconstructAndVerifyShamir(cfg, [][]byte{short}); !errors.Is(err, ErrKeyCheckFailed) {
			t.Fatalf("got %v, want ErrKeyCheckFailed", err)
		}
	})

	t.Run("threshold 1 well-formed wrong share fails KCV", func(t *testing.T) {
		cfg := &SealConfig{Type: SealTypeShamir, Threshold: 1, Shares: 1, KeyCheckValue: kcv}
		if _, err := ReconstructAndVerifyShamir(cfg, [][]byte{testKey(0xEE)}); !errors.Is(err, ErrKeyCheckFailed) {
			t.Fatalf("got %v, want ErrKeyCheckFailed", err)
		}
	})

	t.Run("combine error maps to ErrInvalidShare", func(t *testing.T) {
		// Distinct shares whose x-coordinate (last byte) collides make Combine fail.
		cfg := &SealConfig{Type: SealTypeShamir, Threshold: 3, Shares: 5, KeyCheckValue: kcv}
		crafted := [][]byte{{1, 2, 9}, {3, 4, 9}, {5, 6, 7}}
		if _, err := ReconstructAndVerifyShamir(cfg, crafted); !errors.Is(err, ErrInvalidShare) {
			t.Fatalf("got %v, want ErrInvalidShare", err)
		}
	})

	t.Run("combine wrong-length reconstruction fails key check", func(t *testing.T) {
		// Valid distinct 3-byte shares combine into a 2-byte secret, not 32 bytes.
		cfg := &SealConfig{Type: SealTypeShamir, Threshold: 3, Shares: 5, KeyCheckValue: kcv}
		crafted := [][]byte{{1, 2, 1}, {3, 4, 2}, {5, 6, 3}}
		if _, err := ReconstructAndVerifyShamir(cfg, crafted); !errors.Is(err, ErrKeyCheckFailed) {
			t.Fatalf("got %v, want ErrKeyCheckFailed", err)
		}
	})
}

func TestShamirReseal(t *testing.T) {
	st := fileStore(t)
	u := NewShamirUnsealer(st, 5, 3)
	if _, err := u.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	m2, _ := GenerateKey()
	cfg, shares, err := u.Reseal(context.Background(), m2)
	if err != nil {
		t.Fatalf("Reseal: %v", err)
	}
	if cfg.Type != SealTypeShamir || cfg.Threshold != 3 || cfg.Shares != 5 {
		t.Fatalf("shape not preserved: %+v", cfg)
	}
	if len(shares) != 5 {
		t.Fatalf("want 5 new shares, got %d", len(shares))
	}
	got, err := ReconstructAndVerifyShamir(cfg, shares[:3])
	if err != nil {
		t.Fatalf("verify new shares: %v", err)
	}
	if !bytes.Equal(got, m2) {
		t.Fatal("resealed shares do not reconstruct M2")
	}
}

func TestShamirResealFailures(t *testing.T) {
	ctx := context.Background()

	t.Run("loadConfig error propagates", func(t *testing.T) {
		u := NewShamirUnsealer(&stubStore{getErr: errors.New("db down")}, 5, 3)
		if _, _, err := u.Reseal(ctx, testKey(0x22)); err == nil {
			t.Fatal("want error, got nil")
		}
	})

	t.Run("invalid new key size", func(t *testing.T) {
		st := fileStore(t)
		if _, err := NewShamirUnsealer(st, 5, 3).Init(ctx); err != nil {
			t.Fatal(err)
		}
		u := NewShamirUnsealer(st, 5, 3)
		if _, _, err := u.Reseal(ctx, []byte("short")); !errors.Is(err, ErrInvalidKeySize) {
			t.Fatalf("got %v, want ErrInvalidKeySize", err)
		}
	})

	t.Run("split error from bad stored shape", func(t *testing.T) {
		// A stored config whose threshold exceeds shares (but shares > 1, so the
		// 1-of-1 fast path is skipped) makes shamir.Split fail.
		store := &stubStore{cfg: &SealConfig{Type: SealTypeShamir, Shares: 2, Threshold: 3}}
		u := NewShamirUnsealer(store, 0, 0)
		if _, _, err := u.Reseal(ctx, testKey(0x22)); err == nil {
			t.Fatal("want split error, got nil")
		}
	})

	t.Run("kcv rand failure on 1-of-1 path", func(t *testing.T) {
		// The 1-of-1 branch reads no randomness before makeKCV, so makeKCV's
		// nonce read is the first rand read — fail it outright.
		store := &stubStore{cfg: &SealConfig{Type: SealTypeShamir, Shares: 1, Threshold: 1}}
		u := NewShamirUnsealer(store, 0, 0)
		restore := randReader
		randReader = failReader{}
		defer func() { randReader = restore }()
		if _, _, err := u.Reseal(ctx, testKey(0x22)); err == nil {
			t.Fatal("want error, got nil")
		}
	})
}

func TestShamirResealOneOfOne(t *testing.T) {
	ctx := context.Background()
	store := &stubStore{cfg: &SealConfig{Type: SealTypeShamir, Shares: 1, Threshold: 1}}
	u := NewShamirUnsealer(store, 0, 0)

	m2 := testKey(0x33)
	cfg, parts, err := u.Reseal(ctx, m2)
	if err != nil {
		t.Fatalf("Reseal 1-of-1: %v", err)
	}
	if cfg.Type != SealTypeShamir || cfg.Shares != 1 || cfg.Threshold != 1 {
		t.Fatalf("shape not preserved: %+v", cfg)
	}
	if len(parts) != 1 || !bytes.Equal(parts[0], m2) {
		t.Fatal("1-of-1 reseal share must be the master key itself")
	}
	if err := verifyKCV(m2, cfg.KeyCheckValue); err != nil {
		t.Fatalf("KCV: %v", err)
	}
}

func TestShamirOneOfOne(t *testing.T) {
	ctx := context.Background()
	store := fileStore(t)
	u := NewShamirUnsealer(store, 1, 1)

	res, err := u.Init(ctx)
	if err != nil {
		t.Fatalf("1-of-1 Init: %v", err)
	}
	if len(res.Shares) != 1 || len(res.Shares[0]) != KeySize {
		t.Fatalf("1-of-1 shares: n=%d len=%d, want 1 share of KeySize", len(res.Shares), len(res.Shares[0]))
	}

	// The single share unseals (and KCV verifies it).
	share := append([]byte(nil), res.Shares[0]...)
	if _, err := u.SubmitShare(ctx, share); err != nil {
		t.Fatal(err)
	}
	if got := u.SubmittedShares(); got != 1 {
		t.Fatalf("SubmittedShares after submit = %d, want 1", got)
	}
	master, err := u.Unseal(ctx)
	if err != nil {
		t.Fatalf("1-of-1 Unseal: %v", err)
	}
	if len(master) != KeySize {
		t.Fatalf("master len = %d", len(master))
	}
	zero(master)

	// A wrong single share fails the KCV, not silently succeeds.
	u2 := NewShamirUnsealer(store, 1, 1)
	wrong := testKey(0xEE)
	if _, err := u2.SubmitShare(ctx, wrong); err != nil {
		t.Fatal(err)
	}
	if _, err := u2.Unseal(ctx); !errors.Is(err, ErrKeyCheckFailed) {
		t.Fatalf("wrong 1-of-1 share: got %v, want ErrKeyCheckFailed", err)
	}

	// More than one submitted share for a threshold-1 seal is ambiguous and
	// fails closed deterministically, regardless of map iteration order.
	if _, err := u2.SubmitShare(ctx, res.Shares[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := u2.Unseal(ctx); !errors.Is(err, ErrInvalidShare) {
		t.Fatalf("two 1-of-1 shares: got %v, want ErrInvalidShare", err)
	}
	// Reset + exactly one correct share recovers.
	u2.Reset()
	if _, err := u2.SubmitShare(ctx, res.Shares[0]); err != nil {
		t.Fatal(err)
	}
	master2, err := u2.Unseal(ctx)
	if err != nil {
		t.Fatalf("after Reset with correct share: %v", err)
	}
	if len(master2) != KeySize {
		t.Fatalf("master len = %d", len(master2))
	}
	zero(master2)
}
