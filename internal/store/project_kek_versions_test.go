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

// TestDeleteEmptyKeepsKEKReferencedByPendingEditRequest asserts that a KEK
// version referenced ONLY by a pending (or in-flight 'applying') config edit
// request is NOT retired by DeleteEmpty — otherwise that request's proposal
// blob could never be decrypted, bricking approval (Finding B). It also asserts
// that a version referenced by nothing IS retired.
func TestDeleteEmptyKeepsKEKReferencedByPendingEditRequest(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()

	projectID, _, configID := mkConfig(t, s, "prod")
	requester := mkUser(t, "req-editreq@example.com")

	kr := NewProjectKEKVersionRepo(s)
	cer := NewConfigEditRequestRepo(s)

	// Two superseded KEK versions: v1 referenced by a pending edit request, v2
	// referenced by nothing.
	if err := kr.Insert(ctx, s.pool, projectID, 1, []byte("wrapped-v1")); err != nil {
		t.Fatalf("insert v1: %v", err)
	}
	if err := kr.Insert(ctx, s.pool, projectID, 2, []byte("wrapped-v2")); err != nil {
		t.Fatalf("insert v2: %v", err)
	}

	// A pending edit request whose proposal DEK is wrapped under KEK version 1.
	if _, err := cer.Create(ctx, &ConfigEditRequest{
		ConfigID:           configID,
		RequestedBy:        requester,
		ProposedCiphertext: []byte("ct"),
		WrappedDEK:         []byte("wd"),
		Nonce:              []byte("nn"),
		DEKKeyVersion:      1,
		ChangedKeys:        []string{"K"},
	}); err != nil {
		t.Fatalf("create edit request: %v", err)
	}

	retired, err := kr.DeleteEmpty(ctx, projectID)
	if err != nil {
		t.Fatalf("DeleteEmpty: %v", err)
	}
	if len(retired) != 1 || retired[0] != 2 {
		t.Fatalf("DeleteEmpty = %v, want only [2] retired (v1 pinned by pending request)", retired)
	}
	// v1 must still be present (un-approvable-request guard).
	if _, err := kr.GetWrapped(ctx, projectID, 1); err != nil {
		t.Fatalf("v1 should be kept: GetWrapped(v1) = %v", err)
	}
	// v2 must be gone.
	if _, err := kr.GetWrapped(ctx, projectID, 2); err != ErrNotFound {
		t.Fatalf("v2 should be retired: GetWrapped(v2) = %v, want ErrNotFound", err)
	}
}
