package authz

import (
	"context"
	"testing"
	"time"

	"github.com/steveokay/janus-secrets/internal/auth"
	"github.com/steveokay/janus-secrets/internal/store"
)

// fakeGroupBindings is the optional group-binding source. It mirrors what
// store.GroupBindingRepo.ListForUser returns: ordinary RoleBindings stamped
// with ViaGroupID.
type fakeGroupBindings struct {
	byUser map[string][]*store.RoleBinding
	err    error
	calls  int
}

func (f *fakeGroupBindings) ListForUser(_ context.Context, uid string) ([]*store.RoleBinding, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.byUser[uid], nil
}

func viaGroup(gid, level, role string, project, env *string) *store.RoleBinding {
	return &store.RoleBinding{
		ScopeLevel: level, ProjectID: project, EnvironmentID: env,
		Role: role, ViaGroupID: ptr(gid),
	}
}

// A group binding grants exactly like a direct one, and unions with it — the
// engine's single rule, with no precedence tier between the two sources.
func TestGroupBindingsUnionWithDirect(t *testing.T) {
	uid := "u1"
	tests := []struct {
		name    string
		direct  []*store.RoleBinding
		group   []*store.RoleBinding
		action  Action
		res     Resource
		allowed bool
	}{
		{
			name:    "group binding alone grants",
			group:   []*store.RoleBinding{viaGroup("g1", "project", "developer", ptr("P"), nil)},
			action:  SecretWrite,
			res:     Resource{ProjectID: "P"},
			allowed: true,
		},
		{
			name:    "group binding does not leak to another project",
			group:   []*store.RoleBinding{viaGroup("g1", "project", "admin", ptr("P"), nil)},
			action:  SecretRead,
			res:     Resource{ProjectID: "OTHER"},
			allowed: false,
		},
		{
			name:    "group binding does not leak to a sibling env",
			group:   []*store.RoleBinding{viaGroup("g1", "environment", "admin", nil, ptr("E"))},
			action:  SecretRead,
			res:     Resource{EnvID: "OTHER"},
			allowed: false,
		},
		{
			name:    "union: direct viewer + group developer allows a write",
			direct:  []*store.RoleBinding{{SubjectUserID: uid, ScopeLevel: "project", ProjectID: ptr("P"), Role: "viewer"}},
			group:   []*store.RoleBinding{viaGroup("g1", "project", "developer", ptr("P"), nil)},
			action:  SecretWrite,
			res:     Resource{ProjectID: "P"},
			allowed: true,
		},
		{
			name:    "union does not invent permissions neither side has",
			direct:  []*store.RoleBinding{{SubjectUserID: uid, ScopeLevel: "project", ProjectID: ptr("P"), Role: "viewer"}},
			group:   []*store.RoleBinding{viaGroup("g1", "project", "developer", ptr("P"), nil)},
			action:  ProjectDelete, // owner-only
			res:     Resource{ProjectID: "P"},
			allowed: false,
		},
		{
			name:    "instance-scoped group binding applies everywhere",
			group:   []*store.RoleBinding{viaGroup("g1", "instance", "viewer", nil, nil)},
			action:  SecretRead,
			res:     Resource{ProjectID: "ANY"},
			allowed: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := New(&fakeBindings{byUser: map[string][]*store.RoleBinding{uid: tc.direct}}).
				WithGroups(&fakeGroupBindings{byUser: map[string][]*store.RoleBinding{uid: tc.group}})
			err := e.Can(context.Background(), auth.Principal{Kind: auth.KindUser, ID: uid}, nil, tc.action, tc.res)
			if got := err == nil; got != tc.allowed {
				t.Fatalf("Can(%s) allowed=%v, want %v (err=%v)", tc.action, got, tc.allowed, err)
			}
		})
	}
}

// An engine with no group store must behave exactly as it did before groups
// existed — the source is optional, and nil must never be dereferenced.
func TestNoGroupStoreIsUnchanged(t *testing.T) {
	uid := "u1"
	e := New(&fakeBindings{byUser: map[string][]*store.RoleBinding{uid: {
		{SubjectUserID: uid, ScopeLevel: "project", ProjectID: ptr("P"), Role: "viewer"},
	}}})
	if err := e.Can(context.Background(), auth.Principal{Kind: auth.KindUser, ID: uid}, nil, SecretRead, Resource{ProjectID: "P"}); err != nil {
		t.Fatalf("direct binding should still allow: %v", err)
	}
	if err := e.Can(context.Background(), auth.Principal{Kind: auth.KindUser, ID: uid}, nil, SecretWrite, Resource{ProjectID: "P"}); err == nil {
		t.Fatal("viewer must not write")
	}
}

