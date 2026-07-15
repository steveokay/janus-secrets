package store

import "context"

// IdemRecord is a stored idempotency entry. StatusCode 0 means claimed but not
// yet completed (in flight). No response body is ever stored.
type IdemRecord struct {
	Endpoint    string
	RequestHash string
	StatusCode  int
}

// IdempotencyRepo stores at-most-once execution markers keyed by
// (idempotency_key, actor). It stores only the final HTTP status — never a
// response body — so once-shown secrets in a response can never persist here.
type IdempotencyRepo struct{ s *Store }

// NewIdempotencyRepo returns a generic idempotency repository.
func NewIdempotencyRepo(s *Store) *IdempotencyRepo { return &IdempotencyRepo{s: s} }

// Claim atomically inserts a pending row. claimed=true means THIS caller won and
// must run the handler then Complete/Release. When claimed=false, existing is
// the current record (StatusCode 0 = still pending).
func (r *IdempotencyRepo) Claim(ctx context.Context, key, actor, endpoint, requestHash string) (claimed bool, existing *IdemRecord, err error) {
	ct, err := r.s.pool.Exec(ctx,
		`INSERT INTO idempotency (idempotency_key, actor, endpoint, request_hash)
		 VALUES ($1, $2, $3, $4) ON CONFLICT (idempotency_key, actor) DO NOTHING`,
		key, actor, endpoint, requestHash)
	if err != nil {
		return false, nil, mapError(err)
	}
	if ct.RowsAffected() == 1 {
		return true, nil, nil
	}
	var rec IdemRecord
	if err := r.s.pool.QueryRow(ctx,
		`SELECT endpoint, request_hash, status_code FROM idempotency
		 WHERE idempotency_key=$1 AND actor=$2`, key, actor).
		Scan(&rec.Endpoint, &rec.RequestHash, &rec.StatusCode); err != nil {
		return false, nil, mapError(err)
	}
	return false, &rec, nil
}

// Complete records the final status for a previously claimed row.
func (r *IdempotencyRepo) Complete(ctx context.Context, key, actor string, status int) error {
	return r.s.execAffectingOne(ctx,
		`UPDATE idempotency SET status_code=$3, completed_at=now()
		 WHERE idempotency_key=$1 AND actor=$2`, key, actor, status)
}

// Release deletes a claimed row so a failed request can be retried with the key.
func (r *IdempotencyRepo) Release(ctx context.Context, key, actor string) error {
	_, err := r.s.pool.Exec(ctx,
		`DELETE FROM idempotency WHERE idempotency_key=$1 AND actor=$2`, key, actor)
	return mapError(err)
}
