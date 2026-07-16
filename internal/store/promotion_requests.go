package store

import (
	"context"
	"encoding/json"
	"time"
)

// PromotionSelection identifies one key/action pair chosen for a promotion
// request (e.g. "set" an updated value, "delete" a removed one).
type PromotionSelection struct {
	Key    string `json:"key"`
	Action string `json:"action"`
}

// PromotionRequest is an approval-gated request to promote a source config
// version's selected secrets into a target environment/config.
type PromotionRequest struct {
	ID                   string
	ProjectID            string
	SourceConfigID       string
	SourceVersion        int
	TargetConfigID       *string
	TargetEnvID          string
	TargetName           string
	CreateTarget         bool
	Selections           []PromotionSelection
	Note                 string
	Status               string
	RequestedBy          string
	DecidedBy            *string
	DecisionNote         string
	AppliedTargetVersion *int
	CreatedAt            time.Time
	DecidedAt            *time.Time
}

// PromotionRequestRepo persists promotion_requests rows.
type PromotionRequestRepo struct{ s *Store }

// NewPromotionRequestRepo constructs a PromotionRequestRepo bound to s.
func NewPromotionRequestRepo(s *Store) *PromotionRequestRepo { return &PromotionRequestRepo{s: s} }

const promotionRequestColumns = `
	id::text, project_id::text, source_config_id::text, source_version,
	target_config_id::text, target_env_id::text, target_name, create_target,
	selections, note, status, requested_by::text, decided_by::text,
	decision_note, applied_target_version, created_at, decided_at`

func scanPromotionRequest(row interface {
	Scan(dest ...any) error
}) (*PromotionRequest, error) {
	var pr PromotionRequest
	var selectionsRaw []byte
	if err := row.Scan(
		&pr.ID, &pr.ProjectID, &pr.SourceConfigID, &pr.SourceVersion,
		&pr.TargetConfigID, &pr.TargetEnvID, &pr.TargetName, &pr.CreateTarget,
		&selectionsRaw, &pr.Note, &pr.Status, &pr.RequestedBy, &pr.DecidedBy,
		&pr.DecisionNote, &pr.AppliedTargetVersion, &pr.CreatedAt, &pr.DecidedAt,
	); err != nil {
		return nil, mapError(err)
	}
	if len(selectionsRaw) > 0 {
		if err := json.Unmarshal(selectionsRaw, &pr.Selections); err != nil {
			return nil, err
		}
	}
	return &pr, nil
}

// Create inserts a new pending promotion request and returns the persisted row.
func (r *PromotionRequestRepo) Create(ctx context.Context, in *PromotionRequest) (*PromotionRequest, error) {
	selectionsJSON, err := json.Marshal(in.Selections)
	if err != nil {
		return nil, err
	}
	var id string
	err = r.s.pool.QueryRow(ctx, `
		INSERT INTO promotion_requests
			(project_id, source_config_id, source_version, target_config_id,
			 target_env_id, target_name, create_target, selections, note, requested_by)
		VALUES ($1::uuid, $2::uuid, $3, $4::uuid, $5::uuid, $6, $7, $8::jsonb, $9, $10::uuid)
		RETURNING id::text`,
		in.ProjectID, in.SourceConfigID, in.SourceVersion, in.TargetConfigID,
		in.TargetEnvID, in.TargetName, in.CreateTarget, selectionsJSON, in.Note, in.RequestedBy,
	).Scan(&id)
	if err != nil {
		return nil, mapError(err)
	}
	return r.Get(ctx, id)
}

// Get fetches a promotion request by id.
func (r *PromotionRequestRepo) Get(ctx context.Context, id string) (*PromotionRequest, error) {
	row := r.s.pool.QueryRow(ctx, `SELECT `+promotionRequestColumns+`
		FROM promotion_requests WHERE id = $1::uuid`, id)
	return scanPromotionRequest(row)
}

// ListByProject lists promotion requests for a project, optionally filtered
// by status (empty string means all statuses), newest first.
func (r *PromotionRequestRepo) ListByProject(ctx context.Context, projectID, status string) ([]*PromotionRequest, error) {
	return r.listBy(ctx, "project_id", projectID, status)
}

// ListByRequester lists promotion requests filed by a given user, optionally
// filtered by status, newest first.
func (r *PromotionRequestRepo) ListByRequester(ctx context.Context, userID, status string) ([]*PromotionRequest, error) {
	return r.listBy(ctx, "requested_by", userID, status)
}

func (r *PromotionRequestRepo) listBy(ctx context.Context, column, value, status string) ([]*PromotionRequest, error) {
	query := `SELECT ` + promotionRequestColumns + `
		FROM promotion_requests WHERE ` + column + ` = $1::uuid`
	args := []any{value}
	if status != "" {
		query += ` AND status = $2`
		args = append(args, status)
	}
	query += ` ORDER BY created_at DESC, id DESC`

	rows, err := r.s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	var out []*PromotionRequest
	for rows.Next() {
		pr, err := scanPromotionRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, pr)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ClaimForApply atomically transitions a pending request to applied, recording
// the approver. Returns ErrNotFound if the request is missing or not pending
// (e.g. already claimed by a concurrent approver).
func (r *PromotionRequestRepo) ClaimForApply(ctx context.Context, id, approver string) error {
	return r.s.execAffectingOne(ctx, `
		UPDATE promotion_requests
		SET status = 'applied', decided_by = $2::uuid, decided_at = now()
		WHERE id = $1::uuid AND status = 'pending'`,
		id, approver)
}

// SetAppliedVersion records the resulting target config version on an
// already-applied request.
func (r *PromotionRequestRepo) SetAppliedVersion(ctx context.Context, id string, version int) error {
	return r.s.execAffectingOne(ctx, `
		UPDATE promotion_requests
		SET applied_target_version = $2
		WHERE id = $1::uuid AND status = 'applied'`,
		id, version)
}

// RevertToPending undoes a claim (e.g. because applying the promotion failed
// after the claim was taken), clearing decision/applied-version fields.
func (r *PromotionRequestRepo) RevertToPending(ctx context.Context, id string) error {
	return r.s.execAffectingOne(ctx, `
		UPDATE promotion_requests
		SET status = 'pending', decided_by = NULL, decided_at = NULL, applied_target_version = NULL
		WHERE id = $1::uuid AND status = 'applied'`,
		id)
}

// Decide transitions a pending request to a terminal decision (e.g.
// "rejected") recorded by decidedBy with an optional note.
func (r *PromotionRequestRepo) Decide(ctx context.Context, id, to, decidedBy, note string) error {
	return r.s.execAffectingOne(ctx, `
		UPDATE promotion_requests
		SET status = $2, decided_by = $3::uuid, decision_note = $4, decided_at = now()
		WHERE id = $1::uuid AND status = 'pending'`,
		id, to, decidedBy, note)
}
