package authz

import (
	"context"
	"time"

	"github.com/steveokay/janus-secrets/internal/auth"
	"github.com/steveokay/janus-secrets/internal/store"
)

// BindingStore is the subset of the store the engine needs (tests fake it).
type BindingStore interface {
	ListForUser(ctx context.Context, userID string) ([]*store.RoleBinding, error)
	ListForScope(ctx context.Context, level, scopeID string) ([]*store.RoleBinding, error)
	ListForScopePage(ctx context.Context, level, scopeID string, limit int, after *store.Cursor) ([]*store.RoleBinding, error)
	Create(ctx context.Context, in store.RoleBindingInput) (*store.RoleBinding, error)
	DeleteForSubjectScope(ctx context.Context, subjectUserID, level string, projectID, environmentID *string) error
	CountInstanceOwners(ctx context.Context) (int, error)
}

// GrantStore is the subset of the break-glass store the engine overlays. It is
// OPTIONAL — an Engine with no grant store behaves exactly as before (plain
// bindings). ListActiveForUser must return ONLY grants that are live at `now`
// (non-revoked, non-expired); the engine additionally re-checks the timestamp
// so a stale row can never elevate.
type GrantStore interface {
	ListActiveForUser(ctx context.Context, userID string, now time.Time) ([]*store.BreakGlassGrant, error)
}

// Engine decides permissions and manages role bindings. When grants is non-nil
// the effective role for (user, scope) is the max of the bound role and any
// active break-glass grant on that exact scope.
type Engine struct {
	bindings BindingStore
	grants   GrantStore
	now      func() time.Time
}

// New returns an Engine over the given binding store (no break-glass overlay).
func New(bindings BindingStore) *Engine {
	return &Engine{bindings: bindings, now: time.Now}
}

// WithGrants attaches a break-glass grant store so active grants overlay the
// bound role in Can/EffectiveRole. Returns the receiver for chaining. Passing
// nil is a no-op (leaves the overlay disabled).
func (e *Engine) WithGrants(g GrantStore) *Engine {
	e.grants = g
	return e
}

// SetClock overrides the engine's clock (tests). nil restores time.Now.
func (e *Engine) SetClock(now func() time.Time) {
	if now == nil {
		now = time.Now
	}
	e.now = now
}

// TokenScope is the effective scope of a service token.
type TokenScope struct {
	Kind   string // "config" | "environment"
	ID     string
	Access string // "read" | "readwrite"
}

// Can decides whether the principal may perform action on resource. For a
// service-token principal, scope carries its token scope (nil → deny); for a
// user principal scope is ignored. Returns nil or ErrForbidden.
func (e *Engine) Can(ctx context.Context, p auth.Principal, scope *TokenScope, action Action, res Resource) error {
	switch p.Kind {
	case auth.KindServiceToken:
		if scope != nil && tokenAllows(*scope, action, res) {
			return nil
		}
		return ErrForbidden
	case auth.KindUser:
		bindings, err := e.bindings.ListForUser(ctx, p.ID)
		if err != nil {
			return err
		}
		if userAllows(bindings, action, res) {
			return nil
		}
		// Break-glass overlay: an active grant on the applicable scope may raise
		// the effective role enough to allow the action. Deny-by-default: a nil
		// grant store, no active grant, or a grant that does not apply to res all
		// fall through to ErrForbidden.
		allowed, err := e.grantAllows(ctx, p.ID, action, res)
		if err != nil {
			return err
		}
		if allowed {
			return nil
		}
		return ErrForbidden
	default:
		return ErrForbidden
	}
}

// grantAllows reports whether an active break-glass grant on a scope applying to
// res confers action. A grant is scoped exactly like a role binding: it only
// applies to the scope it was activated on (a project grant never leaks to a
// sibling env, etc.). The engine re-checks liveness against its own clock so a
// store bug that returned a stale row still cannot elevate.
func (e *Engine) grantAllows(ctx context.Context, userID string, action Action, res Resource) (bool, error) {
	if e.grants == nil {
		return false, nil
	}
	now := e.now()
	gs, err := e.grants.ListActiveForUser(ctx, userID, now)
	if err != nil {
		return false, err
	}
	for _, g := range gs {
		if !g.Active(now) {
			continue
		}
		if grantApplies(g, res) && roleAllows(Role(g.ElevatedRole), action) {
			return true, nil
		}
	}
	return false, nil
}
