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
