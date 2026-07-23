package secretsync

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestCreateGitHubTarget(t *testing.T) {
	svc, sec := newTestService(t)
	_, cfg := mkChain(t, sec, "crud-create-github")

	ctx := context.Background()
	in := TargetInput{
		ConfigID:        cfg.ID,
		Provider:        ProviderGitHub,
		IntervalSeconds: 3600,
		Addr:            Addr{Owner: "acme", Repo: "widgets"},
		Creds:           Creds{PAT: "ghp_supersecret"},
	}
	view, err := svc.Create(ctx, in, "user:tester")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if view.Provider != ProviderGitHub {
		t.Fatalf("Provider = %q, want github", view.Provider)
	}
	if view.Addr.Owner != "acme" || view.Addr.Repo != "widgets" {
		t.Fatalf("Addr = %+v, want owner=acme repo=widgets", view.Addr)
	}
	if view.Status != "active" {
		t.Fatalf("Status = %q, want active", view.Status)
	}

	b, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(b), "ghp_supersecret") {
		t.Fatalf("serialized TargetView leaks PAT: %s", b)
	}

	reloaded, err := svc.Get(ctx, view.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if reloaded.ID != view.ID || reloaded.Provider != view.Provider || reloaded.Status != view.Status ||
		!reflect.DeepEqual(reloaded.Addr, view.Addr) {
		t.Fatalf("reloaded view = %+v, want %+v", reloaded, view)
	}
}

func TestCreateK8sTarget(t *testing.T) {
	svc, sec := newTestService(t)
	_, cfg := mkChain(t, sec, "crud-create-k8s")

	ctx := context.Background()
	in := TargetInput{
		ConfigID:        cfg.ID,
		Provider:        ProviderK8s,
		IntervalSeconds: 3600,
		Addr:            Addr{Namespace: "default", SecretName: "app-secrets"},
		Creds:           Creds{APIURL: "https://k8s.example.com", Token: "tok", CACert: "-----BEGIN CERTIFICATE-----"},
	}
	view, err := svc.Create(ctx, in, "user:tester")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if view.Provider != ProviderK8s {
		t.Fatalf("Provider = %q, want k8s", view.Provider)
	}
	if view.Addr.Namespace != "default" || view.Addr.SecretName != "app-secrets" {
		t.Fatalf("Addr = %+v, want namespace=default secret_name=app-secrets", view.Addr)
	}
}

