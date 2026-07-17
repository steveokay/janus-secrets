package secrets

import (
	"context"
	"fmt"
	"testing"
)

func TestListMergedOrigins(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	slug := fmt.Sprintf("inh-%d", slugSeq.Add(1))
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
		{Key: "A", Value: []byte("1")}, {Key: "B", Value: []byte("2")},
	}, "seed base", "tester"); err != nil {
		t.Fatal(err)
	}
	branch, err := s.CreateConfig(ctx, e.ID, "branch", &base.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetSecrets(ctx, branch.ID, []SecretChange{
		{Key: "B", Value: []byte("20")}, {Key: "C", Value: []byte("3")},
	}, "seed branch", "tester"); err != nil {
		t.Fatal(err)
	}

	metas, err := s.ListSecretsMerged(ctx, branch.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, m := range metas {
		got[m.Key] = m.Origin
	}
	if got["A"] != "inherited" || got["B"] != "overridden" || got["C"] != "own" {
		t.Fatalf("origins = %+v", got)
	}
	if len(metas) != 3 {
		t.Fatalf("want 3 keys, got %d", len(metas))
	}
}

func TestListSecretsMerged_CarriesType(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	slug := fmt.Sprintf("inh-type-%d", slugSeq.Add(1))
	p, err := s.CreateProject(ctx, slug, "P")
	if err != nil {
		t.Fatal(err)
	}
	e, err := s.CreateEnvironment(ctx, p.ID, "prod", "Prod")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateConfig(ctx, e.ID, "cfg", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetSecrets(ctx, c.ID, []SecretChange{
		{Key: "TOKEN", Value: []byte("x"), Type: "password"},
	}, "seed", "tester"); err != nil {
		t.Fatal(err)
	}

	metas, err := s.ListSecretsMerged(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	var found *MergedMeta
	for i := range metas {
		if metas[i].Key == "TOKEN" {
			found = &metas[i]
		}
	}
	if found == nil {
		t.Fatal("TOKEN missing")
	}
	if found.Type != "password" {
		t.Errorf("Type = %q, want password", found.Type)
	}
}

func TestListSecretsMerged_InheritedCarriesType(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	slug := fmt.Sprintf("inh-type-base-%d", slugSeq.Add(1))
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
		{Key: "TOKEN", Value: []byte("x"), Type: "password"},
	}, "seed base", "tester"); err != nil {
		t.Fatal(err)
	}
	branch, err := s.CreateConfig(ctx, e.ID, "branch", &base.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetSecrets(ctx, branch.ID, []SecretChange{
		{Key: "OTHER", Value: []byte("y")},
	}, "seed branch", "tester"); err != nil {
		t.Fatal(err)
	}

	metas, err := s.ListSecretsMerged(ctx, branch.ID)
	if err != nil {
		t.Fatal(err)
	}
	var found *MergedMeta
	for i := range metas {
		if metas[i].Key == "TOKEN" {
			found = &metas[i]
		}
	}
	if found == nil {
		t.Fatal("TOKEN missing")
	}
	if found.Origin != "inherited" {
		t.Fatalf("Origin = %q, want inherited", found.Origin)
	}
	if found.Type != "password" {
		t.Errorf("Type = %q, want password (inherited key must carry its type)", found.Type)
	}
}
