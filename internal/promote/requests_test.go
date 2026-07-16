package promote

import (
	"context"
	"testing"

	"github.com/steveokay/janus-secrets/internal/store"
)

// seedUsers creates two distinct users (requester, approver) for request
// lifecycle tests. Skips the test (via newHarness's own skip) only when
// postgres/docker is unavailable — newHarness already asserts that.
func seedUsers(t *testing.T) (requester, approver string) {
	t.Helper()
	ctx := context.Background()
	users := store.NewUserRepo(testStore)
	slug := uniqueSlug("requester")
	req, err := users.Create(ctx, slug+"@example.com", nil)
	if err != nil {
		t.Fatalf("create requester user: %v", err)
	}
	slug2 := uniqueSlug("approver")
	appr, err := users.Create(ctx, slug2+"@example.com", nil)
	if err != nil {
		t.Fatalf("create approver user: %v", err)
	}
	return req.ID, appr.ID
}

// TestRequestApproveApplies creates a promotion request against a seeded
// source config, approves it, and asserts the underlying Apply ran exactly
// once (a second approval must conflict).
func TestRequestApproveApplies(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	requester, approver := seedUsers(t)

	h.setSecrets(t, h.devCfg, map[string]string{"A": "1"})

	id, err := h.svc.CreateRequest(ctx, CreateRequestInput{
		SourceConfigID: h.devCfg,
		TargetConfigID: h.stgCfg,
		SourceVersion:  1,
		Selections:     []Selection{{Key: "A", Action: ActionSet}},
		Note:           "please promote",
		RequestedBy:    requester,
	})
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	if id == "" {
		t.Fatal("CreateRequest returned empty id")
	}

	res, err := h.svc.ApproveRequest(ctx, id, approver)
	if err != nil {
		t.Fatalf("ApproveRequest: %v", err)
	}
	if res.TargetVersion < 1 {
		t.Errorf("TargetVersion = %d, want >= 1", res.TargetVersion)
	}
	if len(res.Applied) != 1 {
		t.Errorf("Applied = %v, want 1 entry", res.Applied)
	}

	if _, err := h.svc.ApproveRequest(ctx, id, approver); err == nil {
		t.Fatal("second ApproveRequest should have errored (conflict)")
	}
}

// TestRequestRejectAndCancel exercises the two other terminal transitions:
// an approver rejecting a request, and the requester cancelling their own.
func TestRequestRejectAndCancel(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	requester, approver := seedUsers(t)

	h.setSecrets(t, h.devCfg, map[string]string{"A": "1"})

	id1, err := h.svc.CreateRequest(ctx, CreateRequestInput{
		SourceConfigID: h.devCfg,
		TargetConfigID: h.stgCfg,
		SourceVersion:  1,
		Selections:     []Selection{{Key: "A", Action: ActionSet}},
		Note:           "req1",
		RequestedBy:    requester,
	})
	if err != nil {
		t.Fatalf("CreateRequest 1: %v", err)
	}
	id2, err := h.svc.CreateRequest(ctx, CreateRequestInput{
		SourceConfigID: h.devCfg,
		TargetConfigID: h.stgCfg,
		SourceVersion:  1,
		Selections:     []Selection{{Key: "A", Action: ActionSet}},
		Note:           "req2",
		RequestedBy:    requester,
	})
	if err != nil {
		t.Fatalf("CreateRequest 2: %v", err)
	}

	if err := h.svc.RejectRequest(ctx, id1, approver, "no"); err != nil {
		t.Fatalf("RejectRequest: %v", err)
	}
	if err := h.svc.CancelRequest(ctx, id2, requester); err != nil {
		t.Fatalf("CancelRequest: %v", err)
	}
}
