package store

import (
	"context"
	"testing"
)

func TestMigration011CreatesSyncTargets(t *testing.T) {
	s := requireStore(t) // TestMain already ran Migrate; assert the sync table exists
	ctx := context.Background()

	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables
		                WHERE table_schema = 'public' AND table_name = 'sync_targets')`).
		Scan(&exists)
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatal("sync_targets table missing after migrate")
	}
}
