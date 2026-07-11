package dynamic

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestInterpolateSubstitutesPlaceholders(t *testing.T) {
	exp := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	out, err := interpolate(
		`CREATE ROLE "{{name}}" LOGIN PASSWORD '{{password}}' VALID UNTIL '{{expiration}}';`,
		"janus_ro_abc123def456", "Pw0rd", exp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"janus_ro_abc123def456"`) ||
		!strings.Contains(out, `'Pw0rd'`) ||
		!strings.Contains(out, `'2030-01-02T03:04:05Z'`) {
		t.Fatalf("unexpected interpolation: %s", out)
	}
}

func TestInterpolateRejectsUnsafeUsername(t *testing.T) {
	if _, err := interpolate(`CREATE ROLE "{{name}}"`, `bad"; DROP`, "x", time.Now()); err == nil {
		t.Fatal("want rejection of non-identifier username")
	}
}

func TestRunStatementsCreatesAndDropsRole(t *testing.T) {
	if testDSN == "" {
		t.Skip("postgres/docker not available")
	}
	ctx := context.Background()
	u, err := generateUsername("itest")
	if err != nil {
		t.Fatal(err)
	}
	create, _ := interpolate(`CREATE ROLE "{{name}}" LOGIN PASSWORD '{{password}}';`, u, "testPW123", time.Now())
	if err := runStatements(ctx, testDSN, create); err != nil {
		t.Fatalf("create: %v", err)
	}
	drop, _ := interpolate(`DROP ROLE IF EXISTS "{{name}}";`, u, "", time.Now())
	if err := runStatements(ctx, testDSN, drop); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if err := runStatements(ctx, testDSN, drop); err != nil {
		t.Fatalf("re-drop not idempotent: %v", err)
	}
	if err := runStatements(ctx, "postgres://bad:bad@127.0.0.1:1/none", drop); err == nil {
		t.Fatal("want connect error")
	} else if strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("error leaked DSN host: %v", err)
	}
}

func TestRunStatementsMultiStatement(t *testing.T) {
	if testDSN == "" {
		t.Skip("postgres/docker not available")
	}
	ctx := context.Background()
	u, err := generateUsername("multi")
	if err != nil {
		t.Fatal(err)
	}
	// Two statements in one call: create role then annotate it. Proves multiple
	// semicolon-separated statements execute in a single simple-protocol Exec.
	sql, _ := interpolate(
		`CREATE ROLE "{{name}}" LOGIN PASSWORD '{{password}}'; COMMENT ON ROLE "{{name}}" IS 'janus-itest';`,
		u, "testPW123", time.Now())
	if err := runStatements(ctx, testDSN, sql); err != nil {
		t.Fatalf("multi-statement create failed (pgx simple-protocol assumption broken?): %v", err)
	}
	drop, _ := interpolate(`DROP ROLE IF EXISTS "{{name}}";`, u, "", time.Now())
	if err := runStatements(ctx, testDSN, drop); err != nil {
		t.Fatalf("cleanup drop: %v", err)
	}
}

// roleExists reports whether a Postgres role is present. Used to assert that a
// failed multi-statement batch rolled back its CREATE ROLE.
func roleExists(t *testing.T, ctx context.Context, name string) bool {
	t.Helper()
	conn, err := pgx.Connect(ctx, testDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var exists bool
	if err := conn.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, name).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	return exists
}

// TestRunStatementsMultiStatementAtomic proves the load-bearing property behind
// the crash-safe issue design: a multi-statement batch runs in one implicit
// transaction, so a failure in a later statement rolls back the CREATE ROLE.
// Without this guarantee a template whose GRANT fails would orphan a half-made
// role that the caller never learns the password for.
func TestRunStatementsMultiStatementAtomic(t *testing.T) {
	if testDSN == "" {
		t.Skip("postgres/docker not available")
	}
	ctx := context.Background()
	u, err := generateUsername("atomic")
	if err != nil {
		t.Fatal(err)
	}
	// First statement creates the role; second statement is guaranteed to fail
	// at execution time (division by zero). The whole batch must roll back.
	sql, _ := interpolate(
		`CREATE ROLE "{{name}}" LOGIN PASSWORD '{{password}}'; SELECT 1/0;`,
		u, "testPW123", time.Now())
	err = runStatements(ctx, testDSN, sql)
	if err == nil {
		_ = runStatements(ctx, testDSN, `DROP ROLE IF EXISTS "`+u+`"`) // cleanup if assumption is wrong
		t.Fatal("want error from failing second statement")
	}
	if !errors.Is(err, ErrApplyFailed) {
		t.Fatalf("want ErrApplyFailed, got %v", err)
	}
	if roleExists(t, ctx, u) {
		// Atomicity broken — the role leaked despite the batch failing.
		_ = runStatements(ctx, testDSN, `DROP ROLE IF EXISTS "`+u+`"`)
		t.Fatal("CREATE ROLE was not rolled back by the failing batch (non-atomic)")
	}
}

// TestRunStatementsExecErrorCategory asserts a malformed statement surfaces the
// value-free ErrApplyFailed sentinel and never echoes the SQL text (which could
// contain the generated password) in the returned error.
func TestRunStatementsExecErrorCategory(t *testing.T) {
	if testDSN == "" {
		t.Skip("postgres/docker not available")
	}
	ctx := context.Background()
	sql, _ := interpolate(`CREATE ROLE "{{name}}" LOGIN PASSWORD '{{password}}' NOT A REAL CLAUSE;`,
		"janus_bad_syntaxcheck", "sup3rSecretPW", time.Now())
	err := runStatements(ctx, testDSN, sql)
	if err == nil {
		t.Fatal("want error from malformed statement")
	}
	if !errors.Is(err, ErrApplyFailed) {
		t.Fatalf("want ErrApplyFailed, got %v", err)
	}
	if strings.Contains(err.Error(), "sup3rSecretPW") {
		t.Fatalf("error leaked the generated password: %v", err)
	}
}
