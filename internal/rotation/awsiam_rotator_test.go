package rotation

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
)

// obviously-fake, low-entropy IAM fixtures.
const (
	fakeIAMUser      = "test-app-user"
	fakeIAMRegion    = "us-east-1"
	fakeAdminKeyID   = "AKIAADMINTEST00000000"
	fakeAdminSecret  = "test-admin-secret-xxxx"
	fakeNewKeyID     = "AKIANEWTEST0000000000"
	fakeNewSecret    = "test-new-secret-yyyy"
	fakeOldKeyID     = "AKIAOLDTEST0000000000"
	fakeOldKeyID2    = "AKIAOLD2TEST000000000"
)

// fakeIAM records calls and models a per-user key set. No live AWS.
type fakeIAM struct {
	existing   []string // key ids present before rotation
	newKeyID   string
	newSecret  string
	created    []string
	deleted    []string
	listErr    error
	createErr  error
	deleteErr  error
	listedUser string
}

func (f *fakeIAM) ListAccessKeys(_ context.Context, in *iam.ListAccessKeysInput, _ ...func(*iam.Options)) (*iam.ListAccessKeysOutput, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	f.listedUser = aws.ToString(in.UserName)
	var md []iamtypes.AccessKeyMetadata
	for _, id := range f.existing {
		id := id
		md = append(md, iamtypes.AccessKeyMetadata{AccessKeyId: aws.String(id), Status: iamtypes.StatusTypeActive})
	}
	return &iam.ListAccessKeysOutput{AccessKeyMetadata: md}, nil
}

func (f *fakeIAM) CreateAccessKey(_ context.Context, in *iam.CreateAccessKeyInput, _ ...func(*iam.Options)) (*iam.CreateAccessKeyOutput, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	f.created = append(f.created, aws.ToString(in.UserName))
	return &iam.CreateAccessKeyOutput{AccessKey: &iamtypes.AccessKey{
		UserName:        in.UserName,
		AccessKeyId:     aws.String(f.newKeyID),
		SecretAccessKey: aws.String(f.newSecret),
		Status:          iamtypes.StatusTypeActive,
	}}, nil
}

func (f *fakeIAM) DeleteAccessKey(_ context.Context, in *iam.DeleteAccessKeyInput, _ ...func(*iam.Options)) (*iam.DeleteAccessKeyOutput, error) {
	if f.deleteErr != nil {
		return nil, f.deleteErr
	}
	f.deleted = append(f.deleted, aws.ToString(in.AccessKeyId))
	return &iam.DeleteAccessKeyOutput{}, nil
}

func newIAMRotator(f *fakeIAM) awsiamRotator {
	return awsiamRotator{newClient: func(_ context.Context, _ PolicyConfig) (iamAPI, error) { return f, nil }}
}

func baseIAMConfig() PolicyConfig {
	return PolicyConfig{
		IAMUser: fakeIAMUser, IAMRegion: fakeIAMRegion,
		IAMAccessKeyID: fakeAdminKeyID, IAMSecretAccessKey: fakeAdminSecret,
	}
}

func TestIAMGenerateCreatesNewKeyAndDeletesOld(t *testing.T) {
	f := &fakeIAM{existing: []string{fakeOldKeyID}, newKeyID: fakeNewKeyID, newSecret: fakeNewSecret}
	rot := newIAMRotator(f)

	val, err := rot.generate(context.Background(), baseIAMConfig(), "pol", "AWS_CREDS")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	// JSON value shape: {"access_key_id":...,"secret_access_key":...}
	var got iamCredsJSON
	if err := json.Unmarshal([]byte(val), &got); err != nil {
		t.Fatalf("value is not JSON: %v (%q)", err, val)
	}
	if got.AccessKeyID != fakeNewKeyID || got.SecretAccessKey != fakeNewSecret {
		t.Fatalf("value = %+v, want new key material", got)
	}
	// The new key was created for the right user.
	if len(f.created) != 1 || f.created[0] != fakeIAMUser {
		t.Fatalf("created = %v, want one for %s", f.created, fakeIAMUser)
	}
	if f.listedUser != fakeIAMUser {
		t.Errorf("listed user = %q, want %q", f.listedUser, fakeIAMUser)
	}
	// The OLD key was deleted; the new one was NOT.
	if len(f.deleted) != 1 || f.deleted[0] != fakeOldKeyID {
		t.Fatalf("deleted = %v, want [%s]", f.deleted, fakeOldKeyID)
	}
	for _, d := range f.deleted {
		if d == fakeNewKeyID {
			t.Fatal("just-created key must never be deleted")
		}
	}
}

