package dynamic

import (
	"context"
	"testing"

	"github.com/steveokay/janus-secrets/internal/audit"
	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/secrets"
	"github.com/steveokay/janus-secrets/internal/store"
)

// newTestService builds a dynamic.Service backed by the shared test store, with
// a freshly-unsealed keyring. It also returns a secrets.Service used only to
// seed project/env/config chains (the engine resolves the owning project via the
// config FK). Skips when Postgres/Docker is unavailable.
func newTestService(t *testing.T) (*Service, *secrets.Service) {
	t.Helper()
	if testStore == nil {
		t.Skip("postgres/docker not available")
	}
	kr := crypto.NewKeyring()
	master, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := kr.Unseal(master); err != nil {
		t.Fatal(err)
	}
	sec := secrets.NewService(testStore, kr)
	aud := audit.New(store.NewAuditRepo(testStore))
	svc := New(kr, testStore, aud, nil)
	return svc, sec
}

// newSealedTestService builds a Service backed by a keyring that is left SEALED.
// Used to assert scheduler/engine methods are no-ops while sealed. It does NOT
// skip when testStore is nil: sealed RunDue returns before any store access.
func newSealedTestService(t *testing.T) *Service {
	t.Helper()
	kr := crypto.NewKeyring() // freshly created keyrings are sealed until Unseal
	aud := audit.New(store.NewAuditRepo(testStore))
	return New(kr, testStore, aud, nil)
}

// seedConfig creates a fresh project->env->config chain (with a real wrapped KEK)
// and returns the config id. dynamic_roles.config_id has a FK onto configs.
func seedConfig(t *testing.T, ctx context.Context, sec *secrets.Service, slug string) string {
	t.Helper()
	p, err := sec.CreateProject(ctx, slug, "Test Project")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	e, err := sec.CreateEnvironment(ctx, p.ID, "prod", "Production")
	if err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}
	c, err := sec.CreateConfig(ctx, e.ID, "root", nil)
	if err != nil {
		t.Fatalf("CreateConfig: %v", err)
	}
	return c.ID
}
