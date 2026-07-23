package crypto

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"
)

// azureKeysAPI is the subset of the Azure Key Vault azkeys SDK client this
// package uses. *azkeys.Client satisfies it; tests substitute a fake.
type azureKeysAPI interface {
	Encrypt(ctx context.Context, name, version string, parameters azkeys.KeyOperationParameters, options *azkeys.EncryptOptions) (azkeys.EncryptResponse, error)
	Decrypt(ctx context.Context, name, version string, parameters azkeys.KeyOperationParameters, options *azkeys.DecryptOptions) (azkeys.DecryptResponse, error)
}

// azureEncAlg is the wrapping algorithm. The master key is small (32 bytes),
// so an RSA key with OAEP-SHA256 wraps it in a single block. The Key Vault key
// must therefore be an RSA (or RSA-HSM) key.
const azureEncAlg = azkeys.EncryptionAlgorithmRSAOAEP256

// AzureKeyVaultClient adapts the Azure Key Vault azkeys SDK to the KMSClient
// interface, pinned to a single key (and optionally a specific version).
type AzureKeyVaultClient struct {
	api     azureKeysAPI
	keyName string
	// keyVersion is optional; empty means "current version" (the SDK resolves
	// the key's latest enabled version).
	keyVersion string
}

// NewAzureKeyVaultClient wraps an Azure Key Vault client (typically
// azkeys.NewClient(vaultURL, cred, nil)) for use with KMSUnsealer. keyName is
// the key name within the vault; keyVersion may be empty to use the current
// version.
func NewAzureKeyVaultClient(api azureKeysAPI, keyName, keyVersion string) *AzureKeyVaultClient {
	return &AzureKeyVaultClient{api: api, keyName: keyName, keyVersion: keyVersion}
}

func (c *AzureKeyVaultClient) Encrypt(ctx context.Context, plaintext []byte) ([]byte, error) {
	alg := azureEncAlg
	resp, err := c.api.Encrypt(ctx, c.keyName, c.keyVersion, azkeys.KeyOperationParameters{
		Algorithm: &alg,
		Value:     plaintext,
	}, nil)
	if err != nil {
		return nil, err
	}
	// Return a fresh copy the caller owns and may zeroize.
	return append([]byte(nil), resp.Result...), nil
}

func (c *AzureKeyVaultClient) Decrypt(ctx context.Context, ciphertext []byte) ([]byte, error) {
	alg := azureEncAlg
	resp, err := c.api.Decrypt(ctx, c.keyName, c.keyVersion, azkeys.KeyOperationParameters{
		Algorithm: &alg,
		Value:     ciphertext,
	}, nil)
	if err != nil {
		return nil, err
	}
	// Freshly allocated plaintext the caller owns and zeroizes.
	return append([]byte(nil), resp.Result...), nil
}
