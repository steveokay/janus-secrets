package promote

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/secrets"
	"github.com/steveokay/janus-secrets/internal/store"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

var (
	testStore *store.Store // nil if Docker is unavailable
	slugSeq   atomic.Int64 // makes per-test project slugs unique
)

func TestMain(m *testing.M) {
	ctx := context.Background()
	ctr, dsn, err := startPostgres(ctx)
	if err != nil {
		fmt.Println("promote tests will be skipped: could not start postgres:", err)
		os.Exit(m.Run())
	}
	st, err := store.Open(ctx, dsn)
	if err == nil {
		if mErr := st.Migrate(ctx); mErr == nil {
			testStore = st
		} else {
			fmt.Println("promote tests will be skipped: migrate failed:", mErr)
			st.Close()
		}
	} else {
		fmt.Println("promote tests will be skipped: open failed:", err)
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

// harness bundles a promote Service with a fully wired project pipeline
// (dev → staging → prod) and helpers to seed and read configs.
type harness struct {
	t   *testing.T
	sec *secrets.Service
	svc *Service

	proj    string
	devEnv  string
	stgEnv  string
	prodEnv string
	devCfg  string
	stgCfg  string
	prodCfg string
	actor   string
}

// newHarness builds an unsealed secrets Service, a promote Service, and a fresh
// project with a dev → staging → prod pipeline, each env holding a "default"
// config. Skips the test when Docker/Postgres is unavailable.
func newHarness(t *testing.T) *harness {
	t.Helper()
	if testStore == nil {
		t.Skip("postgres/docker not available")
	}
	ctx := context.Background()

	kr := crypto.NewKeyring()
	master, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := kr.Unseal(master); err != nil {
		t.Fatal(err)
	}
	sec := secrets.NewService(testStore, kr)
	svc := New(sec, testStore)

	slug := fmt.Sprintf("promo-%d", slugSeq.Add(1))
	p, err := sec.CreateProject(ctx, slug, "Promote Test")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	dev, err := sec.CreateEnvironment(ctx, p.ID, "dev", "Development")
	if err != nil {
		t.Fatalf("CreateEnvironment dev: %v", err)
	}
	stg, err := sec.CreateEnvironment(ctx, p.ID, "staging", "Staging")
	if err != nil {
		t.Fatalf("CreateEnvironment staging: %v", err)
	}
	prod, err := sec.CreateEnvironment(ctx, p.ID, "prod", "Production")
	if err != nil {
		t.Fatalf("CreateEnvironment prod: %v", err)
	}
	devCfg, err := sec.CreateConfig(ctx, dev.ID, "default", nil)
	if err != nil {
		t.Fatalf("CreateConfig dev: %v", err)
	}
	stgCfg, err := sec.CreateConfig(ctx, stg.ID, "default", nil)
	if err != nil {
		t.Fatalf("CreateConfig staging: %v", err)
	}
	prodCfg, err := sec.CreateConfig(ctx, prod.ID, "default", nil)
	if err != nil {
		t.Fatalf("CreateConfig prod: %v", err)
	}
	if err := store.NewPipelineRepo(testStore).Set(ctx, p.ID, []string{dev.ID, stg.ID, prod.ID}); err != nil {
		t.Fatalf("pipeline Set: %v", err)
	}

	return &harness{
		t:       t,
		sec:     sec,
		svc:     svc,
		proj:    p.ID,
		devEnv:  dev.ID,
		stgEnv:  stg.ID,
		prodEnv: prod.ID,
		devCfg:  devCfg.ID,
		stgCfg:  stgCfg.ID,
		prodCfg: prodCfg.ID,
		actor:   "tester",
	}
}

// setSecrets writes each key/value as one config version.
func (h *harness) setSecrets(t *testing.T, configID string, kv map[string]string) {
	t.Helper()
	changes := make([]secrets.SecretChange, 0, len(kv))
	for k, v := range kv {
		changes = append(changes, secrets.SecretChange{Key: k, Value: []byte(v)})
	}
	if _, err := h.sec.SetSecrets(context.Background(), configID, changes, "seed", h.actor); err != nil {
		t.Fatalf("setSecrets(%s): %v", configID, err)
	}
}

// reveal returns the current plaintext values of a config as a string map.
func (h *harness) reveal(t *testing.T, configID string) map[string]string {
	t.Helper()
	_, vals, err := h.sec.RevealConfig(context.Background(), configID)
	if err != nil {
		t.Fatalf("reveal(%s): %v", configID, err)
	}
	out := make(map[string]string, len(vals))
	for k, sec := range vals {
		out[k] = string(sec.Value)
	}
	return out
}
