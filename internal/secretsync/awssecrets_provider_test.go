package secretsync

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
)

// fakeSM models Secrets Manager: PutSecretValue on a missing secret returns
// ResourceNotFoundException (mirroring the real API), driving the create path.
type fakeSM struct {
	store        map[string]string // id -> value
	puts         []string
	creates      []string
	deleted      []string
	forceDeleted []string
	putErr       error // if set, PutSecretValue returns it (non-NotFound)
	createErr    error
	deleteErr    error
}

func (f *fakeSM) PutSecretValue(_ context.Context, in *secretsmanager.PutSecretValueInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.PutSecretValueOutput, error) {
	if f.putErr != nil {
		return nil, f.putErr
	}
	if f.store == nil {
		f.store = map[string]string{}
	}
	id := aws.ToString(in.SecretId)
	if _, ok := f.store[id]; !ok {
		return nil, &smtypes.ResourceNotFoundException{Message: aws.String("Secrets Manager can't find the specified secret.")}
	}
	f.store[id] = aws.ToString(in.SecretString)
	f.puts = append(f.puts, id)
	return &secretsmanager.PutSecretValueOutput{}, nil
}

func (f *fakeSM) CreateSecret(_ context.Context, in *secretsmanager.CreateSecretInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.CreateSecretOutput, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	if f.store == nil {
		f.store = map[string]string{}
	}
	id := aws.ToString(in.Name)
	f.store[id] = aws.ToString(in.SecretString)
	f.creates = append(f.creates, id)
	return &secretsmanager.CreateSecretOutput{}, nil
}

func (f *fakeSM) DeleteSecret(_ context.Context, in *secretsmanager.DeleteSecretInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.DeleteSecretOutput, error) {
	if f.deleteErr != nil {
		return nil, f.deleteErr
	}
	id := aws.ToString(in.SecretId)
	f.deleted = append(f.deleted, id)
	if in.ForceDeleteWithoutRecovery != nil && *in.ForceDeleteWithoutRecovery {
		f.forceDeleted = append(f.forceDeleted, id)
	}
	delete(f.store, id)
	return &secretsmanager.DeleteSecretOutput{}, nil
}

func newSMProvider(f *fakeSM) awssecretsProvider {
	return awssecretsProvider{newClient: func(_ context.Context, _ Creds, _ string) (smAPI, error) { return f, nil }}
}

func smCreds() Creds { return Creds{AccessKeyID: "AKIA", SecretAccessKey: "sk"} }
func smAddr() Addr   { return Addr{Region: "us-east-1", PathPrefix: "janus/app/prod"} }

func TestAWSSecretsApplyCreatesThenUpdates(t *testing.T) {
	f := &fakeSM{}
	p := newSMProvider(f)
	// First apply: neither exists → both go via CreateSecret.
	res, err := p.Apply(context.Background(), smCreds(), smAddr(),
		map[string]string{"API_KEY": "s3cret", "DB_URL": "postgres://x"}, nil, false)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	sort.Strings(res.Applied)
	if len(res.Applied) != 2 || res.Applied[0] != "API_KEY" || res.Applied[1] != "DB_URL" {
		t.Fatalf("Applied = %v", res.Applied)
	}
	if len(f.creates) != 2 {
		t.Errorf("creates = %v, want 2", f.creates)
	}
	if got := f.store["janus/app/prod/API_KEY"]; got != "s3cret" {
		t.Errorf("stored API_KEY = %q, want s3cret", got)
	}

	// Second apply on an existing secret: PutSecretValue succeeds (update path).
	if _, err := p.Apply(context.Background(), smCreds(), smAddr(),
		map[string]string{"API_KEY": "rotated"}, nil, false); err != nil {
		t.Fatalf("Apply update: %v", err)
	}
	if got := f.store["janus/app/prod/API_KEY"]; got != "rotated" {
		t.Errorf("after update = %q, want rotated", got)
	}
	if len(f.puts) != 1 || f.puts[0] != "janus/app/prod/API_KEY" {
		t.Errorf("puts = %v, want [janus/app/prod/API_KEY]", f.puts)
	}
}

func TestAWSSecretsPathJoin(t *testing.T) {
	f := &fakeSM{}
	p := newSMProvider(f)
	_, err := p.Apply(context.Background(), smCreds(),
		Addr{Region: "us-east-1", PathPrefix: "janus/app/prod/"}, // trailing slash
		map[string]string{"K": "v"}, nil, false)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, ok := f.store["janus/app/prod/K"]; !ok {
		t.Errorf("store keys = %v, want janus/app/prod/K", f.store)
	}
}

