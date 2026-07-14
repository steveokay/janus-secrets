package store

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// RewrapRow is one secret_values DEK to re-wrap. It deliberately excludes the
// value ciphertext/nonce — rotation re-wraps the DEK only and never decrypts a
// secret value.
type RewrapRow struct {
	ID            string
	ConfigID      string
	Key           string
	ValueVersion  int
	WrappedDEK    []byte
	DEKKeyVersion int
}

// RewrapBatch processes up to limit secret_values rows for a project whose
// dek_key_version < latest (across all configs, INCLUDING soft-deleted),
// keyset-paginated by secret_values.id ascending. For each row it calls
// rewrap(row) to obtain the DEK re-wrapped under the latest KEK, then updates
// wrapped_dek and dek_key_version in the same transaction. Returns the number
// processed and the next cursor (the last id processed; "" when the batch was
// not full, i.e. no more rows). Re-running resumes safely (processed rows are
// no longer < latest).
func (r *SecretRepo) RewrapBatch(ctx context.Context, projectID string, latest int, cursor string, limit int,
	rewrap func(RewrapRow) (newWrappedDEK []byte, err error)) (processed int, next string, err error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	err = r.s.withTx(ctx, func(tx pgx.Tx) error {
		rows, qerr := tx.Query(ctx,
			`SELECT sv.id::text, sv.config_id::text, sv.key, sv.value_version, sv.wrapped_dek, sv.dek_key_version
			   FROM secret_values sv
			   JOIN configs c ON c.id = sv.config_id
			   JOIN environments e ON e.id = c.environment_id
			  WHERE e.project_id = $1::uuid
			    AND sv.dek_key_version < $2
			    AND ($3 = '' OR sv.id > $3::uuid)
			  ORDER BY sv.id ASC
			  LIMIT $4`, projectID, latest, cursor, limit)
		if qerr != nil {
			return mapError(qerr)
		}
		var batch []RewrapRow
		for rows.Next() {
			var rr RewrapRow
			if err := rows.Scan(&rr.ID, &rr.ConfigID, &rr.Key, &rr.ValueVersion, &rr.WrappedDEK, &rr.DEKKeyVersion); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, rr)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return mapError(err)
		}
		for _, rr := range batch {
			newWrapped, rerr := rewrap(rr)
			if rerr != nil {
				return rerr
			}
			tag, uerr := tx.Exec(ctx,
				`UPDATE secret_values SET wrapped_dek=$2, dek_key_version=$3 WHERE id=$1::uuid`,
				rr.ID, newWrapped, latest)
			if uerr != nil {
				return mapError(uerr)
			}
			if tag.RowsAffected() != 1 {
				return ErrNotFound
			}
			processed++
			next = rr.ID
		}
		return nil
	})
	if err != nil {
		return 0, "", err
	}
	if processed < limit {
		next = ""
	}
	return processed, next, nil
}
