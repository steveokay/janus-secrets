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
