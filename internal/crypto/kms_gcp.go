package crypto

import (
	"context"

	kmspb "cloud.google.com/go/kms/apiv1/kmspb"
	"github.com/googleapis/gax-go/v2"
)

// gcpKMSAPI is the subset of the GCP KMS SDK client this package uses.
// *kms.KeyManagementClient (cloud.google.com/go/kms/apiv1) satisfies it; tests
// substitute a fake.
type gcpKMSAPI interface {
	Encrypt(ctx context.Context, req *kmspb.EncryptRequest, opts ...gax.CallOption) (*kmspb.EncryptResponse, error)
	Decrypt(ctx context.Context, req *kmspb.DecryptRequest, opts ...gax.CallOption) (*kmspb.DecryptResponse, error)
}

// GCPKMSClient adapts the GCP KMS SDK to the KMSClient interface, pinned to a
// single symmetric (ENCRYPT_DECRYPT) crypto key.
type GCPKMSClient struct {
	api     gcpKMSAPI
	keyName string
}

// NewGCPKMSClient wraps a GCP KMS client (typically kms.NewKeyManagementClient)
// for use with KMSUnsealer. keyName is the crypto key resource name
// "projects/P/locations/L/keyRings/R/cryptoKeys/K"; the server uses the key's
// primary version for encryption and resolves the correct version on decrypt.
func NewGCPKMSClient(api gcpKMSAPI, keyName string) *GCPKMSClient {
	return &GCPKMSClient{api: api, keyName: keyName}
}

func (c *GCPKMSClient) Encrypt(ctx context.Context, plaintext []byte) ([]byte, error) {
	resp, err := c.api.Encrypt(ctx, &kmspb.EncryptRequest{
		Name:      c.keyName,
		Plaintext: plaintext,
	})
	if err != nil {
		return nil, err
	}
	// Return a fresh copy the caller owns and may zeroize.
	return append([]byte(nil), resp.GetCiphertext()...), nil
}

func (c *GCPKMSClient) Decrypt(ctx context.Context, ciphertext []byte) ([]byte, error) {
	resp, err := c.api.Decrypt(ctx, &kmspb.DecryptRequest{
		Name:       c.keyName,
		Ciphertext: ciphertext,
	})
	if err != nil {
		return nil, err
	}
	// Freshly allocated plaintext the caller owns and zeroizes.
	return append([]byte(nil), resp.GetPlaintext()...), nil
}
