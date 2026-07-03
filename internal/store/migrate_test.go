package store

import (
	"context"
	"testing"
)

func TestMigrateCreatesTables(t *testing.T) {
	s := requireStore(t) // TestMain already ran Migrate; verify the schema exists
	ctx := context.Background()

	want := []string{
		"seal_config", "projects", "environments", "configs",
		"config_versions", "secret_values", "config_version_entries",
	}
	for _, tbl := range want {
		var exists bool
		err := s.pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables
			                WHERE table_schema = 'public' AND table_name = $1)`,
			tbl).Scan(&exists)
		if err != nil {
			t.Fatalf("query %s: %v", tbl, err)
		}
		if !exists {
			t.Fatalf("table %q missing after migrate", tbl)
		}
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	s := requireStore(t)
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("second Migrate should be a no-op: %v", err)
	}
}

func TestMigrateDownUp(t *testing.T) {
	s := requireStore(t)
	ctx := context.Background()

	if err := s.migrateDownForTest(); err != nil {
		t.Fatalf("down: %v", err)
	}
	var exists bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables
		                WHERE table_schema='public' AND table_name='projects')`).
		Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("projects table should be gone after down")
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("re-up: %v", err)
	}
}
