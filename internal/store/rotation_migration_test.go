package store

import (
	"context"
	"testing"
)

func TestMigration010CreatesRotationPolicies(t *testing.T) {
	s := requireStore(t) // TestMain already ran Migrate; assert the rotation table exists
	ctx := context.Background()

	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables
		                WHERE table_schema = 'public' AND table_name = 'rotation_policies')`).
		Scan(&exists)
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatal("rotation_policies table missing after migrate")
	}
}