func TestAWSSecretsPrunesForceDelete(t *testing.T) {
	f := &fakeSM{}
	p := newSMProvider(f)
	if _, err := p.Apply(context.Background(), smCreds(),
		Addr{Region: "r", PathPrefix: "p"},
		map[string]string{"OLD": "a", "KEEP": "b"}, nil, true); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := p.Apply(context.Background(), smCreds(),
		Addr{Region: "r", PathPrefix: "p"},
		map[string]string{"KEEP": "b"}, []string{"OLD", "KEEP"}, true); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, ok := f.store["p/OLD"]; ok {
		t.Errorf("OLD not pruned; store = %v", f.store)
	}
	found := false
	for _, d := range f.forceDeleted {
		if d == "p/OLD" {
			found = true
		}
		if d == "p/KEEP" {
			t.Errorf("KEEP wrongly deleted")
		}
	}
	if !found {
		t.Errorf("forceDeleted = %v, want p/OLD force-deleted", f.forceDeleted)
	}
}

func TestAWSSecretsPruneMissingIsIdempotent(t *testing.T) {
	f := &fakeSM{deleteErr: &smtypes.ResourceNotFoundException{Message: aws.String("not found")}}
	p := newSMProvider(f)
	// GONE is managed but already absent → DeleteSecret NotFound is swallowed.
	if _, err := p.Apply(context.Background(), smCreds(),
		Addr{Region: "r", PathPrefix: "p"},
		map[string]string{}, []string{"GONE"}, true); err != nil {
		t.Fatalf("Apply: %v", err)
	}
}

func TestAWSSecretsPruneFalseNoDelete(t *testing.T) {
	f := &fakeSM{}
	p := newSMProvider(f)
	if _, err := p.Apply(context.Background(), smCreds(),
		Addr{Region: "r", PathPrefix: "p"},
		map[string]string{"KEEP": "b"}, []string{"OLD", "KEEP"}, false); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(f.deleted) != 0 {
		t.Errorf("expected no deletes, got %v", f.deleted)
	}
}

func TestAWSSecretsMissingConfig(t *testing.T) {
	p := awssecretsProvider{newClient: func(_ context.Context, _ Creds, _ string) (smAPI, error) {
		t.Fatal("client should not be built for invalid config")
		return nil, nil
	}}
	cases := []struct {
		name  string
		creds Creds
		addr  Addr
	}{
		{"no access key", Creds{SecretAccessKey: "b"}, Addr{Region: "r", PathPrefix: "p"}},
		{"no secret key", Creds{AccessKeyID: "a"}, Addr{Region: "r", PathPrefix: "p"}},
		{"no region", Creds{AccessKeyID: "a", SecretAccessKey: "b"}, Addr{PathPrefix: "p"}},
		{"no path", Creds{AccessKeyID: "a", SecretAccessKey: "b"}, Addr{Region: "r"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := p.Apply(context.Background(), tc.creds, tc.addr,
				map[string]string{"K": "v"}, nil, true)
			if !errors.Is(err, ErrInvalidConfig) {
				t.Errorf("err = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestAWSSecretsPutErrorSanitized(t *testing.T) {
	f := &fakeSM{putErr: errors.New("AccessDenied: arn:aws:secretsmanager:us-east-1:123456789012:secret:janus/app/prod/K s3cret value here")}
	p := newSMProvider(f)
	_, err := p.Apply(context.Background(), smCreds(),
		Addr{Region: "us-east-1", PathPrefix: "janus/app/prod"},
		map[string]string{"K": "v"}, nil, false)
	if !errors.Is(err, ErrApplyFailed) {
		t.Fatalf("err = %v, want ErrApplyFailed", err)
	}
	if strings.Contains(err.Error(), "123456789012") || strings.Contains(err.Error(), "s3cret value here") {
		t.Errorf("error leaked AWS details: %v", err)
	}
}

func TestAWSSecretsCreateErrorSanitized(t *testing.T) {
	f := &fakeSM{createErr: errors.New("SomeError: arn:aws:...:987654321098 s3cret")}
	p := newSMProvider(f)
	// Secret absent → Put returns NotFound → Create returns the leaky error.
	_, err := p.Apply(context.Background(), smCreds(),
		Addr{Region: "r", PathPrefix: "p"},
		map[string]string{"K": "v"}, nil, false)
	if !errors.Is(err, ErrApplyFailed) {
		t.Fatalf("err = %v, want ErrApplyFailed", err)
	}
	if strings.Contains(err.Error(), "987654321098") || strings.Contains(err.Error(), "s3cret") {
		t.Errorf("error leaked AWS details: %v", err)
	}
}
