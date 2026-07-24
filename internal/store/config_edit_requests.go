package store

import (
	"context"
	"encoding/json"
	"time"
)

// ConfigEditRequest is a proposed, not-yet-committed batch of secret edits to a
// protected config, awaiting four-eyes approval. The proposed changes are stored
// ENVELOPE-ENCRYPTED (ProposedCiphertext under a fresh DEK wrapped by the
// project KEK); the row's metadata is value-free (ChangedKeys holds key NAMES
// only, never values).
type ConfigEditRequest struct {
	ID                 string
	ConfigID           string
	RequestedBy        string
	Reason             string
	Status             string
	ProposedCiphertext []byte
	WrappedDEK         []byte
	Nonce              []byte
	DEKKeyVersion      int
	ChangedKeys        []string
	Message            string
	ResolvedBy         *string
	ResolvedAt         *time.Time
	AppliedVersion     *int
	CreatedAt          time.Time
}

// ConfigEditRequestRepo persists config_edit_requests rows.
type ConfigEditRequestRepo struct{ s *Store }

// NewConfigEditRequestRepo constructs a ConfigEditRequestRepo bound to s.
func NewConfigEditRequestRepo(s *Store) *ConfigEditRequestRepo {
	return &ConfigEditRequestRepo{s: s}
}

const configEditRequestColumns = `
	id::text, config_id::text, requested_by::text, reason, status,
	proposed_ciphertext, wrapped_dek, nonce, dek_key_version, changed_keys,
	message, resolved_by::text, resolved_at, applied_version, created_at`

func scanConfigEditRequest(row interface {
	Scan(dest ...any) error
}) (*ConfigEditRequest, error) {
	var er ConfigEditRequest
	var keysRaw []byte
	if err := row.Scan(
		&er.ID, &er.ConfigID, &er.RequestedBy, &er.Reason, &er.Status,
		&er.ProposedCiphertext, &er.WrappedDEK, &er.Nonce, &er.DEKKeyVersion,
		&keysRaw, &er.Message, &er.ResolvedBy, &er.ResolvedAt, &er.AppliedVersion,
		&er.CreatedAt,
	); err != nil {
		return nil, mapError(err)
	}
	if len(keysRaw) > 0 {
		if err := json.Unmarshal(keysRaw, &er.ChangedKeys); err != nil {
			return nil, err
		}
	}
	return &er, nil
}

// Create inserts a new pending edit request and returns the persisted row.
func (r *ConfigEditRequestRepo) Create(ctx context.Context, in *ConfigEditRequest) (*ConfigEditRequest, error) {
	keysJSON, err := json.Marshal(in.ChangedKeys)
	if err != nil {
		return nil, err
	}
	var id string
	err = r.s.pool.QueryRow(ctx, `
		INSERT INTO config_edit_requests
			(config_id, requested_by, reason, proposed_ciphertext, wrapped_dek,
			 nonce, dek_key_version, changed_keys, message)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8::jsonb, $9)
		RETURNING id::text`,
		in.ConfigID, in.RequestedBy, in.Reason, in.ProposedCiphertext, in.WrappedDEK,
		in.Nonce, in.DEKKeyVersion, keysJSON, in.Message,
	).Scan(&id)
	if err != nil {
		return nil, mapError(err)
	}
	return r.Get(ctx, id)
}

// Get fetches an edit request by id.
func (r *ConfigEditRequestRepo) Get(ctx context.Context, id string) (*ConfigEditRequest, error) {
	row := r.s.pool.QueryRow(ctx, `SELECT `+configEditRequestColumns+`
		FROM config_edit_requests WHERE id = $1::uuid`, id)
	return scanConfigEditRequest(row)
}

// ListByConfig lists edit requests for a config, optionally filtered by status
// (empty string means all), newest first.
func (r *ConfigEditRequestRepo) ListByConfig(ctx context.Context, configID, status string) ([]*ConfigEditRequest, error) {
	return r.listBy(ctx, "config_id", configID, status)
}

// ListByRequester lists edit requests filed by a given user, optionally
// filtered by status, newest first.
func (r *ConfigEditRequestRepo) ListByRequester(ctx context.Context, userID, status string) ([]*ConfigEditRequest, error) {
	return r.listBy(ctx, "requested_by", userID, status)
}

func (r *ConfigEditRequestRepo) listBy(ctx context.Context, column, value, status string) ([]*ConfigEditRequest, error) {
	query := `SELECT ` + configEditRequestColumns + `
		FROM config_edit_requests WHERE ` + column + ` = $1::uuid`
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

	var out []*ConfigEditRequest
	for rows.Next() {
		er, err := scanConfigEditRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, er)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// MarkApplied atomically transitions a pending request to applied, recording
// the approver and the resulting config version in one statement. Called ONLY
// after the edits have actually been committed, so an applied row always
// corresponds to a real save (no stranded state on a commit failure). Returns
// ErrNotFound if the request is missing or no longer pending (a concurrent
// approver won the race).
func (r *ConfigEditRequestRepo) MarkApplied(ctx context.Context, id, approver string, version int) error {
	return r.s.execAffectingOne(ctx, `
		UPDATE config_edit_requests
		SET status = 'applied', resolved_by = $2::uuid, resolved_at = now(), applied_version = $3
		WHERE id = $1::uuid AND status = 'pending'`,
		id, approver, version)
}

// Decide transitions a pending request to a terminal decision ("rejected" or
// "cancelled") recorded by decidedBy.
func (r *ConfigEditRequestRepo) Decide(ctx context.Context, id, to, decidedBy string) error {
	return r.s.execAffectingOne(ctx, `
		UPDATE config_edit_requests
		SET status = $2, resolved_by = $3::uuid, resolved_at = now()
		WHERE id = $1::uuid AND status = 'pending'`,
		id, to, decidedBy)
}