func TestCreateValidation(t *testing.T) {
	svc, sec := newTestService(t)
	_, cfg := mkChain(t, sec, "crud-create-validation")
	ctx := context.Background()

	cases := []struct {
		name    string
		in      TargetInput
		wantErr error
	}{
		{
			name:    "unknown provider",
			in:      TargetInput{ConfigID: cfg.ID, Provider: "bogus", IntervalSeconds: 60, Addr: Addr{}, Creds: Creds{}},
			wantErr: ErrInvalidType,
		},
		{
			name:    "github missing owner",
			in:      TargetInput{ConfigID: cfg.ID, Provider: ProviderGitHub, IntervalSeconds: 60, Addr: Addr{Repo: "r"}, Creds: Creds{PAT: "p"}},
			wantErr: ErrInvalidConfig,
		},
		{
			name:    "github missing repo",
			in:      TargetInput{ConfigID: cfg.ID, Provider: ProviderGitHub, IntervalSeconds: 60, Addr: Addr{Owner: "o"}, Creds: Creds{PAT: "p"}},
			wantErr: ErrInvalidConfig,
		},
		{
			name:    "github missing pat",
			in:      TargetInput{ConfigID: cfg.ID, Provider: ProviderGitHub, IntervalSeconds: 60, Addr: Addr{Owner: "o", Repo: "r"}, Creds: Creds{}},
			wantErr: ErrInvalidConfig,
		},
		{
			name: "k8s missing cacert",
			in: TargetInput{ConfigID: cfg.ID, Provider: ProviderK8s, IntervalSeconds: 60,
				Addr: Addr{Namespace: "ns", SecretName: "sn"}, Creds: Creds{APIURL: "u", Token: "t"}},
			wantErr: ErrInvalidConfig,
		},
		{
			name: "k8s missing token",
			in: TargetInput{ConfigID: cfg.ID, Provider: ProviderK8s, IntervalSeconds: 60,
				Addr: Addr{Namespace: "ns", SecretName: "sn"}, Creds: Creds{APIURL: "u", CACert: "c"}},
			wantErr: ErrInvalidConfig,
		},
		{
			name: "k8s missing apiurl",
			in: TargetInput{ConfigID: cfg.ID, Provider: ProviderK8s, IntervalSeconds: 60,
				Addr: Addr{Namespace: "ns", SecretName: "sn"}, Creds: Creds{Token: "t", CACert: "c"}},
			wantErr: ErrInvalidConfig,
		},
		{
			name: "k8s missing namespace",
			in: TargetInput{ConfigID: cfg.ID, Provider: ProviderK8s, IntervalSeconds: 60,
				Addr: Addr{SecretName: "sn"}, Creds: Creds{APIURL: "u", Token: "t", CACert: "c"}},
			wantErr: ErrInvalidConfig,
		},
		{
			name: "k8s missing secretname",
			in: TargetInput{ConfigID: cfg.ID, Provider: ProviderK8s, IntervalSeconds: 60,
				Addr: Addr{Namespace: "ns"}, Creds: Creds{APIURL: "u", Token: "t", CACert: "c"}},
			wantErr: ErrInvalidConfig,
		},
		{
			name: "zero interval",
			in: TargetInput{ConfigID: cfg.ID, Provider: ProviderGitHub, IntervalSeconds: 0,
				Addr: Addr{Owner: "o", Repo: "r"}, Creds: Creds{PAT: "p"}},
			wantErr: ErrInvalidConfig,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Create(ctx, tc.in, "user:tester")
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Create(%s): got %v, want %v", tc.name, err, tc.wantErr)
			}
		})
	}
}

func TestCreateDuplicate(t *testing.T) {
	svc, sec := newTestService(t)
	_, cfg := mkChain(t, sec, "crud-create-dup")
	ctx := context.Background()

	in := TargetInput{
		ConfigID:        cfg.ID,
		Provider:        ProviderGitHub,
		IntervalSeconds: 3600,
		Addr:            Addr{Owner: "acme", Repo: "widgets"},
		Creds:           Creds{PAT: "ghp_x"},
	}
	if _, err := svc.Create(ctx, in, "user:tester"); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	_, err := svc.Create(ctx, in, "user:tester")
	if !errors.Is(err, ErrExists) {
		t.Fatalf("second Create: got %v, want ErrExists", err)
	}
}

func TestUpdate(t *testing.T) {
	svc, sec := newTestService(t)
	_, cfg := mkChain(t, sec, "crud-update")
	ctx := context.Background()

	in := TargetInput{
		ConfigID:        cfg.ID,
		Provider:        ProviderGitHub,
		Prune:           false,
		IntervalSeconds: 3600,
		Addr:            Addr{Owner: "acme", Repo: "widgets"},
		Creds:           Creds{PAT: "ghp_x"},
	}
	created, err := svc.Create(ctx, in, "user:tester")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	newInterval := int64(120)
	newPrune := true
	updated, err := svc.Update(ctx, created.ID, &newInterval, &newPrune, nil, nil, nil)
	if err != nil {
		t.Fatalf("Update interval/prune: %v", err)
	}
	if updated.IntervalSeconds != 120 || !updated.Prune {
		t.Fatalf("updated = %+v, want interval=120 prune=true", updated)
	}

	paused := "paused"
	updated, err = svc.Update(ctx, created.ID, nil, nil, &paused, nil, nil)
	if err != nil {
		t.Fatalf("Update status paused: %v", err)
	}
	if updated.Status != "paused" {
		t.Fatalf("Status = %q, want paused", updated.Status)
	}

	active := "active"
	updated, err = svc.Update(ctx, created.ID, nil, nil, &active, nil, nil)
	if err != nil {
		t.Fatalf("Update status active: %v", err)
	}
	if updated.Status != "active" {
		t.Fatalf("Status = %q, want active", updated.Status)
	}

	failed := "failed"
	_, err = svc.Update(ctx, created.ID, nil, nil, &failed, nil, nil)
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("Update status failed: got %v, want ErrInvalidConfig", err)
	}

	newCreds := Creds{PAT: "ghp_rotated"}
	newAddr := Addr{Owner: "acme", Repo: "widgets2"}
	updated, err = svc.Update(ctx, created.ID, nil, nil, nil, &newCreds, &newAddr)
	if err != nil {
		t.Fatalf("Update creds/addr: %v", err)
	}
	if updated.Addr.Repo != "widgets2" {
		t.Fatalf("Addr.Repo = %q, want widgets2", updated.Addr.Repo)
	}

	// Confirm the new creds actually decrypt via the repo/openCreds path.
	proj, err := svc.projects.Get(ctx, created.ProjectID)
	if err != nil {
		t.Fatalf("projects.Get: %v", err)
	}
	stored, err := svc.repo.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("repo.Get: %v", err)
	}
	gotCreds, err := svc.openCreds(proj, stored)
	if err != nil {
		t.Fatalf("openCreds: %v", err)
	}
	if gotCreds != newCreds {
		t.Fatalf("gotCreds = %+v, want %+v", gotCreds, newCreds)
	}
}

