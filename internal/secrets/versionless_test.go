package secrets

import (
	"context"
	"fmt"
	"testing"
)

// TestVersionlessBranchIsReadable proves a config that inherits but has no
// version of its own is still readable: ReadRawByID returns an empty own-value
// set (with InheritsFrom preserved so resolution continues up the chain), and
// ListSecretsMerged surfaces the inherited keys.
func TestVersionlessBranchIsReadable(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	slug := fmt.Sprintf("vl-%d", slugSeq.Add(1))
	p, err := s.CreateProject(ctx, slug, "P")
	if err != nil {
		t.Fatal(err)
	}
	e, err := s.CreateEnvironment(ctx, p.ID, "prod", "Prod")
	if err != nil {
		t.Fatal(err)
	}
	base, err := s.CreateConfig(ctx, e.ID, "base", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetSecrets(ctx, base.ID, []SecretChange{
		{Key: "SHARED", Value: []byte("inherited-val")},
	}, "seed base", "tester"); err != nil {
		t.Fatal(err)
	}
	// Branch inherits from base but is never written to (no own config version).
	branch, err := s.CreateConfig(ctx, e.ID, "branch", &base.ID)
	if err != nil {
		t.Fatal(err)
	}

	// ReadRawByID: no error, empty own values, InheritsFrom preserved.
	rc, err := s.ReadRawByID(ctx, branch.ID)
	if err != nil {
		t.Fatalf("ReadRawByID version-less branch: %v", err)
	}
	if len(rc.Values) != 0 {
		t.Fatalf("version-less branch own values = %v, want empty", rc.Values)
	}
	if rc.InheritsFrom == nil || *rc.InheritsFrom != base.ID {
		t.Fatalf("InheritsFrom = %v, want %s", rc.InheritsFrom, base.ID)
	}

	// ListSecretsMerged: surfaces the inherited key with origin "inherited".
	metas, err := s.ListSecretsMerged(ctx, branch.ID)
	if err != nil {
		t.Fatalf("ListSecretsMerged version-less branch: %v", err)
	}
	if len(metas) != 1 || metas[0].Key != "SHARED" || metas[0].Origin != "inherited" {
		t.Fatalf("merged metas = %+v, want [SHARED inherited]", metas)
	}
}
