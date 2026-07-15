package store

import (
	"context"
	"testing"
)

// TestMigration018AddsProvenanceColumns asserts both provenance columns exist on
// config_versions.
func TestMigration018AddsProvenanceColumns(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	for _, col := range []string{"promoted_from_env_id", "promoted_from_version"} {
		var name *string
		if err := s.pool.QueryRow(context.Background(),
			`SELECT column_name FROM information_schema.columns
			 WHERE table_name='config_versions' AND column_name=$1`, col).Scan(&name); err != nil {
			t.Fatalf("query column %s: %v", col, err)
		}
		if name == nil || *name != col {
			t.Fatalf("column %s not present on config_versions, got %v", col, name)
		}
	}
}

// makeVersion writes one config version (no secrets) and returns its id.
func makeVersion(t *testing.T, s *Store, configID string) ConfigVersion {
	t.Helper()
	repo := NewSecretRepo(s)
	cv, err := repo.SaveConfigVersion(context.Background(), configID, nil, "seed", "tester")
	if err != nil {
		t.Fatalf("SaveConfigVersion: %v", err)
	}
	return cv
}

// TestMarkPromotedAndRead asserts MarkPromoted records provenance and that
// ListVersions/GetVersion surface it on the promoted version and nil on others.
func TestMarkPromotedAndRead(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	_, srcEnv, configID := mkConfig(t, s, "prov")
	repo := NewSecretRepo(s)

	v1 := makeVersion(t, s, configID) // normal
	v2 := makeVersion(t, s, configID) // will be marked promoted

	if err := repo.MarkPromoted(ctx, v2.ID, srcEnv, 7); err != nil {
		t.Fatalf("MarkPromoted: %v", err)
	}

	// ListVersions: v1 nil, v2 populated.
	vs, err := repo.ListVersions(ctx, configID)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(vs) != 2 {
		t.Fatalf("want 2 versions, got %d", len(vs))
	}
	byVer := map[int]ConfigVersion{}
	for _, cv := range vs {
		byVer[cv.Version] = cv
	}
	if byVer[v1.Version].PromotedFromEnvID != nil || byVer[v1.Version].PromotedFromVersion != nil {
		t.Fatalf("v1 should have no provenance, got env=%v ver=%v",
			byVer[v1.Version].PromotedFromEnvID, byVer[v1.Version].PromotedFromVersion)
	}
	p2 := byVer[v2.Version]
	if p2.PromotedFromEnvID == nil || *p2.PromotedFromEnvID != srcEnv {
		t.Fatalf("v2 promoted_from_env_id: want %s, got %v", srcEnv, p2.PromotedFromEnvID)
	}
	if p2.PromotedFromVersion == nil || *p2.PromotedFromVersion != 7 {
		t.Fatalf("v2 promoted_from_version: want 7, got %v", p2.PromotedFromVersion)
	}

	// GetVersion mirrors it.
	cv, _, err := repo.GetVersion(ctx, configID, v2.Version)
	if err != nil {
		t.Fatalf("GetVersion: %v", err)
	}
	if cv.PromotedFromEnvID == nil || *cv.PromotedFromEnvID != srcEnv ||
		cv.PromotedFromVersion == nil || *cv.PromotedFromVersion != 7 {
		t.Fatalf("GetVersion provenance mismatch: env=%v ver=%v", cv.PromotedFromEnvID, cv.PromotedFromVersion)
	}
}

// TestLatestPromotionByConfig asserts only configs whose LATEST version is a
// promotion appear in the map.
func TestLatestPromotionByConfig(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	repo := NewSecretRepo(s)

	// cfgA: v1 normal, v2 promoted  -> PRESENT.
	_, srcEnvA, cfgA := mkConfig(t, s, "cfgA")
	makeVersion(t, s, cfgA)
	a2 := makeVersion(t, s, cfgA)
	if err := repo.MarkPromoted(ctx, a2.ID, srcEnvA, 3); err != nil {
		t.Fatalf("MarkPromoted a2: %v", err)
	}

	// cfgB: v1 promoted, v2 normal  -> ABSENT (latest is not a promotion).
	_, srcEnvB, cfgB := mkConfigNamed(t, s, "projb", "envb", "cfgB")
	b1 := makeVersion(t, s, cfgB)
	if err := repo.MarkPromoted(ctx, b1.ID, srcEnvB, 1); err != nil {
		t.Fatalf("MarkPromoted b1: %v", err)
	}
	makeVersion(t, s, cfgB)

	out, err := repo.LatestPromotionByConfig(ctx, []string{cfgA, cfgB})
	if err != nil {
		t.Fatalf("LatestPromotionByConfig: %v", err)
	}
	ref, ok := out[cfgA]
	if !ok {
		t.Fatalf("cfgA should be present in provenance map, got %v", out)
	}
	if ref.SourceEnvID != srcEnvA || ref.SourceVersion != 3 {
		t.Fatalf("cfgA ref: want env=%s ver=3, got %+v", srcEnvA, ref)
	}
	if _, ok := out[cfgB]; ok {
		t.Fatalf("cfgB should be ABSENT (latest version not promoted), got %+v", out[cfgB])
	}

	// Empty input -> empty map, no error.
	empty, err := repo.LatestPromotionByConfig(ctx, nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty input: want empty map/no error, got %v / %v", empty, err)
	}
}
