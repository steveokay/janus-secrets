package promote

import (
	"context"
	"testing"

	"github.com/steveokay/janus-secrets/internal/store"
)

// TestApplyRecordsProvenance asserts that after Apply, the created target config
// version carries promotion provenance (source env id + pinned source version).
func TestApplyRecordsProvenance(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	h.setSecrets(t, h.devCfg, map[string]string{"A": "1", "B": "dev"})
	srcVer, err := h.sec.LatestVersion(ctx, h.devCfg)
	if err != nil {
		t.Fatalf("LatestVersion: %v", err)
	}

	if _, err := h.svc.Apply(ctx, ApplyRequest{
		SourceConfigID: h.devCfg,
		TargetConfigID: h.stgCfg,
		SourceVersion:  srcVer,
		Selections:     []Selection{{Key: "B", Action: ActionSet}},
		Actor:          h.actor,
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	repo := store.NewSecretRepo(testStore)
	vs, err := repo.ListVersions(ctx, h.stgCfg)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(vs) == 0 {
		t.Fatalf("no versions on staging config")
	}
	latest := vs[len(vs)-1]
	if latest.PromotedFromEnvID == nil || *latest.PromotedFromEnvID != h.devEnv {
		t.Fatalf("promoted_from_env_id: want %s, got %v", h.devEnv, latest.PromotedFromEnvID)
	}
	if latest.PromotedFromVersion == nil || *latest.PromotedFromVersion != srcVer {
		t.Fatalf("promoted_from_version: want %d, got %v", srcVer, latest.PromotedFromVersion)
	}
}
