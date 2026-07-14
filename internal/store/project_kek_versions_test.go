package store

import (
	"context"
	"testing"
)

func TestProjectKEKVersionsInsertGetListDelete(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	pr := NewProjectRepo(s)
	kr := NewProjectKEKVersionRepo(s)

	id, err := s.NewID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pr.Create(ctx, id, "proj", "Proj", []byte("wrapped-latest"), 2); err != nil {
		t.Fatal(err)
	}
	if err := kr.Insert(ctx, s.pool, id, 1, []byte("wrapped-v1")); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err := kr.GetWrapped(ctx, id, 1)
	if err != nil || string(got) != "wrapped-v1" {
		t.Fatalf("GetWrapped = %q, %v", got, err)
	}
	if _, err := kr.GetWrapped(ctx, id, 99); err != ErrNotFound {
		t.Fatalf("GetWrapped(missing) = %v, want ErrNotFound", err)
	}
	pend, err := kr.ListPending(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(pend) != 1 || pend[0].Version != 1 || pend[0].DEKCount != 0 {
		t.Fatalf("ListPending = %+v", pend)
	}
	retired, err := kr.DeleteEmpty(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(retired) != 1 || retired[0] != 1 {
		t.Fatalf("DeleteEmpty = %v, want [1]", retired)
	}
	if _, err := kr.GetWrapped(ctx, id, 1); err != ErrNotFound {
		t.Fatalf("after delete GetWrapped = %v, want ErrNotFound", err)
	}
}
