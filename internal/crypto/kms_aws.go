package crypto

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"
)

// AWSKMSAPI is the subset of the AWS KMS SDK client this package uses.
// *kms.Client satisfies it; tests substitute a fake.
type AWSKMSAPI interface {
	Encrypt(ctx context.Context, in *kms.EncryptInput, opts ...func(*kms.Options)) (*kms.EncryptOutput, error)
	Decrypt(ctx context.Context, in *kms.DecryptInput, opts ...func(*kms.Options)) (*kms.DecryptOutput, error)
}

// AWSKMSClient adapts the AWS SDK to the KMSClient interface, pinned to a
// single KMS key.
type AWSKMSClient struct {
	api   AWSKMSAPI
	keyID string
}

// NewAWSKMSClient wraps an AWS KMS client (typically kms.NewFromConfig(cfg))
// for use with KMSUnsealer. keyID is a key ID, ARN, or alias.
func NewAWSKMSClient(api AWSKMSAPI, keyID string) *AWSKMSClient {
	return &AWSKMSClient{api: api, keyID: keyID}
}

func (c *AWSKMSClient) Encrypt(ctx context.Context, plaintext []byte) ([]byte, error) {
	out, err := c.api.Encrypt(ctx, &kms.EncryptInput{
		KeyId:     aws.String(c.keyID),
		Plaintext: plaintext,
	})
	if err != nil {
		return nil, err
	}
	return out.CiphertextBlob, nil
}

func (c *AWSKMSClient) Decrypt(ctx context.Context, ciphertext []byte) ([]byte, error) {
	out, err := c.api.Decrypt(ctx, &kms.DecryptInput{
		// KeyId is optional for symmetric decrypt but pinning it prevents
		// decrypting blobs from an unexpected key.
		KeyId:          aws.String(c.keyID),
		CiphertextBlob: ciphertext,
	})
	if err != nil {
		return nil, err
	}
	return out.Plaintext, nil
}
