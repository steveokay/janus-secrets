package authz

import (
	"context"

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

// Engine decides permissions and manages role bindings.
type Engine struct{ bindings BindingStore }

// New returns an Engine over the given binding store.
func New(bindings BindingStore) *Engine { return &Engine{bindings: bindings} }

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
		return ErrForbidden
	default:
		return ErrForbidden
	}
}
