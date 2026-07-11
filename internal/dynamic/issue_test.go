package dynamic

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestIssueRenewRevokeRoundTrip(t *testing.T) {
	if testDSN == "" {
		t.Skip("postgres/docker not available")
	}
	ctx := context.Background()
	svc, sec := newTestService(t)
	configID := seedConfig(t, ctx, sec, "dyn-issue-roundtrip")

	role, err := svc.CreateRole(ctx, RoleInput{
		ConfigID: configID, Name: "ro", DefaultTTLSeconds: 3600, MaxTTLSeconds: 7200,
		Config: RoleConfig{
			AdminDSN:           testDSN,
			CreationStatements: `CREATE ROLE "{{name}}" LOGIN PASSWORD '{{password}}' VALID UNTIL '{{expiration}}';`,
		},
	}, "tester")
	if err != nil {
		t.Fatalf("create role: %v", err)
	}

	creds, err := svc.IssueCreds(ctx, role.ID, "tester")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if creds.Password == "" || creds.Username == "" || creds.LeaseID == "" {
		t.Fatalf("incomplete creds: %+v", creds)
	}
	// The generated credential can log in.
	cfg, _ := pgx.ParseConfig(testDSN)
	cfg.User = creds.Username
	cfg.Password = creds.Password
	c, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("issued creds should connect: %v", err)
	}
	c.Close(ctx)

	// Renew succeeds on an active lease.
	if _, err := svc.RenewLease(ctx, creds.LeaseID); err != nil {
		t.Fatalf("renew: %v", err)
	}

	// Revoke drops the role; a second revoke is idempotent.
	if err := svc.RevokeLease(ctx, creds.LeaseID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if err := svc.RevokeLease(ctx, creds.LeaseID); err != nil {
		t.Fatalf("double revoke: %v", err)
	}
	// The dropped role can no longer connect.
	if c2, err := pgx.ConnectConfig(ctx, cfg); err == nil {
		c2.Close(ctx)
		t.Fatal("revoked role should not connect")
	}
}

func TestIssueFailureCleansUp(t *testing.T) {
	if testDSN == "" {
		t.Skip("postgres/docker not available")
	}
	ctx := context.Background()
	svc, sec := newTestService(t)
	configID := seedConfig(t, ctx, sec, "dyn-issue-failure")

	// Creation statement that will FAIL at execution (invalid clause). The
	// reserved lease must be cleaned up (no orphaned active lease) and the
	// caller must get an error, not creds.
	role, err := svc.CreateRole(ctx, RoleInput{
		ConfigID: configID, Name: "bad", DefaultTTLSeconds: 3600, MaxTTLSeconds: 7200,
		Config: RoleConfig{
			AdminDSN:           testDSN,
			CreationStatements: `CREATE ROLE "{{name}}" LOGIN PASSWORD '{{password}}' NOT A REAL CLAUSE;`,
		},
	}, "tester")
	if err != nil {
		t.Fatalf("create role: %v", err)
	}
	if _, err := svc.IssueCreds(ctx, role.ID, "tester"); err == nil {
		t.Fatal("want issue error from failing creation statement")
	}
	// No lease should be left 'active' or 'creating' for this role. Query the
	// store repo directly (the engine ListLeasesByRole method arrives in Task 10).
	leases, err := svc.leases.ListByRole(ctx, role.ID)
	if err != nil {
		t.Fatalf("list leases: %v", err)
	}
	for _, l := range leases {
		if l.Status == "active" || l.Status == "creating" {
			t.Fatalf("issue failure left a non-terminal lease: %s", l.Status)
		}
	}
}
