package store

import "context"

// AnnotationEntry is one per-key secret annotation row: an owner label and/or a
// free-text note. Value-free: only human-facing metadata, never secret material.
// Owner/Note are nil when the corresponding column is NULL.
type AnnotationEntry struct {
	Key   string
	Owner *string
	Note  *string
}

// AnnotationRepo stores per-config / per-key secret annotations (owner + note).
// Value-free: only key names and metadata strings are stored, never secret
// values.
type AnnotationRepo struct{ s *Store }

// NewAnnotationRepo returns an annotation repository.
func NewAnnotationRepo(s *Store) *AnnotationRepo { return &AnnotationRepo{s: s} }

// Set upserts an annotation for (configID, key). owner/note may each be nil to
// leave that column NULL; at least one must be non-nil (the DB CHECK enforces
// this too — callers should route an all-empty annotation to Clear). updatedBy
// may be "" (a service-token actor); stored as NULL.
func (r *AnnotationRepo) Set(ctx context.Context, configID, key string, owner, note *string, updatedBy string) error {
	var by any
	if updatedBy != "" {
		by = updatedBy
	}
	_, err := r.s.pool.Exec(ctx,
		`INSERT INTO config_secret_annotations (config_id, key, owner, note, updated_by, updated_at)
		 VALUES ($1::uuid, $2, $3, $4, $5, now())
		 ON CONFLICT (config_id, key)
		 DO UPDATE SET owner = EXCLUDED.owner, note = EXCLUDED.note,
		               updated_by = EXCLUDED.updated_by, updated_at = now()`,
		configID, key, owner, note, by)
	return mapError(err)
}

// Clear removes an annotation for (configID, key). Clearing an absent annotation
// is a no-op.
func (r *AnnotationRepo) Clear(ctx context.Context, configID, key string) error {
	_, err := r.s.pool.Exec(ctx,
		`DELETE FROM config_secret_annotations WHERE config_id=$1::uuid AND key=$2`, configID, key)
	return mapError(err)
}

// List returns a config's annotations, sorted by key.
func (r *AnnotationRepo) List(ctx context.Context, configID string) ([]AnnotationEntry, error) {
	rows, err := r.s.pool.Query(ctx,
		`SELECT key, owner, note FROM config_secret_annotations
		  WHERE config_id=$1::uuid ORDER BY key ASC`, configID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	out := []AnnotationEntry{}
	for rows.Next() {
		var e AnnotationEntry
		if err := rows.Scan(&e.Key, &e.Owner, &e.Note); err != nil {
			return nil, mapError(err)
		}
		out = append(out, e)
	}
	return out, mapError(rows.Err())
}
