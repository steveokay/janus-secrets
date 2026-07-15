package store

import (
	"context"
	"testing"
)

// seedDeletable creates a project → environment → config, then soft-deletes the
// config and environment (project stays live), returning the ids.
func seedDeletable(t *testing.T, s *Store) (pid, eid, cid string) {
	t.Helper()
	ctx := context.Background()
	pr := NewProjectRepo(s)
	er := NewEnvironmentRepo(s)
	cr := NewConfigRepo(s)
	pid0, err := s.NewID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, err := pr.Create(ctx, pid0, "trash-proj", "Trash Proj", []byte("wrapped-kek-000000000000000000"), 1)
	if err != nil {
		t.Fatal(err)
	}
	e, err := er.Create(ctx, p.ID, "dev", "Dev")
	if err != nil {
		t.Fatal(err)
	}
	c, err := cr.Create(ctx, e.ID, "root", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := cr.SoftDelete(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if err := er.SoftDelete(ctx, e.ID); err != nil {
		t.Fatal(err)
	}
	return p.ID, e.ID, c.ID
}

func TestListDeleted(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	_, eid, cid := seedDeletable(t, s)

	envs, err := NewEnvironmentRepo(s).ListDeleted(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(envs) != 1 || envs[0].ID != eid || envs[0].DeletedAt == nil {
		t.Fatalf("want 1 deleted env %s, got %+v", eid, envs)
	}
	cfgs, err := NewConfigRepo(s).ListDeleted(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfgs) != 1 || cfgs[0].ID != cid || cfgs[0].DeletedAt == nil {
		t.Fatalf("want 1 deleted config %s, got %+v", cid, cfgs)
	}
	projs, err := NewProjectRepo(s).ListDeleted(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(projs) != 0 {
		t.Fatalf("want 0 deleted projects, got %+v", projs)
	}
}

func TestProjectGetIncludingDeleted(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	pr := NewProjectRepo(s)
	pid0, err := s.NewID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, err := pr.Create(ctx, pid0, "gone", "Gone", []byte("wrapped-kek-000000000000000000"), 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := pr.SoftDelete(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pr.Get(ctx, p.ID); err == nil {
		t.Fatal("live Get should not see a soft-deleted project")
	}
	got, err := pr.GetIncludingDeleted(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetIncludingDeleted: %v", err)
	}
	if got.ID != p.ID || got.DeletedAt == nil {
		t.Fatalf("want deleted project %s, got %+v", p.ID, got)
	}
}
