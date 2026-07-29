package store

import (
	"context"
	"time"
)

// OutboundPolicyOverride is the persisted egress (SSRF) policy that supersedes
// the process environment once an operator sets one. Absence of a row — not a
// zero value — means "no override, use the environment", so Get returns
// ErrNotFound rather than an empty struct for an instance that has never used
// the screen.
//
// AllowProxy is deliberately absent: it stays environment-only, because it is
// the one setting that blinds the connect-time guard entirely.
type OutboundPolicyOverride struct {
	BlockPrivate bool
	Allow        []string
	UpdatedAt    time.Time
	// UpdatedBy is nil when the setting user has since been deleted (the FK is
	// ON DELETE SET NULL — losing the actor must not lose the policy).
	UpdatedBy *string
}

// OutboundPolicyRepo persists the single egress-policy override row.
type OutboundPolicyRepo struct{ s *Store }

// NewOutboundPolicyRepo returns the outbound-policy repository.
func NewOutboundPolicyRepo(s *Store) *OutboundPolicyRepo { return &OutboundPolicyRepo{s: s} }

// Get returns the override, or ErrNotFound when none is set.
func (r *OutboundPolicyRepo) Get(ctx context.Context) (*OutboundPolicyOverride, error) {
	var o OutboundPolicyOverride
	err := r.s.pool.QueryRow(ctx,
		`SELECT block_private, allow, updated_at, updated_by::text
		   FROM outbound_policy WHERE id = true`).
		Scan(&o.BlockPrivate, &o.Allow, &o.UpdatedAt, &o.UpdatedBy)
	if err != nil {
		return nil, mapError(err)
	}
	return &o, nil
}

// Put upserts the override and returns the stored row, so the caller reports
// the timestamp the database actually recorded rather than one it guessed.
//
// allow must already be parsed and normalised by the caller (nethard.ParseAllow)
// — this layer does not re-validate, because a second definition of a valid
// entry is exactly how enforcement and storage drift apart.
func (r *OutboundPolicyRepo) Put(ctx context.Context, blockPrivate bool, allow []string, userID string) (*OutboundPolicyOverride, error) {
	if allow == nil {
		allow = []string{} // never write NULL into a NOT NULL text[]
	}
	var actor any
	if userID != "" {
		actor = userID
	}
	var o OutboundPolicyOverride
	err := r.s.pool.QueryRow(ctx,
		`INSERT INTO outbound_policy (id, block_private, allow, updated_at, updated_by)
		 VALUES (true, $1, $2, now(), $3)
		 ON CONFLICT (id) DO UPDATE
		    SET block_private = EXCLUDED.block_private,
		        allow         = EXCLUDED.allow,
		        updated_at    = now(),
		        updated_by    = EXCLUDED.updated_by
		 RETURNING block_private, allow, updated_at, updated_by::text`,
		blockPrivate, allow, actor).
		Scan(&o.BlockPrivate, &o.Allow, &o.UpdatedAt, &o.UpdatedBy)
	if err != nil {
		return nil, mapError(err)
	}
	return &o, nil
}

// Delete removes the override, returning the instance to environment-only
// policy. Deleting a policy that does not exist is not an error — the caller's
// intent ("no override") is satisfied either way.
func (r *OutboundPolicyRepo) Delete(ctx context.Context) error {
	_, err := r.s.pool.Exec(ctx, `DELETE FROM outbound_policy WHERE id = true`)
	return mapError(err)
}
