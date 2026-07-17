package secrets

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/steveokay/janus-secrets/internal/store"
)

// TestCloneEnvironment deep-copies a source environment's config tree and each
// config's own latest secrets into a new environment. It proves two things that
// a naive blob copy could not: (1) inheritance is remapped so the cloned branch
// inherits from the NEW root (not the source root), and (2) values decrypt
// correctly under the NEW config's AAD (the value AAD binds config_id, so the
// secrets had to be decrypted and re-encrypted, not copied verbatim).
func TestCloneEnvironment(t *testing.T) {
	s := newService(t)
	ctx := context.Background()

	slug := fmt.Sprintf("proj-%d", slugSeq.Add(1))
	p, err := s.CreateProject(ctx, slug, "Clone Project")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	src, err := s.CreateEnvironment(ctx, p.ID, "dev", "Dev")
	if err != nil {
		t.Fatalf("CreateEnvironment src: %v", err)
	}
	root, err := s.CreateConfig(ctx, src.ID, "root", nil)
	if err != nil {
		t.Fatalf("CreateConfig root: %v", err)
	}
	branch, err := s.CreateConfig(ctx, src.ID, "branch", &root.ID)
	if err != nil {
		t.Fatalf("CreateConfig branch: %v", err)
	}

	if _, err := s.SetSecrets(ctx, root.ID, []SecretChange{
		{Key: "API_KEY", Value: []byte("v1")},
	}, "", "tester"); err != nil {
		t.Fatalf("SetSecrets root: %v", err)
	}
	if _, err := s.SetSecrets(ctx, branch.ID, []SecretChange{
		{Key: "BRANCH_ONLY", Value: []byte("b1")},
	}, "", "tester"); err != nil {
		t.Fatalf("SetSecrets branch: %v", err)
	}

	newEnv, err := s.CloneEnvironment(ctx, p.ID, src.ID, "staging", "Staging", "tester")
	if err != nil {
		t.Fatalf("CloneEnvironment: %v", err)
	}
	if newEnv.ID == src.ID {
		t.Fatalf("clone returned the source env id %s", src.ID)
	}

	cloned, err := s.configs.ListByEnvironment(ctx, newEnv.ID)
	if err != nil {
		t.Fatalf("ListByEnvironment(new): %v", err)
	}
	var newRoot, newBranch *store.Config
	for _, c := range cloned {
		switch c.Name {
		case "root":
			newRoot = c
		case "branch":
			newBranch = c
		}
	}
	if newRoot == nil || newBranch == nil {
		t.Fatalf("expected root+branch in cloned env, got %d configs", len(cloned))
	}

	// Inheritance remapped to the NEW root, not the source root.
	if newBranch.InheritsFrom == nil {
		t.Fatalf("cloned branch has no inherits_from")
	}
	if *newBranch.InheritsFrom == root.ID {
		t.Fatalf("cloned branch still inherits from SOURCE root %s", root.ID)
	}
	if *newBranch.InheritsFrom != newRoot.ID {
		t.Fatalf("cloned branch inherits from %s, want new root %s", *newBranch.InheritsFrom, newRoot.ID)
	}

	// Values decrypt under the new config's AAD.
	gotRoot, err := s.GetSecret(ctx, newRoot.ID, "API_KEY")
	if err != nil {
		t.Fatalf("GetSecret(newRoot, API_KEY): %v", err)
	}
	if !bytes.Equal(gotRoot.Value, []byte("v1")) {
		t.Fatalf("newRoot API_KEY = %q, want v1", gotRoot.Value)
	}
	gotBranch, err := s.GetSecret(ctx, newBranch.ID, "BRANCH_ONLY")
	if err != nil {
		t.Fatalf("GetSecret(newBranch, BRANCH_ONLY): %v", err)
	}
	if !bytes.Equal(gotBranch.Value, []byte("b1")) {
		t.Fatalf("newBranch BRANCH_ONLY = %q, want b1", gotBranch.Value)
	}
}

// TestCloneEnvironmentCleanupOnFailure forces a mid-clone failure and asserts the
// best-effort compensating cleanup ran: the newly created environment must not be
// left LIVE. The fault seam is the keyring — after building the source tree we
// seal it, so copyOwnSecrets→RevealConfig fails to decrypt the source, which
// happens only AFTER CreateEnvironment succeeded (the copy step, not the env
// create). CloneEnvironment must then soft-delete the new env and return the error.
func TestCloneEnvironmentCleanupOnFailure(t *testing.T) {
	s := newService(t)
	ctx := context.Background()

	slug := fmt.Sprintf("proj-%d", slugSeq.Add(1))
	p, err := s.CreateProject(ctx, slug, "Clone Cleanup Project")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	src, err := s.CreateEnvironment(ctx, p.ID, "dev", "Dev")
	if err != nil {
		t.Fatalf("CreateEnvironment src: %v", err)
	}
	root, err := s.CreateConfig(ctx, src.ID, "root", nil)
	if err != nil {
		t.Fatalf("CreateConfig root: %v", err)
	}
	if _, err := s.SetSecrets(ctx, root.ID, []SecretChange{
		{Key: "API_KEY", Value: []byte("v1")},
	}, "", "tester"); err != nil {
		t.Fatalf("SetSecrets root: %v", err)
	}

	// Seal the keyring so the copy step (RevealConfig) fails mid-clone, after the
	// new environment has already been created.
	s.keyring.Seal()

	const newSlug = "staging"
	newEnv, err := s.CloneEnvironment(ctx, p.ID, src.ID, newSlug, "Staging", "tester")
	if err == nil {
		t.Fatalf("CloneEnvironment succeeded despite sealed keyring; want error")
	}
	if newEnv != nil {
		t.Fatalf("CloneEnvironment returned env %v alongside error; want nil", newEnv)
	}

	// The compensating cleanup must have soft-deleted the new env: it must not
	// appear among the project's LIVE environments.
	live, err := s.envs.ListByProject(ctx, p.ID)
	if err != nil {
		t.Fatalf("ListByProject: %v", err)
	}
	for _, e := range live {
		if e.Slug == newSlug {
			t.Fatalf("cloned env %q left LIVE after failed clone; cleanup did not run", newSlug)
		}
	}
}
