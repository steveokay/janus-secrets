package store

import (
	"context"
	"time"
)

// BreakGlassGrant is a time-boxed emergency role elevation for a single user on
// a single scope. The reason is operator-entered justification text (never a
// secret value). A grant applies only while now < ExpiresAt and RevokedAt is nil.
type BreakGlassGrant struct {
	ID            string
	UserID        string
	ScopeLevel    string // "instance" | "project" | "environment"
	ProjectID     *string
	EnvironmentID *string
	ElevatedRole  string // viewer | developer | admin | owner
	Reason        string
	ActivatedAt   time.Time
	ExpiresAt     time.Time
	RevokedAt     *time.Time
	CreatedAt     time.Time
}

// Active reports whether the grant confers its elevated role at instant now.
func (g *BreakGlassGrant) Active(now time.Time) bool {
	return g.RevokedAt == nil && now.Before(g.ExpiresAt)
}

// BreakGlassGrantInput is the activation payload.
type BreakGlassGrantInput struct {
	UserID        string
	ScopeLevel    string
	ProjectID     *string
	EnvironmentID *string
	ElevatedRole  string
	Reason        string
	ExpiresAt     time.Time
}

// BreakGlassRepo persists break_glass_grants rows.
type BreakGlassRepo struct{ s *Store }

// NewBreakGlassRepo returns a break-glass repository.
func NewBreakGlassRepo(s *Store) *BreakGlassRepo { return &BreakGlassRepo{s: s} }

const breakGlassCols = `id::text, user_id::text, scope_level,
	project_id::text, environment_id::text, elevated_role, reason,
	activated_at, expires_at, revoked_at, created_at`

func scanBreakGlass(row interface{ Scan(...any) error }) (*BreakGlassGrant, error) {
	var g BreakGlassGrant
	if err := row.Scan(&g.ID, &g.UserID, &g.ScopeLevel,
		&g.ProjectID, &g.EnvironmentID, &g.ElevatedRole, &g.Reason,
		&g.ActivatedAt, &g.ExpiresAt, &g.RevokedAt, &g.CreatedAt); err != nil {
		return nil, mapError(err)
	}
	return &g, nil
}

// Create inserts a new grant and returns it.
func (r *BreakGlassRepo) Create(ctx context.Context, in BreakGlassGrantInput) (*BreakGlassGrant, error) {
	row := r.s.pool.QueryRow(ctx,
		`INSERT INTO break_glass_grants
		   (user_id, scope_level, project_id, environment_id, elevated_role, reason, expires_at)
		 VALUES ($1::uuid, $2, $3::uuid, $4::uuid, $5, $6, $7)
		 RETURNING `+breakGlassCols,
		in.UserID, in.ScopeLevel, in.ProjectID, in.EnvironmentID, in.ElevatedRole, in.Reason, in.ExpiresAt)
	return scanBreakGlass(row)
}

// Get returns a single grant by id. ErrNotFound if it does not exist.
func (r *BreakGlassRepo) Get(ctx context.Context, id string) (*BreakGlassGrant, error) {
	row := r.s.pool.QueryRow(ctx,
		`SELECT `+breakGlassCols+` FROM break_glass_grants WHERE id = $1::uuid`, id)
	return scanBreakGlass(row)
}

// ListActiveForUser returns the user's grants that are still live at `now`
// (not revoked, not expired) — the authz-overlay hot path.
func (r *BreakGlassRepo) ListActiveForUser(ctx context.Context, userID string, now time.Time) ([]*BreakGlassGrant, error) {
	rows, err := r.s.pool.Query(ctx,
		`SELECT `+breakGlassCols+` FROM break_glass_grants
		 WHERE user_id = $1::uuid AND revoked_at IS NULL AND expires_at > $2`,
		userID, now)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	return collectBreakGlass(rows)
}

// ListActive returns every live grant across the instance (admin listing),
// most-recently-activated first.
func (r *BreakGlassRepo) ListActive(ctx context.Context, now time.Time) ([]*BreakGlassGrant, error) {
	rows, err := r.s.pool.Query(ctx,
		`SELECT `+breakGlassCols+` FROM break_glass_grants
		 WHERE revoked_at IS NULL AND expires_at > $1
		 ORDER BY activated_at DESC, id DESC`,
		now)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	return collectBreakGlass(rows)
}

// ListActiveForUserOrdered returns every live grant belonging to a specific
// user (a user's own listing), most-recently-activated first.
func (r *BreakGlassRepo) ListActiveForUserOrdered(ctx context.Context, userID string, now time.Time) ([]*BreakGlassGrant, error) {
	rows, err := r.s.pool.Query(ctx,
		`SELECT `+breakGlassCols+` FROM break_glass_grants
		 WHERE user_id = $1::uuid AND revoked_at IS NULL AND expires_at > $2
		 ORDER BY activated_at DESC, id DESC`,
		userID, now)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	return collectBreakGlass(rows)
}

// Revoke ends a grant early by stamping revoked_at = now, but only if it is not
// already revoked. Returns ErrNotFound if no live grant with that id exists.
func (r *BreakGlassRepo) Revoke(ctx context.Context, id string, now time.Time) error {
	return r.s.execAffectingOne(ctx,
		`UPDATE break_glass_grants SET revoked_at = $2
		 WHERE id = $1::uuid AND revoked_at IS NULL`,
		id, now)
}

// SweepExpired stamps revoked_at on grants that lapsed by expiry while
// unattended (purely cosmetic — the overlay already ignores expired grants by
// timestamp — so listings and audits can mark them ended). Returns the ids of
// grants transitioned so the caller can emit breakglass.expire audit events.
func (r *BreakGlassRepo) SweepExpired(ctx context.Context, now time.Time) ([]*BreakGlassGrant, error) {
	rows, err := r.s.pool.Query(ctx,
		`UPDATE break_glass_grants SET revoked_at = expires_at
		 WHERE revoked_at IS NULL AND expires_at <= $1
		 RETURNING `+breakGlassCols,
		now)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	return collectBreakGlass(rows)
}

func collectBreakGlass(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]*BreakGlassGrant, error) {
	var out []*BreakGlassGrant
	for rows.Next() {
		g, err := scanBreakGlass(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, mapError(rows.Err())
}
