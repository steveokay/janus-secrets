package secretsync

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
)

// smAPI is the subset of the Secrets Manager SDK client this provider uses.
// *secretsmanager.Client satisfies it; tests substitute a fake so no live AWS
// call is ever made.
type smAPI interface {
	PutSecretValue(ctx context.Context, in *secretsmanager.PutSecretValueInput, opts ...func(*secretsmanager.Options)) (*secretsmanager.PutSecretValueOutput, error)
	CreateSecret(ctx context.Context, in *secretsmanager.CreateSecretInput, opts ...func(*secretsmanager.Options)) (*secretsmanager.CreateSecretOutput, error)
	DeleteSecret(ctx context.Context, in *secretsmanager.DeleteSecretInput, opts ...func(*secretsmanager.Options)) (*secretsmanager.DeleteSecretOutput, error)
	// ListSecrets + GetSecretValue back drift verification (read-back).
	ListSecrets(ctx context.Context, in *secretsmanager.ListSecretsInput, opts ...func(*secretsmanager.Options)) (*secretsmanager.ListSecretsOutput, error)
	GetSecretValue(ctx context.Context, in *secretsmanager.GetSecretValueInput, opts ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error)
}

// awssecretsProvider writes a config's resolved secrets as individually-named
// secrets in AWS Secrets Manager under a path prefix. Unlike SSM standard
// parameters, Secrets Manager bills per-secret per-month — operators choose
// this provider deliberately. Janus supplies only static, explicit creds.
type awssecretsProvider struct {
	// newClient builds an smAPI from static creds + region (overridable in tests).
	newClient func(ctx context.Context, creds Creds, region string) (smAPI, error)
}

func (awssecretsProvider) Name() string { return ProviderAWSSecrets }

// defaultSMClient builds a Secrets Manager client from STATIC credentials only.
// Like the SSM provider, it never falls back to ambient env/instance-profile
// creds: a sync target's identity is explicit and must not silently borrow the
// host's AWS identity.
func defaultSMClient(ctx context.Context, creds Creds, region string) (smAPI, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			creds.AccessKeyID, creds.SecretAccessKey, creds.SessionToken)),
	)
	if err != nil {
		return nil, ErrInvalidConfig
	}
	return secretsmanager.NewFromConfig(cfg), nil
}

func (p awssecretsProvider) client(ctx context.Context, creds Creds, region string) (smAPI, error) {
	if p.newClient != nil {
		return p.newClient(ctx, creds, region)
	}
	return defaultSMClient(ctx, creds, region)
}

// secretID joins the prefix and key into a Secrets Manager secret name (single
// slash), mirroring the SSM path-join semantics.
func secretID(prefix, key string) string {
	return strings.TrimRight(prefix, "/") + "/" + key
}

// ── drift verification ───────────────────────────────────────────────────────
//
// Secrets Manager secrets are readable with GetSecretValue (the same identity
// already needs kms:Decrypt), so this provider supports real value drift
// detection. Names come from a prefix-filtered ListSecrets; values are fetched
// only for the keys Janus manages, so verification never pulls unrelated
// plaintext from the account.

func (awssecretsProvider) Capability() Capability { return CapValues }

// smPageMax bounds the ListSecrets pagination loop.
const smPageMax = 50

