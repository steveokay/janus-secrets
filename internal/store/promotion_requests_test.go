package store

import (
	"context"
	"errors"
	"testing"
)

// TestPromotionRequestLifecycle exercises the full promotion_requests repo
// lifecycle: create a pending request, list/get it, claim it for apply
// (single-winner), record the applied target version, and confirm the
// terminal state.
func TestPromotionRequestLifecycle(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()

	projectID, _, sourceConfigID := mkConfig(t, s, "prod")
	targetEnv, err := NewEnvironmentRepo(s).Create(ctx, projectID, "staging", "Staging")
	if err != nil {
		t.Fatalf("create target env: %v", err)
	}
	targetEnvID := targetEnv.ID
	requester := mkUser(t, "requester@example.com")
	approver := mkUser(t, "approver@example.com")

	repo := NewPromotionRequestRepo(s)

	in := &PromotionRequest{
		ProjectID:      projectID,
		SourceConfigID: sourceConfigID,
		SourceVersion:  1,
		TargetEnvID:    targetEnvID,
		TargetName:     "staging",
		CreateTarget:   false,
		Selections:     []PromotionSelection{{Key: "DB_PASSWORD", Action: "set"}},
		Note:           "promote db password",
		RequestedBy:    requester,
	}

	created, err := repo.Create(ctx, in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("want non-empty id, got %+v", created)
	}
	if created.Status != "pending" {
		t.Fatalf("want status pending, got %q", created.Status)
	}

	// Get round trip.
	got, err := repo.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Note != "promote db password" {
		t.Fatalf("want note preserved, got %q", got.Note)
	}
	if len(got.Selections) != 1 || got.Selections[0].Key != "DB_PASSWORD" || got.Selections[0].Action != "set" {
		t.Fatalf("want 1 selection DB_PASSWORD/set, got %+v", got.Selections)
	}
	if got.RequestedBy != requester {
		t.Fatalf("want requested_by %s, got %s", requester, got.RequestedBy)
	}
	if got.DecidedBy != nil || got.AppliedTargetVersion != nil || got.DecidedAt != nil {
		t.Fatalf("want nil decided_by/applied_target_version/decided_at, got %+v", got)
	}

	// ListByProject filters by status.
	list, err := repo.ListByProject(ctx, projectID, "pending")
	if err != nil {
		t.Fatalf("ListByProject: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("want 1 pending request, got %d", len(list))
	}
	if list[0].ID != created.ID {
		t.Fatalf("want listed id %s, got %s", created.ID, list[0].ID)
	}

	// ListByRequester too.
	byRequester, err := repo.ListByRequester(ctx, requester, "pending")
	if err != nil {
		t.Fatalf("ListByRequester: %v", err)
	}
	if len(byRequester) != 1 || byRequester[0].ID != created.ID {
		t.Fatalf("want 1 request by requester, got %+v", byRequester)
	}

	// MarkApplied atomically transitions pending -> applied with the version.
	if err := repo.MarkApplied(ctx, created.ID, approver, 2); err != nil {
		t.Fatalf("MarkApplied: %v", err)
	}

	// Second MarkApplied on the same (now non-pending) request -> ErrNotFound.
	if err := repo.MarkApplied(ctx, created.ID, approver, 3); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second MarkApplied: want ErrNotFound, got %v", err)
	}

	final, err := repo.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("final Get: %v", err)
	}
	if final.Status != "applied" {
		t.Fatalf("want status applied, got %q", final.Status)
	}
	if final.AppliedTargetVersion == nil || *final.AppliedTargetVersion != 2 {
		t.Fatalf("want applied_target_version 2, got %v", final.AppliedTargetVersion)
	}
	if final.DecidedBy == nil || *final.DecidedBy != approver {
		t.Fatalf("want decided_by %s, got %v", approver, final.DecidedBy)
	}
	if final.DecidedAt == nil {
		t.Fatalf("want decided_at set, got nil")
	}
}
