package store

import (
	"context"
	"testing"
)

func TestMigration015CreatesProjectKEKVersions(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	var reg *string
	if err := s.pool.QueryRow(context.Background(),
		`SELECT to_regclass('public.project_kek_versions')::text`).Scan(&reg); err != nil {
		t.Fatalf("query: %v", err)
	}
	if reg == nil || *reg != "project_kek_versions" {
		t.Fatalf("table project_kek_versions not created, got %v", reg)
	}
}