// A failing group store must DENY, not silently fall back to direct bindings —
// otherwise a transient database fault becomes a quiet permission change.
func TestGroupStoreErrorFailsClosed(t *testing.T) {
	uid := "u1"
	e := New(&fakeBindings{byUser: map[string][]*store.RoleBinding{uid: {
		{SubjectUserID: uid, ScopeLevel: "project", ProjectID: ptr("P"), Role: "admin"},
	}}}).WithGroups(&fakeGroupBindings{err: errBoom})

	err := e.Can(context.Background(), auth.Principal{Kind: auth.KindUser, ID: uid}, nil, SecretRead, Resource{ProjectID: "P"})
	if err == nil {
		t.Fatal("a group-store error must not resolve to allow")
	}
	if _, rErr := e.BoundRole(context.Background(), uid, Resource{ProjectID: "P"}); rErr == nil {
		t.Fatal("BoundRole must propagate a group-store error")
	}
}

// The delegation cap reads BoundRole, so a group-derived role must count toward
// it: a group binding is durable, the same kind of thing as a direct binding.
func TestBoundRoleIncludesGroupBindings(t *testing.T) {
	uid := "u1"
	e := New(&fakeBindings{byUser: map[string][]*store.RoleBinding{uid: {
		{SubjectUserID: uid, ScopeLevel: "project", ProjectID: ptr("P"), Role: "viewer"},
	}}}).WithGroups(&fakeGroupBindings{byUser: map[string][]*store.RoleBinding{uid: {
		viaGroup("g1", "project", "admin", ptr("P"), nil),
	}}})

	got, err := e.BoundRole(context.Background(), uid, Resource{ProjectID: "P"})
	if err != nil {
		t.Fatalf("BoundRole: %v", err)
	}
	if got != RoleAdmin {
		t.Fatalf("BoundRole = %q, want admin (highest of direct viewer + group admin)", got)
	}
}

// M-1 must survive groups: break-glass still arrives through GrantStore, so an
// elevation raises EffectiveRole but NOT BoundRole, even alongside groups.
func TestBreakGlassStillExcludedFromBoundRoleWithGroups(t *testing.T) {
	uid := "u1"
	now := time.Now()
	e := New(&fakeBindings{byUser: map[string][]*store.RoleBinding{uid: nil}}).
		WithGroups(&fakeGroupBindings{byUser: map[string][]*store.RoleBinding{uid: {
			viaGroup("g1", "project", "developer", ptr("P"), nil),
		}}}).
		WithGrants(&fakeGrants{byUser: map[string][]*store.BreakGlassGrant{uid: {{
			UserID: uid, ScopeLevel: "project", ProjectID: ptr("P"),
			ElevatedRole: "admin", ExpiresAt: now.Add(time.Hour),
		}}}})
	e.SetClock(func() time.Time { return now })

	bound, err := e.BoundRole(context.Background(), uid, Resource{ProjectID: "P"})
	if err != nil {
		t.Fatalf("BoundRole: %v", err)
	}
	if bound != RoleDeveloper {
		t.Fatalf("BoundRole = %q, want developer — a break-glass grant must not raise the DURABLE role", bound)
	}
	eff, err := e.EffectiveRole(context.Background(), uid, Resource{ProjectID: "P"})
	if err != nil {
		t.Fatalf("EffectiveRole: %v", err)
	}
	if eff != RoleAdmin {
		t.Fatalf("EffectiveRole = %q, want admin (grant overlays the group role)", eff)
	}
}

// Service tokens have no user identity, so groups must never widen their scope.
func TestGroupsDoNotApplyToServiceTokens(t *testing.T) {
	gs := &fakeGroupBindings{byUser: map[string][]*store.RoleBinding{
		"t1": {viaGroup("g1", "instance", "admin", nil, nil)},
	}}
	e := New(&fakeBindings{}).WithGroups(gs)
	p := auth.Principal{Kind: auth.KindServiceToken, ID: "t1"}
	scope := &TokenScope{Kind: "config", ID: "C", Access: "read"}

	if err := e.Can(context.Background(), p, scope, SecretWrite, Resource{ConfigID: "C"}); err == nil {
		t.Fatal("a read token must not gain write from a same-id group binding")
	}
	if gs.calls != 0 {
		t.Fatalf("group store consulted %d times for a token principal; want 0", gs.calls)
	}
}