func TestSyncNowClearsFailed(t *testing.T) {
	svc, sec := newTestService(t)
	fake := newReconcileGHServer(t)
	svc.githubBaseURL = fake.srv.URL

	proj, cfg := mkChain(t, sec, "crud-syncnow")
	tgt := seedGHTarget(t, svc, sec, proj, cfg, map[string]string{"API_KEY": "s3cret"})

	ctx := context.Background()

	// Force the target into 'failed' via a failing fake, then point the fake
	// back at success and confirm SyncNow reactivates + succeeds.
	fake.failStatus = http.StatusInternalServerError
	for i := 0; i < 5; i++ {
		_ = svc.attempt(ctx, tgt, false)
		var err error
		tgt, err = svc.repo.Get(ctx, tgt.ID)
		if err != nil {
			t.Fatalf("repo.Get: %v", err)
		}
	}
	if tgt.Status != "failed" {
		t.Fatalf("Status = %q, want failed after %d attempts", tgt.Status, tgt.FailureCount)
	}

	fake.failStatus = 0
	fake.reset()
	if err := svc.SyncNow(ctx, tgt.ID); err != nil {
		t.Fatalf("SyncNow: %v", err)
	}

	got, err := svc.repo.Get(ctx, tgt.ID)
	if err != nil {
		t.Fatalf("repo.Get: %v", err)
	}
	if got.Status != "active" {
		t.Fatalf("Status = %q, want active after SyncNow", got.Status)
	}
	if got.SyncedFingerprint == nil {
		t.Fatal("SyncedFingerprint is nil after SyncNow success")
	}
	if got.FailureCount != 0 {
		t.Fatalf("FailureCount = %d, want 0 after SyncNow success", got.FailureCount)
	}
}

// TestViewMasksCreds is the definitive security test: the serialized
// TargetView must never contain the plaintext credential secret, regardless
// of provider.
func TestViewMasksCreds(t *testing.T) {
	svc, sec := newTestService(t)
	_, cfg := mkChain(t, sec, "crud-view-masks")
	ctx := context.Background()

	const secretPAT = "ghp_do_not_leak_me"
	in := TargetInput{
		ConfigID:        cfg.ID,
		Provider:        ProviderGitHub,
		IntervalSeconds: 3600,
		Addr:            Addr{Owner: "acme", Repo: "widgets"},
		Creds:           Creds{PAT: secretPAT},
	}
	view, err := svc.Create(ctx, in, "user:tester")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	b, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(b), secretPAT) {
		t.Fatalf("serialized TargetView leaks PAT: %s", b)
	}

	got, err := svc.Get(ctx, view.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	b2, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(b2), secretPAT) {
		t.Fatalf("serialized reloaded TargetView leaks PAT: %s", b2)
	}
}
