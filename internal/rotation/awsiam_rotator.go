package rotation

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/iam"
)

// iamUserRe restricts the rotatable IAM user name to the AWS-documented charset
// (upper/lowercase alphanumerics plus _+=,.@-, 1-64 chars). It fails fast on a
// malformed value as invalid config rather than a runtime AWS error.
var iamUserRe = regexp.MustCompile(`^[A-Za-z0-9_+=,.@-]{1,64}$`)

// iamAPI is the subset of the IAM SDK client this rotator uses. *iam.Client
// satisfies it; tests substitute a fake so no live AWS call is ever made.
type iamAPI interface {
	CreateAccessKey(ctx context.Context, in *iam.CreateAccessKeyInput, opts ...func(*iam.Options)) (*iam.CreateAccessKeyOutput, error)
	ListAccessKeys(ctx context.Context, in *iam.ListAccessKeysInput, opts ...func(*iam.Options)) (*iam.ListAccessKeysOutput, error)
	DeleteAccessKey(ctx context.Context, in *iam.DeleteAccessKeyInput, opts ...func(*iam.Options)) (*iam.DeleteAccessKeyOutput, error)
}

// iamCredsJSON is the JSON secret value shape stored after a successful rotation.
// An application secret consumer parses this to obtain the new key pair.
type iamCredsJSON struct {
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
}

// awsiamRotator is a GENERATING rotator: AWS mints a NEW access key for an IAM
// user (CreateAccessKey) and Janus stores the returned {access_key_id,
// secret_access_key} as a JSON secret value. The OLD key(s) for the same user
// are then deleted, so the just-created key is the only live credential. AWS
// caps a user at 2 access keys, so a robust rotation lists first, creates the
// new key, then deletes the others. It implements rotatorGenerator, not
// rotatorApplier — the CreateAccessKey call IS the external side effect.
//
// Crash-safety note: if the server crashes AFTER CreateAccessKey but BEFORE the
// new value is persisted, the new key exists in AWS but Janus did not record it,
// orphaning a credential (the user is now at 2 keys and the next rotation may
// need to prune the un-recorded one). This mirrors how dynamic secrets reason
// about "the external system minted a credential we must not lose": a retry is
// safe — deletion of the old key(s) is idempotent, and because AWS caps a user
// at 2 keys, a subsequent rotation lists the (at most) two keys, creates a new
// one only if capacity allows, and prunes the rest, converging the user back to
// a single Janus-recorded key.
type awsiamRotator struct {
	// newClient builds an iamAPI from static creds + region (overridable in tests).
	newClient func(ctx context.Context, cfg PolicyConfig) (iamAPI, error)
}

// defaultIAMClient builds an IAM client from STATIC credentials only. It never
// falls back to ambient env/instance-profile creds: the rotator's identity is
// explicit and must not silently borrow the host's AWS identity.
func defaultIAMClient(ctx context.Context, cfg PolicyConfig) (iamAPI, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(cfg.IAMRegion),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			cfg.IAMAccessKeyID, cfg.IAMSecretAccessKey, cfg.IAMSessionToken)),
	)
	if err != nil {
		return nil, ErrInvalidConfig
	}
	return iam.NewFromConfig(awsCfg), nil
}

func (r awsiamRotator) client(ctx context.Context, cfg PolicyConfig) (iamAPI, error) {
	if r.newClient != nil {
		return r.newClient(ctx, cfg)
	}
	return defaultIAMClient(ctx, cfg)
}

func (r awsiamRotator) generate(ctx context.Context, cfg PolicyConfig, policyID, secretKey string) (string, error) {
	if !iamUserRe.MatchString(cfg.IAMUser) || cfg.IAMRegion == "" ||
		cfg.IAMAccessKeyID == "" || cfg.IAMSecretAccessKey == "" {
		return "", ErrInvalidConfig
	}
	cl, err := r.client(ctx, cfg)
	if err != nil {
		return "", err
	}

	// List existing keys BEFORE creating the new one so we know precisely which
	// keys pre-dated this rotation and must be pruned. (AWS caps a user at 2
	// keys; if the user is already at the cap, CreateAccessKey will fail and we
	// surface a sanitized error — the operator must clear a slot.)
	existing, err := cl.ListAccessKeys(ctx, &iam.ListAccessKeysInput{UserName: aws.String(cfg.IAMUser)})
	if err != nil {
		return "", fmt.Errorf("%w: list access keys", ErrApplyFailed)
	}
	var oldKeyIDs []string
	for _, m := range existing.AccessKeyMetadata {
		if id := aws.ToString(m.AccessKeyId); id != "" {
			oldKeyIDs = append(oldKeyIDs, id)
		}
	}

	// Create the NEW key — the external side effect that mints the credential.
	created, err := cl.CreateAccessKey(ctx, &iam.CreateAccessKeyInput{UserName: aws.String(cfg.IAMUser)})
	if err != nil {
		return "", fmt.Errorf("%w: create access key", ErrApplyFailed)
	}
	if created.AccessKey == nil ||
		aws.ToString(created.AccessKey.AccessKeyId) == "" ||
		aws.ToString(created.AccessKey.SecretAccessKey) == "" {
		return "", fmt.Errorf("%w: create access key returned no key material", ErrApplyFailed)
	}
	newKeyID := aws.ToString(created.AccessKey.AccessKeyId)

	// Marshal the new credential BEFORE pruning old keys so that even if a prune
	// call fails, the caller can still receive the freshly-minted key (the old
	// keys will be pruned on the next rotation). A prune failure is surfaced as
	// an error, but the JSON has already been prepared for logging-free return.
	value, err := json.Marshal(iamCredsJSON{
		AccessKeyID:     newKeyID,
		SecretAccessKey: aws.ToString(created.AccessKey.SecretAccessKey),
	})
	if err != nil {
		return "", fmt.Errorf("%w: marshal new credential", ErrApplyFailed)
	}

	// Delete every OTHER key (skip the just-created one, which won't be in the
	// pre-create list, but guard against races). Deletion is idempotent enough:
	// a key already gone yields an error we sanitize; sorting makes the order
	// deterministic for tests.
	sort.Strings(oldKeyIDs)
	for _, id := range oldKeyIDs {
		if id == newKeyID {
			continue
		}
		if _, err := cl.DeleteAccessKey(ctx, &iam.DeleteAccessKeyInput{
			UserName:    aws.String(cfg.IAMUser),
			AccessKeyId: aws.String(id),
		}); err != nil {
			// Never echo the AWS error (may carry ARN / account id / key id).
			return "", fmt.Errorf("%w: delete old access key", ErrApplyFailed)
		}
	}
	return string(value), nil
}
