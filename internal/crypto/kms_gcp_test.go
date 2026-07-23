package crypto

import (
	"bytes"
	"context"
	"errors"
	"testing"

	kmspb "cloud.google.com/go/kms/apiv1/kmspb"
	"github.com/googleapis/gax-go/v2"
)

// fakeGCPAPI implements gcpKMSAPI for adapter tests.
type fakeGCPAPI struct {
	encResp *kmspb.EncryptResponse
	encErr  error
	decResp *kmspb.DecryptResponse
	decErr  error

	gotEncName string
	gotDecName string
}

func (f *fakeGCPAPI) Encrypt(_ context.Context, req *kmspb.EncryptRequest, _ ...gax.CallOption) (*kmspb.EncryptResponse, error) {
	f.gotEncName = req.GetName()
	return f.encResp, f.encErr
}

func (f *fakeGCPAPI) Decrypt(_ context.Context, req *kmspb.DecryptRequest, _ ...gax.CallOption) (*kmspb.DecryptResponse, error) {
	f.gotDecName = req.GetName()
	return f.decResp, f.decErr
}

const gcpTestKey = "projects/p/locations/l/keyRings/r/cryptoKeys/k"

func TestGCPKMSClientAdapter(t *testing.T) {
	ctx := context.Background()

	t.Run("encrypt", func(t *testing.T) {
		api := &fakeGCPAPI{encResp: &kmspb.EncryptResponse{Ciphertext: []byte("blob")}}
		c := NewGCPKMSClient(api, gcpTestKey)
		got, err := c.Encrypt(ctx, []byte("pt"))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, []byte("blob")) || api.gotEncName != gcpTestKey {
			t.Fatalf("got %q, name %q", got, api.gotEncName)
		}
		// Returned slice must be freshly allocated, not aliasing the response.
		got[0] = 'X'
		if api.encResp.Ciphertext[0] == 'X' {
			t.Fatal("Encrypt returned a slice aliasing the KMS response")
		}
	})

	t.Run("encrypt error", func(t *testing.T) {
		c := NewGCPKMSClient(&fakeGCPAPI{encErr: errors.New("denied")}, gcpTestKey)
		if _, err := c.Encrypt(ctx, []byte("pt")); err == nil {
			t.Fatal("want error, got nil")
		}
	})

	t.Run("decrypt", func(t *testing.T) {
		api := &fakeGCPAPI{decResp: &kmspb.DecryptResponse{Plaintext: []byte("pt")}}
		c := NewGCPKMSClient(api, gcpTestKey)
		got, err := c.Decrypt(ctx, []byte("blob"))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, []byte("pt")) || api.gotDecName != gcpTestKey {
			t.Fatalf("got %q, name %q", got, api.gotDecName)
		}
		// Returned plaintext must be freshly allocated (caller zeroizes).
		got[0] = 'X'
		if api.decResp.Plaintext[0] == 'X' {
			t.Fatal("Decrypt returned a slice aliasing the KMS response")
		}
	})

	t.Run("decrypt error", func(t *testing.T) {
		c := NewGCPKMSClient(&fakeGCPAPI{decErr: errors.New("denied")}, gcpTestKey)
		if _, err := c.Decrypt(ctx, []byte("blob")); err == nil {
			t.Fatal("want error, got nil")
		}
	})
}

// TestGCPKMSUnsealRoundTrip drives the provider-agnostic KMSUnsealer through
// the GCP adapter over a reversible in-memory fake, exercising the gcpkms seal
// type end to end.
func TestGCPKMSUnsealRoundTrip(t *testing.T) {
	ctx := context.Background()
	api := &reversibleGCP{}
	client := NewGCPKMSClient(api, gcpTestKey)
	store := fileStore(t)
	u := NewKMSUnsealerFor(store, client, SealTypeGCPKMS)

	if _, err := u.Init(ctx); err != nil {
		t.Fatal(err)
	}
	cfg, err := store.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Type != SealTypeGCPKMS {
		t.Fatalf("seal type = %q, want %q", cfg.Type, SealTypeGCPKMS)
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

// reversibleGCP wraps/unwraps via a "gcp:" prefix so KMSUnsealer round-trips.
type reversibleGCP struct{}

func (r *reversibleGCP) Encrypt(_ context.Context, req *kmspb.EncryptRequest, _ ...gax.CallOption) (*kmspb.EncryptResponse, error) {
	return &kmspb.EncryptResponse{Ciphertext: append([]byte("gcp:"), req.GetPlaintext()...)}, nil
}

func (r *reversibleGCP) Decrypt(_ context.Context, req *kmspb.DecryptRequest, _ ...gax.CallOption) (*kmspb.DecryptResponse, error) {
	return &kmspb.DecryptResponse{Plaintext: bytes.TrimPrefix(req.GetCiphertext(), []byte("gcp:"))}, nil
}
