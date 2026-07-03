package crypto

import (
	"bytes"
	"context"
	"errors"
	"testing"

	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
)

// fakeKMS implements KMSClient with a reversible transform (prefix), plus
// injectable failures and response overrides.
type fakeKMS struct {
	encErr      error
	decErr      error
	decOverride []byte // if set, Decrypt returns this instead
}

func (f *fakeKMS) Encrypt(_ context.Context, plaintext []byte) ([]byte, error) {
	if f.encErr != nil {
		return nil, f.encErr
	}
	return append([]byte("kms:"), plaintext...), nil
}

func (f *fakeKMS) Decrypt(_ context.Context, ciphertext []byte) ([]byte, error) {
	if f.decErr != nil {
		return nil, f.decErr
	}
	if f.decOverride != nil {
		return f.decOverride, nil
	}
	return bytes.TrimPrefix(ciphertext, []byte("kms:")), nil
}

func TestKMSInitAndUnseal(t *testing.T) {
	ctx := context.Background()
	store := fileStore(t)
	u := NewKMSUnsealer(store, &fakeKMS{})

	res, err := u.Init(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Shares != nil {
		t.Fatal("KMS init must not return shares")
	}
	cfg, err := store.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Type != SealTypeAWSKMS || len(cfg.WrappedMasterKey) == 0 || len(cfg.KeyCheckValue) == 0 {
		t.Fatalf("persisted config: %+v", cfg)
	}

	master, err := u.Unseal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	k := NewKeyring()
	if err := k.Unseal(master); err != nil {
		t.Fatal(err)
	}
	if _, err := k.WrapProjectKEK(testKey(0x0B), "p"); err != nil {
		t.Fatal(err)
	}
}

func TestKMSInitFailures(t *testing.T) {
	ctx := context.Background()

	t.Run("already initialized", func(t *testing.T) {
		store := fileStore(t)
		u := NewKMSUnsealer(store, &fakeKMS{})
		if _, err := u.Init(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := u.Init(ctx); !errors.Is(err, ErrAlreadyInitialized) {
			t.Fatalf("got %v, want ErrAlreadyInitialized", err)
		}
	})

	t.Run("store get error", func(t *testing.T) {
		u := NewKMSUnsealer(&stubStore{getErr: errors.New("db down")}, &fakeKMS{})
		if _, err := u.Init(ctx); err == nil {
			t.Fatal("want error, got nil")
		}
	})

	t.Run("rand failure", func(t *testing.T) {
		restore := randReader
		randReader = failReader{}
		defer func() { randReader = restore }()
		u := NewKMSUnsealer(fileStore(t), &fakeKMS{})
		if _, err := u.Init(ctx); err == nil {
			t.Fatal("want error, got nil")
		}
	})

	t.Run("kms encrypt failure", func(t *testing.T) {
		u := NewKMSUnsealer(fileStore(t), &fakeKMS{encErr: errors.New("kms down")})
		if _, err := u.Init(ctx); err == nil {
			t.Fatal("want error, got nil")
		}
	})

	t.Run("kcv rand failure after keygen", func(t *testing.T) {
		restore := randReader
		randReader = &failAfterReader{n: 1}
		defer func() { randReader = restore }()
		u := NewKMSUnsealer(fileStore(t), &fakeKMS{})
		if _, err := u.Init(ctx); err == nil {
			t.Fatal("want error, got nil")
		}
	})

	t.Run("store put error", func(t *testing.T) {
		u := NewKMSUnsealer(&stubStore{putErr: errors.New("disk full")}, &fakeKMS{})
		if _, err := u.Init(ctx); err == nil {
			t.Fatal("want error, got nil")
		}
	})
}

func TestKMSUnsealFailures(t *testing.T) {
	ctx := context.Background()

	t.Run("before init", func(t *testing.T) {
		u := NewKMSUnsealer(fileStore(t), &fakeKMS{})
		if _, err := u.Unseal(ctx); !errors.Is(err, ErrNoSealConfig) {
			t.Fatalf("got %v, want ErrNoSealConfig", err)
		}
	})

	t.Run("wrong seal type", func(t *testing.T) {
		store := &stubStore{cfg: &SealConfig{Type: SealTypeShamir}}
		u := NewKMSUnsealer(store, &fakeKMS{})
		if _, err := u.Unseal(ctx); !errors.Is(err, ErrInvalidSealConfig) {
			t.Fatalf("got %v, want ErrInvalidSealConfig", err)
		}
	})

	// Shared initialized store for the remaining cases.
	store := fileStore(t)
	if _, err := NewKMSUnsealer(store, &fakeKMS{}).Init(ctx); err != nil {
		t.Fatal(err)
	}

	t.Run("kms decrypt failure", func(t *testing.T) {
		u := NewKMSUnsealer(store, &fakeKMS{decErr: errors.New("denied")})
		if _, err := u.Unseal(ctx); err == nil {
			t.Fatal("want error, got nil")
		}
	})

	t.Run("wrong-length master", func(t *testing.T) {
		u := NewKMSUnsealer(store, &fakeKMS{decOverride: []byte("short")})
		if _, err := u.Unseal(ctx); !errors.Is(err, ErrKeyCheckFailed) {
			t.Fatalf("got %v, want ErrKeyCheckFailed", err)
		}
	})

	t.Run("wrong master fails key check", func(t *testing.T) {
		u := NewKMSUnsealer(store, &fakeKMS{decOverride: testKey(0xEE)})
		if _, err := u.Unseal(ctx); !errors.Is(err, ErrKeyCheckFailed) {
			t.Fatalf("got %v, want ErrKeyCheckFailed", err)
		}
	})
}

// fakeAWSAPI implements AWSKMSAPI for adapter tests.
type fakeAWSAPI struct {
	encOut *awskms.EncryptOutput
	encErr error
	decOut *awskms.DecryptOutput
	decErr error

	gotKeyID string
}

func (f *fakeAWSAPI) Encrypt(_ context.Context, in *awskms.EncryptInput, _ ...func(*awskms.Options)) (*awskms.EncryptOutput, error) {
	if in.KeyId != nil {
		f.gotKeyID = *in.KeyId
	}
	return f.encOut, f.encErr
}

func (f *fakeAWSAPI) Decrypt(_ context.Context, in *awskms.DecryptInput, _ ...func(*awskms.Options)) (*awskms.DecryptOutput, error) {
	if in.KeyId != nil {
		f.gotKeyID = *in.KeyId
	}
	return f.decOut, f.decErr
}

func TestAWSKMSClientAdapter(t *testing.T) {
	ctx := context.Background()

	t.Run("encrypt", func(t *testing.T) {
		api := &fakeAWSAPI{encOut: &awskms.EncryptOutput{CiphertextBlob: []byte("blob")}}
		c := NewAWSKMSClient(api, "key-arn")
		got, err := c.Encrypt(ctx, []byte("pt"))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, []byte("blob")) || api.gotKeyID != "key-arn" {
			t.Fatalf("got %q, keyID %q", got, api.gotKeyID)
		}
	})

	t.Run("encrypt error", func(t *testing.T) {
		c := NewAWSKMSClient(&fakeAWSAPI{encErr: errors.New("denied")}, "key-arn")
		if _, err := c.Encrypt(ctx, []byte("pt")); err == nil {
			t.Fatal("want error, got nil")
		}
	})

	t.Run("decrypt", func(t *testing.T) {
		api := &fakeAWSAPI{decOut: &awskms.DecryptOutput{Plaintext: []byte("pt")}}
		c := NewAWSKMSClient(api, "key-arn")
		got, err := c.Decrypt(ctx, []byte("blob"))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, []byte("pt")) || api.gotKeyID != "key-arn" {
			t.Fatalf("got %q, keyID %q", got, api.gotKeyID)
		}
	})

	t.Run("decrypt error", func(t *testing.T) {
		c := NewAWSKMSClient(&fakeAWSAPI{decErr: errors.New("denied")}, "key-arn")
		if _, err := c.Decrypt(ctx, []byte("blob")); err == nil {
			t.Fatal("want error, got nil")
		}
	})
}
