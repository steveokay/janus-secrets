package auth

import (
	"context"
	"fmt"
	"os"
	"sync"
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
	resetPool *pgxpool.Pool // second handle used only to reset tables between tests
)

func TestMain(m *testing.M) {
	ctx := context.Background()
	ctr, dsn, err := startPostgres(ctx)
	if err != nil {
		fmt.Println("auth tests will be skipped: could not start postgres:", err)
		os.Exit(m.Run())
	}
	st, err := store.Open(ctx, dsn)
	if err == nil {
		if mErr := st.Migrate(ctx); mErr == nil {
			testStore = st
			if pool, pErr := pgxpool.New(ctx, dsn); pErr == nil {
				resetPool = pool
			} else {
				fmt.Println("auth tests will be skipped: reset pool failed:", pErr)
				testStore = nil
			}
		} else {
			fmt.Println("auth tests will be skipped: migrate failed:", mErr)
			st.Close()
		}
	} else {
		fmt.Println("auth tests will be skipped: open failed:", err)
	}
	code := m.Run()
	if resetPool != nil {
		resetPool.Close()
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

// resetAuthTables clears the identity tables so each test starts from zero
// users (letting CreateInitialAdmin's bootstrap guard exercise its create
// branch). auth_config is deliberately preserved: the process-wide keyring is
// unsealed once, so the stored wrapped HMAC key must survive between tests.
func resetAuthTables(t *testing.T) {
	t.Helper()
	_, err := resetPool.Exec(context.Background(),
		`TRUNCATE oidc_federation_bindings, oidc_federation_config, service_tokens, sessions, users RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatalf("resetAuthTables: %v", err)
	}
}

// All tests share one database and therefore one stored wrapped HMAC key, so
// every keyring in the process must hold the same master.
var (
	testKeyringOnce sync.Once
	testKeyring     *crypto.Keyring
)

func sharedKeyring(t *testing.T) *crypto.Keyring {
	t.Helper()
	testKeyringOnce.Do(func() {
		kr := crypto.NewKeyring()
		master, err := crypto.GenerateKey()
		if err == nil {
			err = kr.Unseal(master)
		}
		if err != nil {
			panic(err)
		}
		testKeyring = kr
	})
	return testKeyring
}

// cryptoNewSealedKeyring returns a keyring that was never unsealed.
func cryptoNewSealedKeyring() *crypto.Keyring { return crypto.NewKeyring() }

// newTestService returns a Service over the shared store with the shared
// unsealed keyring and its HMAC key already bootstrapped. It resets the
// identity tables first, then bootstraps a fresh admin with a unique email.
var emailSeq int

func newTestService(t *testing.T) (*Service, string, string) {
	t.Helper()
	if testStore == nil {
		t.Skip("postgres/docker not available")
	}
	resetAuthTables(t)
	kr := sharedKeyring(t)
	svc := NewService(testStore, kr)
	ctx := context.Background()
	if err := svc.EnsureHMACKey(ctx); err != nil {
		t.Fatal(err)
	}
	emailSeq++
	email := fmt.Sprintf("admin%d@example.com", emailSeq)
	_, password, err := svc.CreateInitialAdmin(ctx, email)
	if err != nil {
		t.Fatal(err)
	}
	return svc, email, password
}
