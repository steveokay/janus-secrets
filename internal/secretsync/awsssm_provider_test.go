package secretsync

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// fakeSSM records PutParameter/DeleteParameters calls and models a param store.
type fakeSSM struct {
	store     map[string]string // name -> value
	puts      []ssm.PutParameterInput
	deleted   []string
	putErr    error
	deleteErr error
}

func (f *fakeSSM) PutParameter(_ context.Context, in *ssm.PutParameterInput, _ ...func(*ssm.Options)) (*ssm.PutParameterOutput, error) {
	if f.putErr != nil {
		return nil, f.putErr
	}
	if f.store == nil {
		f.store = map[string]string{}
	}
	f.store[aws.ToString(in.Name)] = aws.ToString(in.Value)
	f.puts = append(f.puts, *in)
	return &ssm.PutParameterOutput{}, nil
}

func (f *fakeSSM) DeleteParameters(_ context.Context, in *ssm.DeleteParametersInput, _ ...func(*ssm.Options)) (*ssm.DeleteParametersOutput, error) {
	if f.deleteErr != nil {
		return nil, f.deleteErr
	}
	f.deleted = append(f.deleted, in.Names...)
	for _, n := range in.Names {
		delete(f.store, n)
	}
	return &ssm.DeleteParametersOutput{DeletedParameters: in.Names}, nil
}

func newSSMProvider(f *fakeSSM) awsssmProvider {
	return awsssmProvider{newClient: func(_ context.Context, _ Creds, _ string) (ssmAPI, error) { return f, nil }}
}

func TestAWSSSMApplyWritesSecureStrings(t *testing.T) {
	f := &fakeSSM{}
	p := newSSMProvider(f)
	res, err := p.Apply(context.Background(),
		Creds{AccessKeyID: "AKIA", SecretAccessKey: "sk"},
		Addr{Region: "us-east-1", PathPrefix: "/janus/app/prod"},
		map[string]string{"API_KEY": "s3cret", "DB_URL": "postgres://x"}, nil, true)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	sort.Strings(res.Applied)
	if len(res.Applied) != 2 || res.Applied[0] != "API_KEY" || res.Applied[1] != "DB_URL" {
		t.Fatalf("Applied = %v", res.Applied)
	}
	if got := f.store["/janus/app/prod/API_KEY"]; got != "s3cret" {
		t.Errorf("stored API_KEY = %q, want s3cret", got)
	}
	for _, in := range f.puts {
		if in.Type != ssmtypes.ParameterTypeSecureString {
			t.Errorf("param %s type = %v, want SecureString", aws.ToString(in.Name), in.Type)
		}
		if in.Overwrite == nil || !*in.Overwrite {
			t.Errorf("param %s Overwrite not true", aws.ToString(in.Name))
		}
	}
}

func TestAWSSSMPathJoin(t *testing.T) {
	f := &fakeSSM{}
	p := newSSMProvider(f)
	// Trailing slash on prefix must not double up.
	_, err := p.Apply(context.Background(),
		Creds{AccessKeyID: "a", SecretAccessKey: "b"},
		Addr{Region: "us-east-1", PathPrefix: "/janus/app/prod/"},
		map[string]string{"K": "v"}, nil, false)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, ok := f.store["/janus/app/prod/K"]; !ok {
		t.Errorf("store keys = %v, want /janus/app/prod/K", f.store)
	}
}

func TestAWSSSMPrunesManagedKeys(t *testing.T) {
	f := &fakeSSM{}
	p := newSSMProvider(f)
	// Seed OLD and KEEP.
	if _, err := p.Apply(context.Background(),
		Creds{AccessKeyID: "a", SecretAccessKey: "b"},
		Addr{Region: "us-east-1", PathPrefix: "/p"},
		map[string]string{"OLD": "a", "KEEP": "b"}, nil, true); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// KEEP only; OLD is managed → deleted.
	_, err := p.Apply(context.Background(),
		Creds{AccessKeyID: "a", SecretAccessKey: "b"},
		Addr{Region: "us-east-1", PathPrefix: "/p"},
		map[string]string{"KEEP": "b"}, []string{"OLD", "KEEP"}, true)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	wantDeleted := "/p/OLD"
	found := false
	for _, d := range f.deleted {
		if d == wantDeleted {
			found = true
		}
		if d == "/p/KEEP" {
			t.Errorf("KEEP wrongly deleted")
		}
	}
	if !found {
		t.Errorf("deleted = %v, want %s", f.deleted, wantDeleted)
	}
}

func TestAWSSSMPruneFalseNoDelete(t *testing.T) {
	f := &fakeSSM{}
	p := newSSMProvider(f)
	_, err := p.Apply(context.Background(),
		Creds{AccessKeyID: "a", SecretAccessKey: "b"},
		Addr{Region: "us-east-1", PathPrefix: "/p"},
		map[string]string{"KEEP": "b"}, []string{"OLD", "KEEP"}, false)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(f.deleted) != 0 {
		t.Errorf("expected no deletes, got %v", f.deleted)
	}
}

func TestAWSSSMMissingConfig(t *testing.T) {
	p := awsssmProvider{newClient: func(_ context.Context, _ Creds, _ string) (ssmAPI, error) {
		t.Fatal("client should not be built for invalid config")
		return nil, nil
	}}
	cases := []struct {
		name  string
		creds Creds
		addr  Addr
	}{
		{"no access key", Creds{SecretAccessKey: "b"}, Addr{Region: "r", PathPrefix: "/p"}},
		{"no secret key", Creds{AccessKeyID: "a"}, Addr{Region: "r", PathPrefix: "/p"}},
		{"no region", Creds{AccessKeyID: "a", SecretAccessKey: "b"}, Addr{PathPrefix: "/p"}},
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

func TestAWSSSMPutErrorSanitized(t *testing.T) {
	f := &fakeSSM{putErr: errors.New("AccessDenied: arn:aws:ssm:us-east-1:123456789012:parameter/janus/app/prod/K secret value here")}
	p := newSSMProvider(f)
	_, err := p.Apply(context.Background(),
		Creds{AccessKeyID: "a", SecretAccessKey: "b"},
		Addr{Region: "us-east-1", PathPrefix: "/janus/app/prod"},
		map[string]string{"K": "v"}, nil, false)
	if !errors.Is(err, ErrApplyFailed) {
		t.Fatalf("err = %v, want ErrApplyFailed", err)
	}
	// The AWS error string (ARN, account id, value) must not leak.
	if strings.Contains(err.Error(), "123456789012") || strings.Contains(err.Error(), "secret value here") {
		t.Errorf("error leaked AWS details: %v", err)
	}
}
