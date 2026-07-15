package store

import (
	"context"
	"testing"
)

func TestMigration016CreatesPromotionTables(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	for _, tbl := range []string{"promotion_pipeline_steps", "config_locked_keys"} {
		var reg *string
		if err := s.pool.QueryRow(context.Background(),
			`SELECT to_regclass('public.'||$1)::text`, tbl).Scan(&reg); err != nil {
			t.Fatalf("query %s: %v", tbl, err)
		}
		if reg == nil || *reg != tbl {
			t.Fatalf("table %s not created, got %v", tbl, reg)
		}
	}
}
