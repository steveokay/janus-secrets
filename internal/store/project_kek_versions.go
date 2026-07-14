package store

import (
	"context"

	"github.com/jackc/pgx/v5/pgconn"
)

// ProjectKEKVersionRepo persists superseded project-KEK versions (wrapped under
// the master) still referenced by not-yet-rewrapped DEKs.
type ProjectKEKVersionRepo struct{ s *Store }

func NewProjectKEKVersionRepo(s *Store) *ProjectKEKVersionRepo { return &ProjectKEKVersionRepo{s: s} }

// PendingVersion is a superseded KEK version and how many DEKs still point at it.
type PendingVersion struct {
	Version  int
	DEKCount int
}

// execer is satisfied by *pgxpool.Pool and pgx.Tx.
type execer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

// Insert records a superseded (project, version) wrapped KEK on the given executor.
func (r *ProjectKEKVersionRepo) Insert(ctx context.Context, ex execer, projectID string, version int, wrappedKEK []byte) error {
	_, err := ex.Exec(ctx,
		`INSERT INTO project_kek_versions (project_id, version, wrapped_kek) VALUES ($1::uuid, $2, $3)`,
		projectID, version, wrappedKEK)
	return mapError(err)
}

// GetWrapped returns the wrapped KEK for a superseded version, or ErrNotFound.
func (r *ProjectKEKVersionRepo) GetWrapped(ctx context.Context, projectID string, version int) ([]byte, error) {
	var b []byte
	err := r.s.pool.QueryRow(ctx,
		`SELECT wrapped_kek FROM project_kek_versions WHERE project_id=$1::uuid AND version=$2`,
		projectID, version).Scan(&b)
	if err != nil {
		return nil, mapError(err)
	}
	return b, nil
}

// ListPending returns every superseded version for a project with the count of
// secret_values DEKs still at that version, oldest first.
//
// A config belongs to a project through its environment
// (secret_values -> configs -> environments -> projects); there is no
// project_id column on configs, so the count joins via environments.
func (r *ProjectKEKVersionRepo) ListPending(ctx context.Context, projectID string) ([]PendingVersion, error) {
	rows, err := r.s.pool.Query(ctx,
		`SELECT v.version,
		        (SELECT count(*) FROM secret_values sv
		           JOIN configs c ON c.id = sv.config_id
		           JOIN environments e ON e.id = c.environment_id
		          WHERE e.project_id = $1::uuid AND sv.dek_key_version = v.version)
		   FROM project_kek_versions v
		  WHERE v.project_id = $1::uuid
		  ORDER BY v.version ASC`, projectID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	out := []PendingVersion{}
	for rows.Next() {
		var p PendingVersion
		if err := rows.Scan(&p.Version, &p.DEKCount); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, mapError(rows.Err())
}

// DeleteEmpty removes superseded versions no DEK references anymore, returning
// the deleted version numbers.
func (r *ProjectKEKVersionRepo) DeleteEmpty(ctx context.Context, projectID string) ([]int, error) {
	rows, err := r.s.pool.Query(ctx,
		`DELETE FROM project_kek_versions v
		  WHERE v.project_id = $1::uuid
		    AND NOT EXISTS (
		      SELECT 1 FROM secret_values sv
		        JOIN configs c ON c.id = sv.config_id
		        JOIN environments e ON e.id = c.environment_id
		       WHERE e.project_id = $1::uuid AND sv.dek_key_version = v.version)
		 RETURNING version`, projectID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	out := []int{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, mapError(rows.Err())
}
