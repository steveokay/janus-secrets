package secretsync

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/steveokay/janus-secrets/internal/audit"
	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/secrets"
	"github.com/steveokay/janus-secrets/internal/store"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

var testStore *store.Store // nil if Docker is unavailable
var testDSN string         // superuser DSN for the shared container; "" if unavailable

func TestMain(m *testing.M) {
	ctx := context.Background()
	ctr, dsn, err := startPostgres(ctx)
	if err != nil {
		fmt.Println("secretsync tests will be skipped: could not start postgres:", err)
		os.Exit(m.Run())
	}
	testDSN = dsn
	st, err := store.Open(ctx, dsn)
	if err == nil {
		if mErr := st.Migrate(ctx); mErr == nil {
			testStore = st
		} else {
			fmt.Println("secretsync tests will be skipped: migrate failed:", mErr)
			st.Close()
		}
	} else {
		fmt.Println("secretsync tests will be skipped: open failed:", err)
	}
	code := m.Run()
	if testStore != nil {
		testStore.Close()
	}
	_ = ctr.Terminate(ctx)
	os.Exit(code)
}

func startPostgres(ctx context.Context) (testcontainers.Container, string, error) {
	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("janus"),
		tcpostgres.WithUsername("janus"),
		tcpostgres.WithPassword("janus-test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second)),
	)
	if err != nil {
		return nil, "", err
	}
	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = ctr.Terminate(ctx)
		return nil, "", err
	}
	return ctr, dsn, nil
}

// newTestService returns a sync Service backed by the shared store and a
// freshly unsealed keyring, along with the secrets service (needed to seed a
// real project). Skips the test when Docker is unavailable.
func newTestService(t *testing.T) (*Service, *secrets.Service) {
	t.Helper()
	if testStore == nil {
		t.Skip("postgres/docker not available")
	}
	kr := crypto.NewKeyring()
	master, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := kr.Unseal(master); err != nil {
		t.Fatal(err)
	}
	sec := secrets.NewService(testStore, kr)
	aud := audit.New(store.NewAuditRepo(testStore))
	svc := New(kr, testStore, sec, aud, nil)
	return svc, sec
}

// mkChain creates a fresh project→env→config chain (with a real wrapped KEK)
// via the secrets service. sync_targets.config_id has a foreign key onto
// configs, so a real config row is required.
func mkChain(t *testing.T, sec *secrets.Service, slug string) (*store.Project, *store.Config) {
	t.Helper()
	ctx := context.Background()
	p, err := sec.CreateProject(ctx, slug, "Test Project")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	e, err := sec.CreateEnvironment(ctx, p.ID, "prod", "Production")
	if err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}
	c, err := sec.CreateConfig(ctx, e.ID, "root", nil)
	if err != nil {
		t.Fatalf("CreateConfig: %v", err)
	}
	return p, c
}

func TestCredsRoundTrip(t *testing.T) {
	svc, sec := newTestService(t)
	proj, storeCfg := mkChain(t, sec, "sync-creds-roundtrip")

	ctx := context.Background()
	targetID, err := testStore.NewID(ctx)
	if err != nil {
		t.Fatalf("NewID: %v", err)
	}

	creds := Creds{PAT: "ghp_secret", Token: "k8s-tok"}

	ct, nonce, wrapped, kekVer, err := svc.sealCreds(proj, targetID, creds)
	if err != nil {
		t.Fatalf("sealCreds: %v", err)
	}
	if len(ct) == 0 || len(nonce) == 0 || len(wrapped) == 0 {
		t.Fatalf("sealCreds returned empty blob: ct=%d nonce=%d wrapped=%d", len(ct), len(nonce), len(wrapped))
	}
	if kekVer != proj.KEKVersion {
		t.Fatalf("kekVer = %d, want %d", kekVer, proj.KEKVersion)
	}

	in := &store.SyncTarget{
		ID:                 targetID,
		ProjectID:          proj.ID,
		ConfigID:           storeCfg.ID,
		Provider:           ProviderGitHub,
		IntervalSeconds:    3600,
		NextSyncAt:         time.Now().Add(time.Hour).UTC().Truncate(time.Second),
		CredsCT:            ct,
		CredsNonce:         nonce,
		CredsWrappedDEK:    wrapped,
		CredsDEKKEKVersion: kekVer,
		Addr:               []byte(`{"owner":"o","repo":"r"}`),
		CreatedBy:          "user:tester",
	}
	if _, err := svc.repo.Create(ctx, in); err != nil {
		t.Fatalf("repo.Create: %v", err)
	}

	got, err := svc.repo.Get(ctx, targetID)
	if err != nil {
		t.Fatalf("repo.Get: %v", err)
	}

	gotCreds, err := svc.openCreds(proj, got)
	if err != nil {
		t.Fatalf("openCreds: %v", err)
	}
	if gotCreds != creds {
		t.Fatalf("round-tripped creds mismatch: got %+v, want %+v", gotCreds, creds)
	}

	// AAD binding: decrypting the creds blob under a DIFFERENT target id's AAD
	// must fail — creds are bound to their target and are not interchangeable.
	if _, err := svc.openBlob(proj, crypto.SyncCredsAAD("other"), got.CredsWrappedDEK, got.CredsNonce, got.CredsCT); err == nil {
		t.Fatal("openBlob under a foreign target AAD unexpectedly succeeded (AAD binding broken)")
	}
}
