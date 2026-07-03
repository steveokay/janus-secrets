package secrets

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/store"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

var (
	testStore *store.Store  // nil if Docker is unavailable
	testPool  *pgxpool.Pool // separate pool for tamper/verification SQL
	slugSeq   atomic.Int64  // makes per-test project slugs unique
)

func TestMain(m *testing.M) {
	ctx := context.Background()
	ctr, dsn, err := startPostgres(ctx)
	if err != nil {
		fmt.Println("secrets tests will be skipped: could not start postgres:", err)
		os.Exit(m.Run())
	}
	st, err := store.Open(ctx, dsn)
	if err == nil {
		if mErr := st.Migrate(ctx); mErr == nil {
			testStore = st
			testPool, _ = pgxpool.New(ctx, dsn)
		} else {
			fmt.Println("secrets tests will be skipped: migrate failed:", mErr)
			st.Close()
		}
	} else {
		fmt.Println("secrets tests will be skipped: open failed:", err)
	}
	code := m.Run()
	if testPool != nil {
		testPool.Close()
	}
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

// newService returns a Service backed by the shared store and a freshly
// unsealed keyring, skipping the test when Docker is absent.
func newService(t *testing.T) *Service {
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
	return NewService(testStore, kr)
}

// mkChain creates a fresh project→env→config chain with a unique project slug
// and returns the project id and config id.
func mkChain(t *testing.T, s *Service) (projectID, configID string) {
	t.Helper()
	ctx := context.Background()
	slug := fmt.Sprintf("proj-%d", slugSeq.Add(1))
	p, err := s.CreateProject(ctx, slug, "Test Project")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	e, err := s.CreateEnvironment(ctx, p.ID, "prod", "Production")
	if err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}
	c, err := s.CreateConfig(ctx, e.ID, "root", nil)
	if err != nil {
		t.Fatalf("CreateConfig: %v", err)
	}
	return p.ID, c.ID
}