// Fetch lists secrets under the target's path prefix and reads back the values
// of the managed keys only.
func (p awssecretsProvider) Fetch(ctx context.Context, creds Creds, addr Addr, keys []string) (RemoteState, error) {
	if creds.AccessKeyID == "" || creds.SecretAccessKey == "" || addr.Region == "" || addr.PathPrefix == "" {
		return RemoteState{}, ErrInvalidConfig
	}
	cl, err := p.client(ctx, creds, addr.Region)
	if err != nil {
		return RemoteState{}, err
	}
	prefix := strings.TrimRight(addr.PathPrefix, "/") + "/"

	var names []string
	present := map[string]bool{}
	var token *string
	for page := 0; page < smPageMax; page++ {
		out, err := cl.ListSecrets(ctx, &secretsmanager.ListSecretsInput{
			Filters: []smtypes.Filter{{
				Key:    smtypes.FilterNameStringTypeName,
				Values: []string{prefix},
			}},
			NextToken: token,
		})
		if err != nil {
			return RemoteState{}, fmt.Errorf("%w: list secrets", ErrApplyFailed)
		}
		for _, entry := range out.SecretList {
			full := aws.ToString(entry.Name)
			key := strings.TrimPrefix(full, prefix)
			if key == "" || key == full || strings.Contains(key, "/") {
				continue // not a direct child of the managed prefix
			}
			names = append(names, key)
			present[key] = true
		}
		token = out.NextToken
		if token == nil {
			break
		}
	}

	values := map[string]string{}
	for _, k := range keys {
		if !present[k] {
			continue // absent → reported as missing by the engine
		}
		out, err := cl.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
			SecretId: aws.String(secretID(addr.PathPrefix, k)),
		})
		if err != nil {
			var nf *smtypes.ResourceNotFoundException
			if errors.As(err, &nf) {
				continue
			}
			return RemoteState{}, fmt.Errorf("%w: get secret value", ErrApplyFailed)
		}
		if out.SecretString == nil {
			continue // binary secret → present but unreadable as a string
		}
		values[k] = aws.ToString(out.SecretString)
	}
	return RemoteState{Names: names, Values: values}, nil
}

func (p awssecretsProvider) Apply(ctx context.Context, creds Creds, addr Addr, desired map[string]string,
	managedKeys []string, prune bool) (ApplyResult, error) {
	if creds.AccessKeyID == "" || creds.SecretAccessKey == "" || addr.Region == "" || addr.PathPrefix == "" {
		return ApplyResult{}, ErrInvalidConfig
	}
	cl, err := p.client(ctx, creds, addr.Region)
	if err != nil {
		return ApplyResult{}, err
	}

	res := ApplyResult{Skipped: map[string]string{}}
	for key, val := range desired {
		if err := p.upsert(ctx, cl, addr.PathPrefix, key, val); err != nil {
			return res, err
		}
		res.Applied = append(res.Applied, key)
	}

	if prune {
		desiredSet := map[string]bool{}
		for _, k := range res.Applied {
			desiredSet[k] = true
		}
		for _, k := range managedKeys {
			if desiredSet[k] {
				continue
			}
			// ForceDeleteWithoutRecovery: sync is a full mirror, so a pruned key
			// should not linger in a recovery window shadowing a later re-create
			// of the same name. Document this as the deliberate sync semantics.
			_, derr := cl.DeleteSecret(ctx, &secretsmanager.DeleteSecretInput{
				SecretId:                   aws.String(secretID(addr.PathPrefix, k)),
				ForceDeleteWithoutRecovery: aws.Bool(true),
			})
			if derr != nil {
				// A missing secret is an idempotent prune success.
				var nf *smtypes.ResourceNotFoundException
				if errors.As(derr, &nf) {
					continue
				}
				return res, fmt.Errorf("%w: delete secret", ErrApplyFailed)
			}
		}
	}
	return res, nil
}

// upsert writes a value: try PutSecretValue; on ResourceNotFoundException the
// secret doesn't exist yet → CreateSecret. AWS errors are sanitized to a
// value-free category (no ARN/account/value leak).
func (p awssecretsProvider) upsert(ctx context.Context, cl smAPI, prefix, key, val string) error {
	id := secretID(prefix, key)
	_, err := cl.PutSecretValue(ctx, &secretsmanager.PutSecretValueInput{
		SecretId:     aws.String(id),
		SecretString: aws.String(val),
	})
	if err == nil {
		return nil
	}
	var nf *smtypes.ResourceNotFoundException
	if errors.As(err, &nf) {
		if _, cerr := cl.CreateSecret(ctx, &secretsmanager.CreateSecretInput{
			Name:         aws.String(id),
			SecretString: aws.String(val),
		}); cerr != nil {
			return fmt.Errorf("%w: create secret", ErrApplyFailed)
		}
		return nil
	}
	return fmt.Errorf("%w: put secret", ErrApplyFailed)
}
