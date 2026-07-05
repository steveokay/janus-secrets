package secrets

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// TestCreateConfigInheritsFromSameEnvironmentOnly locks the security invariant
// that a config may only inherit from a live base in the *same* environment.
// Inheritance is transparent to authorization (reading a branch needs no
// separate grant on its base), so a cross-environment or cross-project base
// would let a caller read another scope's secrets through the branch — an authz
// bypass. CreateConfig must reject such a base as invalid input.
func TestCreateConfigInheritsFromSameEnvironmentOnly(t *testing.T) {
	s := newService(t)
	ctx := context.Background()

	slug := fmt.Sprintf("isc-%d", slugSeq.Add(1))
	pA, err := s.CreateProject(ctx, slug, "A")
	if err != nil {
		t.Fatal(err)
	}
	prod, err := s.CreateEnvironment(ctx, pA.ID, "prod", "Prod")
	if err != nil {
		t.Fatal(err)
	}
	dev, err := s.CreateEnvironment(ctx, pA.ID, "dev", "Dev")
	if err != nil {
		t.Fatal(err)
	}
	// A prod config holding a secret the dev scope must not reach.
	prodCfg, err := s.CreateConfig(ctx, prod.ID, "prod", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetSecrets(ctx, prodCfg.ID, []SecretChange{
		{Key: "PROD_ONLY", Value: []byte("top-secret")},
	}, "seed prod", "tester"); err != nil {
		t.Fatal(err)
	}

	// Cross-environment base (dev inheriting from prod) → rejected.
	if _, err := s.CreateConfig(ctx, dev.ID, "dev", &prodCfg.ID); !errors.Is(err, ErrValidation) {
		t.Fatalf("cross-environment inherits_from: err = %v, want ErrValidation", err)
	}

	// Cross-project base (project A inheriting from project B) → rejected.
	slugB := fmt.Sprintf("isc-b-%d", slugSeq.Add(1))
	pB, err := s.CreateProject(ctx, slugB, "B")
	if err != nil {
		t.Fatal(err)
	}
	bProd, err := s.CreateEnvironment(ctx, pB.ID, "prod", "Prod")
	if err != nil {
		t.Fatal(err)
	}
	bCfg, err := s.CreateConfig(ctx, bProd.ID, "prod", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateConfig(ctx, dev.ID, "dev2", &bCfg.ID); !errors.Is(err, ErrValidation) {
		t.Fatalf("cross-project inherits_from: err = %v, want ErrValidation", err)
	}

	// Non-existent base → rejected as invalid input (not a create-time 404).
	missing := "00000000-0000-0000-0000-000000000000"
	if _, err := s.CreateConfig(ctx, dev.ID, "dev3", &missing); !errors.Is(err, ErrValidation) {
		t.Fatalf("missing inherits_from: err = %v, want ErrValidation", err)
	}

	// Positive control: a same-environment base is accepted.
	branch, err := s.CreateConfig(ctx, prod.ID, "prod-ci", &prodCfg.ID)
	if err != nil {
		t.Fatalf("same-environment inherits_from should be allowed: %v", err)
	}
	if branch.InheritsFrom == nil || *branch.InheritsFrom != prodCfg.ID {
		t.Fatalf("branch.InheritsFrom = %v, want %s", branch.InheritsFrom, prodCfg.ID)
	}
}
