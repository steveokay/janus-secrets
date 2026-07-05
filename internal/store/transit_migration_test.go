package store

import (
	"context"
	"testing"
)

func TestMigration000006CreatesTransitTables(t *testing.T) {
	s := requireStore(t) // TestMain already ran Migrate; assert the transit tables exist
	ctx := context.Background()

	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.tables
		 WHERE table_schema = 'public' AND table_name IN ('transit_keys','transit_key_versions')`).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("expected 2 transit tables, got %d", n)
	}
}
