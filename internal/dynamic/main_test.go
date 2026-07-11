package dynamic

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

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
		fmt.Println("dynamic tests will be skipped: could not start postgres:", err)
		os.Exit(m.Run())
	}
	testDSN = dsn
	st, err := store.Open(ctx, dsn)
	if err == nil {
		if mErr := st.Migrate(ctx); mErr == nil {
			testStore = st
		} else {
			fmt.Println("dynamic tests will be skipped: migrate failed:", mErr)
			st.Close()
		}
	} else {
		fmt.Println("dynamic tests will be skipped: open failed:", err)
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
