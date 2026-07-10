package rotation

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
		fmt.Println("rotation tests will be skipped: could not start postgres:", err)
		os.Exit(m.Run())
	}
	testDSN = dsn
	st, err := store.Open(ctx, dsn)
	if err == nil {
		if mErr := st.Migrate(ctx); mErr == nil {
			testStore = st
		} else {
			fmt.Println("rotation tests will be skipped: migrate failed:", mErr)
			st.Close()
		}
	} else {
		fmt.Println("rotation tests will be skipped: open failed:", err)
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

// newTestService returns a rotation Service backed by the shared store and a
// freshly unsealed keyring, along with the underlying keyring and secrets
// service (needed to seed a real project). Skips the test when Docker is
// unavailable.
func newTestService(t *testing.T) (*Service, *crypto.Keyring, *secrets.Service) {
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
	return svc, kr, sec
}

// mkChain creates a fresh project→env→config chain (with a real wrapped KEK)
// via the secrets service, mirroring internal/secrets' own test harness.
// rotation_policies.config_id has a foreign key onto configs, so a real
// config row is required even though this test never touches its secrets.
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

func TestConfigBlobRoundTrip(t *testing.T) {
	svc, _, sec := newTestService(t)
	proj, storeCfg := mkChain(t, sec, "rotation-config-roundtrip")

	ctx := context.Background()
	policyID, err := testStore.NewID(ctx)
	if err != nil {
		t.Fatalf("NewID: %v", err)
	}

	cfg := PolicyConfig{
		AdminDSN:    "postgres://u:p@h/db",
		Role:        "app",
		PasswordLen: 24,
	}

	ct, nonce, wrapped, kekVer, err := svc.sealConfig(proj, policyID, cfg)
	if err != nil {
		t.Fatalf("sealConfig: %v", err)
	}
	if len(ct) == 0 || len(nonce) == 0 || len(wrapped) == 0 {
		t.Fatalf("sealConfig returned empty blob: ct=%d nonce=%d wrapped=%d", len(ct), len(nonce), len(wrapped))
	}
	if kekVer != proj.KEKVersion {
		t.Fatalf("kekVer = %d, want %d", kekVer, proj.KEKVersion)
	}

	repo := store.NewRotationRepo(testStore)
	in := &store.RotationPolicy{
		ID:                  policyID,
		ProjectID:           proj.ID,
		ConfigID:            storeCfg.ID,
		SecretKey:           "DB_PASSWORD",
		Type:                TypePostgres,
		IntervalSeconds:     3600,
		NextRotationAt:      time.Now().Add(time.Hour).UTC().Truncate(time.Second),
		ConfigCT:            ct,
		ConfigNonce:         nonce,
		ConfigWrappedDEK:    wrapped,
		ConfigDEKKEKVersion: kekVer,
		CreatedBy:           "user:tester",
	}
	if _, err := repo.Create(ctx, in); err != nil {
		t.Fatalf("repo.Create: %v", err)
	}

	got, err := repo.Get(ctx, policyID)
	if err != nil {
		t.Fatalf("repo.Get: %v", err)
	}

	gotCfg, err := svc.openConfig(proj, got)
	if err != nil {
		t.Fatalf("openConfig: %v", err)
	}
	if gotCfg != cfg {
		t.Fatalf("round-tripped config mismatch: got %+v, want %+v", gotCfg, cfg)
	}

	// AAD domain separation: decrypting the config blob under the pending AAD
	// must fail (config and pending slots must never be interchangeable).
	if _, err := svc.openBlob(proj, crypto.RotationPendingAAD(policyID), got.ConfigWrappedDEK, got.ConfigNonce, got.ConfigCT); err == nil {
		t.Fatal("openBlob under RotationPendingAAD unexpectedly succeeded on a config blob (AAD domain separation broken)")
	}
}