func TestIAMGenerateDeletesAllOldKeysAtCap(t *testing.T) {
	// A user already at 2 keys: after CreateAccessKey (fake ignores the cap),
	// BOTH pre-existing keys are pruned so only the new one remains.
	f := &fakeIAM{existing: []string{fakeOldKeyID, fakeOldKeyID2}, newKeyID: fakeNewKeyID, newSecret: fakeNewSecret}
	rot := newIAMRotator(f)
	if _, err := rot.generate(context.Background(), baseIAMConfig(), "pol", "AWS_CREDS"); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(f.deleted) != 2 {
		t.Fatalf("deleted = %v, want both old keys", f.deleted)
	}
}

func TestIAMGenerateNoOldKeys(t *testing.T) {
	// Fresh user with no keys: create one, delete none.
	f := &fakeIAM{existing: nil, newKeyID: fakeNewKeyID, newSecret: fakeNewSecret}
	rot := newIAMRotator(f)
	if _, err := rot.generate(context.Background(), baseIAMConfig(), "pol", "AWS_CREDS"); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(f.deleted) != 0 {
		t.Fatalf("deleted = %v, want none", f.deleted)
	}
}

func TestIAMGenerateRejectsBadConfig(t *testing.T) {
	rot := awsiamRotator{newClient: func(_ context.Context, _ PolicyConfig) (iamAPI, error) {
		t.Fatal("client should not be built for invalid config")
		return nil, nil
	}}
	cases := []struct {
		name string
		cfg  PolicyConfig
	}{
		{"no user", PolicyConfig{IAMRegion: "r", IAMAccessKeyID: "a", IAMSecretAccessKey: "b"}},
		{"bad user", PolicyConfig{IAMUser: "bad user!", IAMRegion: "r", IAMAccessKeyID: "a", IAMSecretAccessKey: "b"}},
		{"no region", PolicyConfig{IAMUser: fakeIAMUser, IAMAccessKeyID: "a", IAMSecretAccessKey: "b"}},
		{"no access key", PolicyConfig{IAMUser: fakeIAMUser, IAMRegion: "r", IAMSecretAccessKey: "b"}},
		{"no secret key", PolicyConfig{IAMUser: fakeIAMUser, IAMRegion: "r", IAMAccessKeyID: "a"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := rot.generate(context.Background(), tc.cfg, "p", "K"); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("err = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestIAMGenerateSanitizesErrors(t *testing.T) {
	leaky := errors.New("AccessDenied: arn:aws:iam::123456789012:user/test-app-user key AKIALEAKED000")
	cases := []struct {
		name string
		f    *fakeIAM
	}{
		{"list", &fakeIAM{listErr: leaky}},
		{"create", &fakeIAM{existing: []string{fakeOldKeyID}, createErr: leaky}},
		{"delete", &fakeIAM{existing: []string{fakeOldKeyID}, newKeyID: fakeNewKeyID, newSecret: fakeNewSecret, deleteErr: leaky}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rot := newIAMRotator(tc.f)
			_, err := rot.generate(context.Background(), baseIAMConfig(), "p", "K")
			if !errors.Is(err, ErrApplyFailed) {
				t.Fatalf("err = %v, want ErrApplyFailed", err)
			}
			for _, leak := range []string{"123456789012", "AKIALEAKED000", "AccessDenied", "arn:aws"} {
				if strings.Contains(err.Error(), leak) {
					t.Fatalf("error leaked %q: %v", leak, err)
				}
			}
		})
	}
}

// awsiamRotator must be a generating rotator, never an applier.
func TestIAMImplementsGeneratorNotApplier(t *testing.T) {
	var rot any = awsiamRotator{}
	if _, ok := rot.(rotatorGenerator); !ok {
		t.Fatal("awsiamRotator must implement rotatorGenerator")
	}
	if _, ok := rot.(rotatorApplier); ok {
		t.Fatal("awsiamRotator must NOT implement rotatorApplier")
	}
}
