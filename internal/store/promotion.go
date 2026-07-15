package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// PipelineStep is one env in a project's ordered release pipeline.
type PipelineStep struct {
	Position      int
	EnvironmentID string
}

// PipelineRepo stores a project's ordered promotion pipeline.
type PipelineRepo struct{ s *Store }

// NewPipelineRepo returns a pipeline repository.
func NewPipelineRepo(s *Store) *PipelineRepo { return &PipelineRepo{s: s} }

// Get returns the pipeline steps in order (empty if none configured).
func (r *PipelineRepo) Get(ctx context.Context, projectID string) ([]PipelineStep, error) {
	rows, err := r.s.pool.Query(ctx,
		`SELECT position, environment_id::text FROM promotion_pipeline_steps
		  WHERE project_id=$1::uuid ORDER BY position ASC`, projectID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	out := []PipelineStep{}
	for rows.Next() {
		var st PipelineStep
		if err := rows.Scan(&st.Position, &st.EnvironmentID); err != nil {
			return nil, mapError(err)
		}
		out = append(out, st)
	}
	return out, mapError(rows.Err())
}

// Set replaces the whole ordering in one transaction; positions become 0..n-1.
func (r *PipelineRepo) Set(ctx context.Context, projectID string, envIDs []string) error {
	return r.s.withTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`DELETE FROM promotion_pipeline_steps WHERE project_id=$1::uuid`, projectID); err != nil {
			return mapError(err)
		}
		for i, eid := range envIDs {
			if _, err := tx.Exec(ctx,
				`INSERT INTO promotion_pipeline_steps (project_id, position, environment_id)
				 VALUES ($1::uuid, $2, $3::uuid)`, projectID, i, eid); err != nil {
				return mapError(err)
			}
		}
		return nil
	})
}

// NextEnv returns the environment immediately after envID. ok is false when
// envID is the last step or not in the pipeline.
func (r *PipelineRepo) NextEnv(ctx context.Context, projectID, envID string) (string, bool, error) {
	var next string
	err := r.s.pool.QueryRow(ctx,
		`SELECT environment_id::text FROM promotion_pipeline_steps
		  WHERE project_id=$1::uuid
		    AND position = (SELECT position + 1 FROM promotion_pipeline_steps
		                     WHERE project_id=$1::uuid AND environment_id=$2::uuid)`,
		projectID, envID).Scan(&next)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, mapError(err)
	}
	return next, true, nil
}

// LockedKeyRepo stores keys protected from promotion overwrite/removal, per config.
type LockedKeyRepo struct{ s *Store }

// NewLockedKeyRepo returns a locked-key repository.
func NewLockedKeyRepo(s *Store) *LockedKeyRepo { return &LockedKeyRepo{s: s} }

// Lock marks a key protected on a config. Idempotent (re-locking is a no-op).
// createdBy may be "" (a service-token actor); stored as NULL.
func (r *LockedKeyRepo) Lock(ctx context.Context, configID, key, createdBy string) error {
	var by any
	if createdBy != "" {
		by = createdBy
	}
	_, err := r.s.pool.Exec(ctx,
		`INSERT INTO config_locked_keys (config_id, key, created_by)
		 VALUES ($1::uuid, $2, $3)
		 ON CONFLICT (config_id, key) DO NOTHING`, configID, key, by)
	return mapError(err)
}

// Unlock removes a key's protection. Removing an absent key is a no-op.
func (r *LockedKeyRepo) Unlock(ctx context.Context, configID, key string) error {
	_, err := r.s.pool.Exec(ctx,
		`DELETE FROM config_locked_keys WHERE config_id=$1::uuid AND key=$2`, configID, key)
	return mapError(err)
}

// List returns a config's locked keys, sorted.
func (r *LockedKeyRepo) List(ctx context.Context, configID string) ([]string, error) {
	rows, err := r.s.pool.Query(ctx,
		`SELECT key FROM config_locked_keys WHERE config_id=$1::uuid ORDER BY key ASC`, configID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, mapError(err)
		}
		out = append(out, k)
	}
	return out, mapError(rows.Err())
}

// AreLocked reports which of keys are locked on the config.
func (r *LockedKeyRepo) AreLocked(ctx context.Context, configID string, keys []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(keys) == 0 {
		return out, nil
	}
	rows, err := r.s.pool.Query(ctx,
		`SELECT key FROM config_locked_keys WHERE config_id=$1::uuid AND key = ANY($2)`, configID, keys)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, mapError(err)
		}
		out[k] = true
	}
	return out, mapError(rows.Err())
}

// IdempotencyRecord is a stored promote-apply idempotency entry. Response is nil
// while the request is still in flight (claimed but not yet completed).
type IdempotencyRecord struct {
	RequestHash string
	Response    []byte // nil = pending
}

// PromotionIdempotencyRepo stores promote-apply idempotency entries so a retried
// request with the same Idempotency-Key replays the original result instead of
// re-applying. The stored response is value-free (target version + applied key
// names only) — never a secret value.
type PromotionIdempotencyRepo struct{ s *Store }

// NewPromotionIdempotencyRepo returns a promotion-idempotency repository.
func NewPromotionIdempotencyRepo(s *Store) *PromotionIdempotencyRepo {
	return &PromotionIdempotencyRepo{s: s}
}

// Claim atomically inserts a pending row for (key, actor). Returns claimed=true
// when THIS caller won the claim (must proceed then Complete/Release). When
// claimed=false the key already exists and existing is its current record
// (possibly still pending, i.e. Response==nil).
func (r *PromotionIdempotencyRepo) Claim(ctx context.Context, key, actor, requestHash string) (claimed bool, existing *IdempotencyRecord, err error) {
	ct, err := r.s.pool.Exec(ctx,
		`INSERT INTO promotion_idempotency (idempotency_key, actor, request_hash)
		 VALUES ($1, $2, $3) ON CONFLICT (idempotency_key, actor) DO NOTHING`, key, actor, requestHash)
	if err != nil {
		return false, nil, mapError(err)
	}
	if ct.RowsAffected() == 1 {
		return true, nil, nil
	}
	var rec IdempotencyRecord
	if err := r.s.pool.QueryRow(ctx,
		`SELECT request_hash, response FROM promotion_idempotency
		 WHERE idempotency_key=$1 AND actor=$2`, key, actor).Scan(&rec.RequestHash, &rec.Response); err != nil {
		return false, nil, mapError(err)
	}
	return false, &rec, nil
}

// Complete stores the final response for a previously claimed row.
func (r *PromotionIdempotencyRepo) Complete(ctx context.Context, key, actor string, response []byte) error {
	return r.s.execAffectingOne(ctx,
		`UPDATE promotion_idempotency SET response=$3 WHERE idempotency_key=$1 AND actor=$2`, key, actor, response)
}

// Release deletes a claimed row so a failed apply can be retried with the same key.
func (r *PromotionIdempotencyRepo) Release(ctx context.Context, key, actor string) error {
	_, err := r.s.pool.Exec(ctx,
		`DELETE FROM promotion_idempotency WHERE idempotency_key=$1 AND actor=$2`, key, actor)
	return mapError(err)
}
