package dynamic

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/steveokay/janus-secrets/internal/store"
)

func TestRunDueRevokesExpiredLease(t *testing.T) {
	if testDSN == "" {
		t.Skip("postgres/docker not available")
	}
	ctx := context.Background()
	svc, sec := newTestService(t)
	configID := seedConfig(t, ctx, sec, "dyn-sched-expiry")

	role, err := svc.CreateRole(ctx, RoleInput{
		ConfigID: configID, Name: "exp", DefaultTTLSeconds: 3600, MaxTTLSeconds: 7200,
		Config: RoleConfig{AdminDSN: testDSN, CreationStatements: `CREATE ROLE "{{name}}" LOGIN PASSWORD '{{password}}';`},
	}, "tester")
	if err != nil {
		t.Fatal(err)
	}
	creds, err := svc.IssueCreds(ctx, role.ID, "tester")
	if err != nil {
		t.Fatal(err)
	}

	// Advance the engine clock past expiry.
	base := time.Now()
	svc.now = func() time.Time { return base.Add(2 * time.Hour) }

	svc.RunDue(ctx)

	l, err := svc.leases.Get(ctx, creds.LeaseID)
	if err != nil {
		t.Fatal(err)
	}
	if l.Status != "expired" {
		t.Fatalf("want expired, got %s", l.Status)
	}
	// Role really gone.
	cfg, _ := pgx.ParseConfig(testDSN)
	cfg.User, cfg.Password = creds.Username, creds.Password
	if c, err := pgx.ConnectConfig(ctx, cfg); err == nil {
		c.Close(ctx)
		t.Fatal("expired role should not connect")
	}
}

func TestSweepReclaimsOrphanedCreatingLease(t *testing.T) {
	if testDSN == "" {
		t.Skip("postgres/docker not available")
	}
	ctx := context.Background()
	svc, sec := newTestService(t)
	configID := seedConfig(t, ctx, sec, "dyn-sched-orphan")

	role, err := svc.CreateRole(ctx, RoleInput{
		ConfigID: configID, Name: "orph", DefaultTTLSeconds: 3600, MaxTTLSeconds: 7200,
		Config: RoleConfig{AdminDSN: testDSN, CreationStatements: `CREATE ROLE "{{name}}" LOGIN PASSWORD '{{password}}';`},
	}, "tester")
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a crash mid-issue: a lease row stuck in 'creating' whose DB role
	// may or may not exist. Insert one directly via the store repo, with a
	// created_at old enough to be past the grace window (use the engine clock
	// override so ClaimDue's creatingBefore cutoff selects it).
	id, err := testStore.NewID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	uname, _ := generateUsername(role.Name)
	lease := &store.DynamicLease{
		ID: id, RoleID: role.ID, ProjectID: role.ProjectID, DBUsername: uname,
		ExpiresAt: time.Now().Add(time.Hour), MaxExpiresAt: time.Now().Add(2 * time.Hour), CreatedBy: "tester",
	}
	if err := svc.leases.Create(ctx, lease); err != nil { // inserts status 'creating'
		t.Fatal(err)
	}
	// Advance the clock so the 'creating' row is older than creatingGrace.
	base := time.Now()
	svc.now = func() time.Time { return base.Add(10 * time.Minute) }

	svc.SweepOrphanedLeases(ctx)

	got, err := svc.leases.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "expired" {
		t.Fatalf("want orphaned creating lease reclaimed to 'expired', got %s", got.Status)
	}
}

func TestRunDueNoopWhileSealed(t *testing.T) {
	svc := newSealedTestService(t)
	// Must return without panicking and without touching the store.
	svc.RunDue(context.Background())
	svc.SweepOrphanedLeases(context.Background())
}

func TestRunSchedulerDisabledOnZeroTick(t *testing.T) {
	svc := newSealedTestService(t)
	// tick<=0 returns immediately (no goroutine leak, no panic).
	svc.RunScheduler(context.Background(), 0)
}
