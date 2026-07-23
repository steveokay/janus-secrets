package crypto

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"
)

// fakeAzureAPI implements azureKeysAPI for adapter tests.
type fakeAzureAPI struct {
	encResult []byte
	encErr    error
	decResult []byte
	decErr    error

	gotEncName, gotEncVersion string
	gotDecName, gotDecVersion string
	gotEncAlg, gotDecAlg      azkeys.EncryptionAlgorithm
}

func (f *fakeAzureAPI) Encrypt(_ context.Context, name, version string, p azkeys.KeyOperationParameters, _ *azkeys.EncryptOptions) (azkeys.EncryptResponse, error) {
	f.gotEncName, f.gotEncVersion = name, version
	if p.Algorithm != nil {
		f.gotEncAlg = *p.Algorithm
	}
	if f.encErr != nil {
		return azkeys.EncryptResponse{}, f.encErr
	}
	return azkeys.EncryptResponse{KeyOperationResult: azkeys.KeyOperationResult{Result: f.encResult}}, nil
}

func (f *fakeAzureAPI) Decrypt(_ context.Context, name, version string, p azkeys.KeyOperationParameters, _ *azkeys.DecryptOptions) (azkeys.DecryptResponse, error) {
	f.gotDecName, f.gotDecVersion = name, version
	if p.Algorithm != nil {
		f.gotDecAlg = *p.Algorithm
	}
	if f.decErr != nil {
		return azkeys.DecryptResponse{}, f.decErr
	}
	return azkeys.DecryptResponse{KeyOperationResult: azkeys.KeyOperationResult{Result: f.decResult}}, nil
}

func TestAzureKeyVaultClientAdapter(t *testing.T) {
	ctx := context.Background()

	t.Run("encrypt", func(t *testing.T) {
		api := &fakeAzureAPI{encResult: []byte("blob")}
		c := NewAzureKeyVaultClient(api, "mk", "v1")
		got, err := c.Encrypt(ctx, []byte("pt"))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, []byte("blob")) {
			t.Fatalf("ciphertext = %q", got)
		}
		if api.gotEncName != "mk" || api.gotEncVersion != "v1" {
			t.Fatalf("name/version = %q/%q", api.gotEncName, api.gotEncVersion)
		}
		if api.gotEncAlg != azkeys.EncryptionAlgorithmRSAOAEP256 {
			t.Fatalf("algorithm = %q", api.gotEncAlg)
		}
		got[0] = 'X'
		if api.encResult[0] == 'X' {
			t.Fatal("Encrypt returned a slice aliasing the KMS response")
		}
	})

	t.Run("encrypt error", func(t *testing.T) {
		c := NewAzureKeyVaultClient(&fakeAzureAPI{encErr: errors.New("denied")}, "mk", "")
		if _, err := c.Encrypt(ctx, []byte("pt")); err == nil {
			t.Fatal("want error, got nil")
		}
	})

	t.Run("decrypt", func(t *testing.T) {
		api := &fakeAzureAPI{decResult: []byte("pt")}
		c := NewAzureKeyVaultClient(api, "mk", "v1")
		got, err := c.Decrypt(ctx, []byte("blob"))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, []byte("pt")) {
			t.Fatalf("plaintext = %q", got)
		}
		if api.gotDecName != "mk" || api.gotDecVersion != "v1" {
			t.Fatalf("name/version = %q/%q", api.gotDecName, api.gotDecVersion)
		}
		if api.gotDecAlg != azkeys.EncryptionAlgorithmRSAOAEP256 {
			t.Fatalf("algorithm = %q", api.gotDecAlg)
		}
		got[0] = 'X'
		if api.decResult[0] == 'X' {
			t.Fatal("Decrypt returned a slice aliasing the KMS response")
		}
	})

	t.Run("decrypt error", func(t *testing.T) {
		c := NewAzureKeyVaultClient(&fakeAzureAPI{decErr: errors.New("denied")}, "mk", "")
		if _, err := c.Decrypt(ctx, []byte("blob")); err == nil {
			t.Fatal("want error, got nil")
		}
	})
}

// TestAzureKeyVaultUnsealRoundTrip drives the KMSUnsealer through the Azure
// adapter over a reversible in-memory fake, exercising the azurekv seal type.
func TestAzureKeyVaultUnsealRoundTrip(t *testing.T) {
	ctx := context.Background()
	client := NewAzureKeyVaultClient(&reversibleAzure{}, "mk", "")
	store := fileStore(t)
	u := NewKMSUnsealerFor(store, client, SealTypeAzureKV)

	if _, err := u.Init(ctx); err != nil {
		t.Fatal(err)
	}
	cfg, err := store.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Type != SealTypeAzureKV {
		t.Fatalf("seal type = %q, want %q", cfg.Type, SealTypeAzureKV)
	}
	master, err := u.Unseal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	k := NewKeyring()
	if err := k.Unseal(master); err != nil {
		t.Fatal(err)
	}
}

// reversibleAzure wraps/unwraps via an "az:" prefix so KMSUnsealer round-trips.
type reversibleAzure struct{}

func (r *reversibleAzure) Encrypt(_ context.Context, _, _ string, p azkeys.KeyOperationParameters, _ *azkeys.EncryptOptions) (azkeys.EncryptResponse, error) {
	return azkeys.EncryptResponse{KeyOperationResult: azkeys.KeyOperationResult{
		Result: append([]byte("az:"), p.Value...),
	}}, nil
}

func (r *reversibleAzure) Decrypt(_ context.Context, _, _ string, p azkeys.KeyOperationParameters, _ *azkeys.DecryptOptions) (azkeys.DecryptResponse, error) {
	return azkeys.DecryptResponse{KeyOperationResult: azkeys.KeyOperationResult{
		Result: bytes.TrimPrefix(p.Value, []byte("az:")),
	}}, nil
}
