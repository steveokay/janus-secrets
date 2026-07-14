package store

import (
	"context"
	"testing"
)

func TestPipelineRepoSetGetNext(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	pr := NewProjectRepo(s)
	er := NewEnvironmentRepo(s)
	plr := NewPipelineRepo(s)

	pid, _ := s.NewID(ctx)
	if _, err := pr.Create(ctx, pid, "proj", "Proj", []byte("kek"), 1); err != nil {
		t.Fatal(err)
	}
	// EnvironmentRepo.Create(ctx, projectID, slug, name) generates the id itself.
	mk := func(name string) string {
		e, err := er.Create(ctx, pid, name, name)
		if err != nil {
			t.Fatalf("env %s: %v", name, err)
		}
		return e.ID
	}
	dev, stg, prod := mk("dev"), mk("staging"), mk("prod")

	if err := plr.Set(ctx, pid, []string{dev, stg, prod}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := plr.Get(ctx, pid)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].EnvironmentID != dev || got[2].EnvironmentID != prod {
		t.Fatalf("Get = %+v", got)
	}
	if got[0].Position != 0 || got[1].Position != 1 || got[2].Position != 2 {
		t.Fatalf("positions = %+v, want 0,1,2", got)
	}
	next, ok, err := plr.NextEnv(ctx, pid, dev)
	if err != nil || !ok || next != stg {
		t.Fatalf("NextEnv(dev) = %q ok=%v err=%v, want staging", next, ok, err)
	}
	if _, ok, _ := plr.NextEnv(ctx, pid, prod); ok {
		t.Fatalf("NextEnv(prod) should be the last step (ok=false)")
	}
	// An env not in the pipeline has no next.
	unknown, _ := s.NewID(ctx)
	if _, ok, err := plr.NextEnv(ctx, pid, unknown); ok || err != nil {
		t.Fatalf("NextEnv(unknown) ok=%v err=%v, want false/nil", ok, err)
	}
	// Empty pipeline for an unknown project.
	other, _ := s.NewID(ctx)
	if steps, err := plr.Get(ctx, other); err != nil || len(steps) != 0 {
		t.Fatalf("Get(empty) = %+v err=%v, want empty", steps, err)
	}

	// Set replaces the whole ordering.
	if err := plr.Set(ctx, pid, []string{dev, stg}); err != nil {
		t.Fatal(err)
	}
	if got, _ := plr.Get(ctx, pid); len(got) != 2 {
		t.Fatalf("after replace len = %d, want 2", len(got))
	}
}
