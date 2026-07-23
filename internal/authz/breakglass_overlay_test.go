package authz

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/steveokay/janus-secrets/internal/auth"
	"github.com/steveokay/janus-secrets/internal/store"
)

type fakeGrants struct {
	byUser map[string][]*store.BreakGlassGrant
	err    error
}

func (f *fakeGrants) ListActiveForUser(_ context.Context, uid string, now time.Time) ([]*store.BreakGlassGrant, error) {
	if f.err != nil {
		return nil, f.err
	}
	// Emulate the store's liveness filter (revoked/expired excluded).
	var out []*store.BreakGlassGrant
	for _, g := range f.byUser[uid] {
		if g.Active(now) {
			out = append(out, g)
		}
	}
	return out, nil
}

func grant(user, level string, project, env *string, role string, expires time.Time) *store.BreakGlassGrant {
	return &store.BreakGlassGrant{
		UserID: user, ScopeLevel: level, ProjectID: project, EnvironmentID: env,
		ElevatedRole: role, ExpiresAt: expires,
	}
}

func TestBreakGlassGrantElevatesWithinScope(t *testing.T) {
	ctx := context.Background()
	uid := "u1"
	future := time.Now().Add(time.Hour)

	// Bound as viewer on project P; grant raises to admin on P.
	e := New(&fakeBindings{byUser: map[string][]*store.RoleBinding{uid: {
		{SubjectUserID: uid, ScopeLevel: "project", ProjectID: ptr("P"), Role: "viewer"},
	}}}).WithGrants(&fakeGrants{byUser: map[string][]*store.BreakGlassGrant{uid: {
		grant(uid, "project", ptr("P"), nil, "admin", future),
	}}})
	user := auth.Principal{Kind: auth.KindUser, ID: uid}

	// Without the grant a viewer cannot manage members; with it, admin can.
	if err := e.Can(ctx, user, nil, MemberManage, Resource{ProjectID: "P"}); err != nil {
		t.Fatalf("grant should elevate viewer→admin on P: %v", err)
	}
	// Secret write (developer+) is likewise unlocked.
	if err := e.Can(ctx, user, nil, SecretWrite, Resource{ProjectID: "P", EnvID: "E"}); err != nil {
		t.Fatalf("grant admin should permit secret write on P: %v", err)
	}
}

func TestBreakGlassGrantScopedOnlyToItsScope(t *testing.T) {
	ctx := context.Background()
	uid := "u2"
	future := time.Now().Add(time.Hour)

	// Grant is on env E only; must not apply to a sibling env or another project.
	e := New(&fakeBindings{byUser: map[string][]*store.RoleBinding{uid: {
		{SubjectUserID: uid, ScopeLevel: "environment", EnvironmentID: ptr("E"), Role: "viewer"},
	}}}).WithGrants(&fakeGrants{byUser: map[string][]*store.BreakGlassGrant{uid: {
		grant(uid, "environment", nil, ptr("E"), "owner", future),
	}}})
	user := auth.Principal{Kind: auth.KindUser, ID: uid}

	if err := e.Can(ctx, user, nil, MemberManage, Resource{ProjectID: "P", EnvID: "E"}); err != nil {
		t.Fatalf("grant should apply on its own env E: %v", err)
	}
	if err := e.Can(ctx, user, nil, MemberManage, Resource{ProjectID: "P", EnvID: "E2"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("grant must NOT apply to sibling env E2: %v", err)
	}
	if err := e.Can(ctx, user, nil, SecretRead, Resource{ProjectID: "Q"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("grant must NOT leak to another project: %v", err)
	}
}

func TestBreakGlassExpiredGrantDoesNotApply(t *testing.T) {
	ctx := context.Background()
	uid := "u3"
	past := time.Now().Add(-time.Minute)

	e := New(&fakeBindings{byUser: map[string][]*store.RoleBinding{uid: {
		{SubjectUserID: uid, ScopeLevel: "project", ProjectID: ptr("P"), Role: "viewer"},
	}}}).WithGrants(&fakeGrants{byUser: map[string][]*store.BreakGlassGrant{uid: {
		grant(uid, "project", ptr("P"), nil, "owner", past), // expired
	}}})
	user := auth.Principal{Kind: auth.KindUser, ID: uid}

	if err := e.Can(ctx, user, nil, MemberManage, Resource{ProjectID: "P"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expired grant must not elevate: %v", err)
	}
}

func TestBreakGlassRevokedGrantDoesNotApply(t *testing.T) {
	ctx := context.Background()
	uid := "u4"
	future := time.Now().Add(time.Hour)
	revoked := time.Now().Add(-time.Second)

	g := grant(uid, "project", ptr("P"), nil, "owner", future)
	g.RevokedAt = &revoked

	e := New(&fakeBindings{byUser: map[string][]*store.RoleBinding{uid: {
		{SubjectUserID: uid, ScopeLevel: "project", ProjectID: ptr("P"), Role: "viewer"},
	}}}).WithGrants(&fakeGrants{byUser: map[string][]*store.BreakGlassGrant{uid: {g}}})
	user := auth.Principal{Kind: auth.KindUser, ID: uid}

	if err := e.Can(ctx, user, nil, MemberManage, Resource{ProjectID: "P"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked grant must not elevate: %v", err)
	}
}

func TestBreakGlassDenyByDefaultNoBaseBindingIrrelevantToOverlay(t *testing.T) {
	// The overlay itself never checks for a base binding — that guard lives in the
	// activation handler. But a grant a user somehow holds still only elevates on
	// its scope. With NO grant store, behaviour is unchanged (deny by default).
	ctx := context.Background()
	uid := "u5"
	e := New(&fakeBindings{byUser: map[string][]*store.RoleBinding{uid: {}}})
	user := auth.Principal{Kind: auth.KindUser, ID: uid}
	if err := e.Can(ctx, user, nil, SecretRead, Resource{ProjectID: "P"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("no binding + no grant store must be forbidden: %v", err)
	}
}

func TestBreakGlassOverlayPropagatesStoreError(t *testing.T) {
	ctx := context.Background()
	uid := "u6"
	boom := errors.New("grant store boom")
	e := New(&fakeBindings{byUser: map[string][]*store.RoleBinding{uid: {}}}).
		WithGrants(&fakeGrants{err: boom})
	user := auth.Principal{Kind: auth.KindUser, ID: uid}
	if err := e.Can(ctx, user, nil, SecretRead, Resource{ProjectID: "P"}); !errors.Is(err, boom) {
		t.Fatalf("overlay must propagate store error, got %v", err)
	}
}

func TestBreakGlassEffectiveRoleMaxOfBoundAndGrant(t *testing.T) {
	ctx := context.Background()
	uid := "u7"
	future := time.Now().Add(time.Hour)
	e := New(&fakeBindings{byUser: map[string][]*store.RoleBinding{uid: {
		{SubjectUserID: uid, ScopeLevel: "project", ProjectID: ptr("P"), Role: "developer"},
	}}}).WithGrants(&fakeGrants{byUser: map[string][]*store.BreakGlassGrant{uid: {
		grant(uid, "project", ptr("P"), nil, "owner", future),
	}}})

	got, err := e.EffectiveRole(ctx, uid, Resource{ProjectID: "P"})
	if err != nil || got != RoleOwner {
		t.Fatalf("effective role = %q (err %v), want owner", got, err)
	}
	// BoundRole ignores the grant.
	bound, err := e.BoundRole(ctx, uid, Resource{ProjectID: "P"})
	if err != nil || bound != RoleDeveloper {
		t.Fatalf("bound role = %q (err %v), want developer", bound, err)
	}
}
