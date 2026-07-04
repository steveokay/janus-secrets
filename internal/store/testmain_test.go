package store

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// testStore is the package-wide store, or nil if Docker is unavailable.
var testStore *Store

func TestMain(m *testing.M) {
	ctx := context.Background()
	ctr, dsn, err := startPostgres(ctx)
	if err != nil {
		fmt.Println("store tests will be skipped: could not start postgres:", err)
		os.Exit(m.Run()) // requireStore(t) skips each test
	}
	st, err := Open(ctx, dsn)
	if err == nil {
		if mErr := st.Migrate(ctx); mErr == nil {
			testStore = st
		} else {
			fmt.Println("store tests will be skipped: migrate failed:", mErr)
			st.Close()
		}
	} else {
		fmt.Println("store tests will be skipped: open failed:", err)
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
		// Postgres restarts once during first-boot init, so wait for the
		// "ready" log line to appear a SECOND time — a listening-port wait can
		// race the transient first start.
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

// requireStore returns the shared store or skips the test when Docker is absent.
func requireStore(t *testing.T) *Store {
	t.Helper()
	if testStore == nil {
		t.Skip("postgres/docker not available")
	}
	return testStore
}

// resetDB truncates all tables between tests for isolation.
func resetDB(t *testing.T) {
	t.Helper()
	_, err := testStore.pool.Exec(context.Background(),
		`TRUNCATE seal_config, auth_config, role_bindings, service_tokens, sessions, users,
		         config_version_entries, secret_values,
		         config_versions, configs, environments, projects
		 RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatalf("resetDB: %v", err)
	}
}
