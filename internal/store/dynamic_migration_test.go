package store

import (
	"context"
	"testing"
)

func TestMigration012CreatesDynamicTables(t *testing.T) {
	s := requireStore(t) // TestMain already ran Migrate; assert the dynamic tables exist
	ctx := context.Background()

	for _, tbl := range []string{"dynamic_roles", "dynamic_leases"} {
		var exists bool
		err := s.pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables
			                WHERE table_schema = 'public' AND table_name = $1)`, tbl).
			Scan(&exists)
		if err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Fatalf("%s table missing after migrate", tbl)
		}
	}
}
