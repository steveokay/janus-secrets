package dynamic

import (
	"context"
	"strings"
	"testing"
	"time"
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
	// Two statements in one call: create then grant (self-grant is a no-op-safe example).
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
