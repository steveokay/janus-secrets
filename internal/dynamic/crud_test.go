package dynamic

import (
	"context"
	"testing"
)

func TestCreateRoleValidation(t *testing.T) {
	svc, sec := newTestService(t)
	ctx := context.Background()
	configID := seedConfig(t, ctx, sec, "dyn-crud-validation")

	// Missing {{password}} placeholder -> invalid.
	if _, err := svc.CreateRole(ctx, RoleInput{
		ConfigID: configID, Name: "ro", DefaultTTLSeconds: 60, MaxTTLSeconds: 120,
		Config: RoleConfig{AdminDSN: "postgres://x", CreationStatements: `CREATE ROLE "{{name}}";`},
	}, "tester"); err != ErrInvalidConfig {
		t.Fatalf("want ErrInvalidConfig for missing placeholder, got %v", err)
	}

	// max < default -> invalid.
	if _, err := svc.CreateRole(ctx, RoleInput{
		ConfigID: configID, Name: "ro", DefaultTTLSeconds: 120, MaxTTLSeconds: 60,
		Config: RoleConfig{AdminDSN: "postgres://x", CreationStatements: `CREATE ROLE "{{name}}" PASSWORD '{{password}}';`},
	}, "tester"); err != ErrInvalidConfig {
		t.Fatalf("want ErrInvalidConfig for bad ttls, got %v", err)
	}

	// Valid create.
	v, err := svc.CreateRole(ctx, RoleInput{
		ConfigID: configID, Name: "ro", DefaultTTLSeconds: 60, MaxTTLSeconds: 120,
		Config: RoleConfig{AdminDSN: "postgres://x", CreationStatements: `CREATE ROLE "{{name}}" PASSWORD '{{password}}';`},
	}, "tester")
	if err != nil {
		t.Fatalf("valid create: %v", err)
	}
	if v.Name != "ro" || v.DefaultTTLSeconds != 60 || v.MaxTTLSeconds != 120 {
		t.Fatalf("unexpected view: %+v", v)
	}

	// Duplicate name -> ErrExists.
	if _, err := svc.CreateRole(ctx, RoleInput{
		ConfigID: configID, Name: "ro", DefaultTTLSeconds: 60, MaxTTLSeconds: 120,
		Config: RoleConfig{AdminDSN: "postgres://x", CreationStatements: `CREATE ROLE "{{name}}" PASSWORD '{{password}}';`},
	}, "tester"); err != ErrExists {
		t.Fatalf("want ErrExists, got %v", err)
	}

	// Get + List round-trip.
	got, err := svc.GetRole(ctx, v.ID)
	if err != nil || got.ID != v.ID {
		t.Fatalf("GetRole: %+v err=%v", got, err)
	}
	list, err := svc.ListRolesByConfig(ctx, configID)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListRolesByConfig: len=%d err=%v", len(list), err)
	}

	// Update default TTL + re-seal config with a new admin DSN.
	nd := int64(90)
	if _, err := svc.UpdateRole(ctx, v.ID, &nd, nil, &RoleConfig{
		AdminDSN: "postgres://y", CreationStatements: `CREATE ROLE "{{name}}" PASSWORD '{{password}}';`,
	}); err != nil {
		t.Fatalf("UpdateRole: %v", err)
	}
	upd, _ := svc.GetRole(ctx, v.ID)
	if upd.DefaultTTLSeconds != 90 {
		t.Fatalf("update did not apply: %+v", upd)
	}

	// UpdateRole rejects an invalid config blob (missing {{password}}).
	if _, err := svc.UpdateRole(ctx, v.ID, nil, nil, &RoleConfig{
		AdminDSN: "postgres://z", CreationStatements: `CREATE ROLE "{{name}}";`,
	}); err != ErrInvalidConfig {
		t.Fatalf("want ErrInvalidConfig for bad update config, got %v", err)
	}

	// UpdateRole rejects lowering max below the (unchanged) default of 90.
	lowMax := int64(30)
	if _, err := svc.UpdateRole(ctx, v.ID, nil, &lowMax, nil); err != ErrInvalidConfig {
		t.Fatalf("want ErrInvalidConfig for max<default, got %v", err)
	}

	// DeleteRole (minimal: sealed guard + delete).
	if err := svc.DeleteRole(ctx, v.ID); err != nil {
		t.Fatalf("DeleteRole: %v", err)
	}
	if _, err := svc.GetRole(ctx, v.ID); err != ErrNotFound {
		t.Fatalf("want ErrNotFound after delete, got %v", err)
	}
}
