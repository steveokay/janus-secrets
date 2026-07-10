package rotation

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestPostgresRotatorAltersRole(t *testing.T) {
	if testDSN == "" {
		t.Skip("postgres/docker not available")
	}
	adminDSN := testDSN
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)

	// Keep the test rerunnable against a shared container.
	if _, err := admin.Exec(ctx, `DROP ROLE IF EXISTS app_rot`); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `CREATE ROLE app_rot LOGIN PASSWORD 'old_pw'`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = admin.Exec(ctx, `DROP ROLE IF EXISTS app_rot`) }()

	rot := postgresRotator{}
	cfg := PolicyConfig{AdminDSN: adminDSN, Role: "app_rot"}
	if err := rot.apply(ctx, cfg, "pol", "DB_PASSWORD", "brandNewPW123"); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// new password connects
	connCfg, err := pgx.ParseConfig(adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	connCfg.User = "app_rot"
	connCfg.Password = "brandNewPW123"
	c, err := pgx.ConnectConfig(ctx, connCfg)
	if err != nil {
		t.Fatalf("new password should connect: %v", err)
	}
	c.Close(ctx)

	// idempotent: applying the same value again still succeeds
	if err := rot.apply(ctx, cfg, "pol", "DB_PASSWORD", "brandNewPW123"); err != nil {
		t.Fatalf("re-apply not idempotent: %v", err)
	}

	// bad role name is rejected before touching the DB
	if err := rot.apply(ctx, PolicyConfig{AdminDSN: adminDSN, Role: "bad; DROP"}, "p", "K", "v"); err == nil {
		t.Fatal("want rejection of invalid role identifier")
	}
}
