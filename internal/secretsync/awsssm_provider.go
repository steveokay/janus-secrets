package secretsync

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// ssmAPI is the subset of the SSM SDK client this provider uses. *ssm.Client
// satisfies it; tests substitute a fake so no live AWS call is ever made.
type ssmAPI interface {
	PutParameter(ctx context.Context, in *ssm.PutParameterInput, opts ...func(*ssm.Options)) (*ssm.PutParameterOutput, error)
	DeleteParameters(ctx context.Context, in *ssm.DeleteParametersInput, opts ...func(*ssm.Options)) (*ssm.DeleteParametersOutput, error)
}

// awsssmProvider writes a config's resolved secrets as SecureString parameters
// under a path prefix in AWS SSM Parameter Store. SSM encrypts SecureString
// values with the account KMS key; Janus supplies only static, explicit creds.
type awsssmProvider struct {
	// newClient builds an ssmAPI from static creds + region (overridable in tests).
	newClient func(ctx context.Context, creds Creds, region string) (ssmAPI, error)
}

func (awsssmProvider) Name() string { return ProviderAWSSSM }

// defaultSSMClient builds an SSM client from STATIC credentials only. It never
// falls back to ambient env/instance-profile creds: a sync target's identity is
// explicit and must not silently borrow the host's AWS identity.
func defaultSSMClient(ctx context.Context, creds Creds, region string) (ssmAPI, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			creds.AccessKeyID, creds.SecretAccessKey, creds.SessionToken)),
	)
	if err != nil {
		return nil, ErrInvalidConfig
	}
	return ssm.NewFromConfig(cfg), nil
}

func (p awsssmProvider) client(ctx context.Context, creds Creds, region string) (ssmAPI, error) {
	if p.newClient != nil {
		return p.newClient(ctx, creds, region)
	}
	return defaultSSMClient(ctx, creds, region)
}

// paramName joins the prefix and key into an SSM parameter path (single slash).
func paramName(prefix, key string) string {
	return strings.TrimRight(prefix, "/") + "/" + key
}

func (p awsssmProvider) Apply(ctx context.Context, creds Creds, addr Addr, desired map[string]string,
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
		_, err := cl.PutParameter(ctx, &ssm.PutParameterInput{
			Name:      aws.String(paramName(addr.PathPrefix, key)),
			Value:     aws.String(val),
			Type:      ssmtypes.ParameterTypeSecureString,
			Overwrite: aws.Bool(true),
		})
		if err != nil {
			// Sanitize: never echo the AWS error (may carry ARN/account id).
			return res, fmt.Errorf("%w: put parameter", ErrApplyFailed)
		}
		res.Applied = append(res.Applied, key)
	}

	if prune {
		desiredSet := map[string]bool{}
		for _, k := range res.Applied {
			desiredSet[k] = true
		}
		var toDelete []string
		for _, k := range managedKeys {
			if !desiredSet[k] {
				toDelete = append(toDelete, paramName(addr.PathPrefix, k))
			}
		}
		// DeleteParameters caps at 10 names per call; batch accordingly.
		for start := 0; start < len(toDelete); start += 10 {
			end := start + 10
			if end > len(toDelete) {
				end = len(toDelete)
			}
			if _, err := cl.DeleteParameters(ctx, &ssm.DeleteParametersInput{
				Names: toDelete[start:end],
			}); err != nil {
				return res, fmt.Errorf("%w: delete parameters", ErrApplyFailed)
			}
		}
	}
	return res, nil
}
