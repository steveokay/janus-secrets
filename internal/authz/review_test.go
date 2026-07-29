package authz

import (
	"context"
	"testing"
	"time"

	"github.com/steveokay/janus-secrets/internal/store"
)

func direct(level, role string, project, env *string) *store.RoleBinding {
	return &store.RoleBinding{ScopeLevel: level, ProjectID: project, EnvironmentID: env, Role: role}
}

// A cross-scope review has to say WHY a role holds, so it needs the
// contributing bindings — and it must agree with the decision path exactly. The
// table below is the union rule seen from the review side: instance reaches
// everything, project reaches its environments, environment reaches only
// itself, and nothing leaks sideways.
func TestApplicableBindingsMatchesTheDecisionRule(t *testing.T) {
	held := []*store.RoleBinding{
		direct("instance", "viewer", nil, nil),
		direct("project", "developer", ptr("P"), nil),
		direct("environment", "admin", nil, ptr("E")),
		direct("project", "owner", ptr("OTHER"), nil),
		viaGroup("g1", "project", "admin", ptr("P"), nil),
	}
	tests := []struct {
		name  string
		res   Resource
		count int
		role  Role
	}{
		{"instance sees only the instance binding", Instance(), 1, RoleViewer},
		{"project unions instance + direct + group", Resource{ProjectID: "P"}, 3, RoleAdmin},
		{"an environment inherits its project", Resource{ProjectID: "P", EnvID: "E"}, 4, RoleAdmin},
		{"an environment without its project chain loses the project binding",
			Resource{EnvID: "E"}, 2, RoleAdmin},
		{"an unrelated project sees only the instance binding",
			Resource{ProjectID: "NOPE"}, 1, RoleViewer},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ApplicableBindings(held, tc.res)
			if len(got) != tc.count {
				t.Fatalf("applicable = %d bindings, want %d", len(got), tc.count)
			}
			if r := RoleFromBindings(held, tc.res); r != tc.role {
				t.Fatalf("role = %q, want %q", r, tc.role)
			}
			// The two must never disagree: the max over the applicable subset is
			// the same answer as the max over everything.
			if r := RoleFromBindings(got, tc.res); r != tc.role {
				t.Fatalf("role over the applicable subset = %q, want %q", r, tc.role)
			}
			// And it must agree with the predicate the DECISION path uses. A
			// review whose scope-matching drifted from userAllows would still
			// look authoritative while reporting access the server does not
			// grant (or missing access it does).
			if allowed, want := userAllows(held, SecretWrite, tc.res), RoleAllows(tc.role, SecretWrite); allowed != want {
				t.Fatalf("userAllows=%v but role %q allows secret:write=%v", allowed, tc.role, want)
			}
		})
	}
}

// RoleFromBindings must fail closed on a subject with nothing.
func TestRoleFromBindingsEmpty(t *testing.T) {
	if r := RoleFromBindings(nil, Resource{ProjectID: "P"}); r != "" {
		t.Fatalf("role = %q, want empty", r)
	}
	if RoleAllows("", SecretRead) {
		t.Fatal("the empty role must confer nothing")
	}
}

// The whole reason BoundRoles exists: the obvious loop calls BoundRole per
// scope, and each call re-queries direct AND group bindings. A bulk revocation
// across fifty scopes would issue a hundred queries to check a delegation cap.
func TestBoundRolesResolvesBindingsOnce(t *testing.T) {
	groups := &fakeGroupBindings{byUser: map[string][]*store.RoleBinding{
		"u": {viaGroup("g1", "project", "admin", ptr("P2"), nil)},
	}}
	e := New(&fakeBindings{byUser: map[string][]*store.RoleBinding{
		"u": {direct("project", "developer", ptr("P1"), nil)},
	}}).WithGroups(groups)

	resources := []Resource{
		{ProjectID: "P1"}, {ProjectID: "P2"}, {ProjectID: "P3"}, Instance(),
	}
	got, err := e.BoundRoles(context.Background(), "u", resources)
	if err != nil {
		t.Fatal(err)
	}
	want := []Role{RoleDeveloper, RoleAdmin, "", ""}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("resource %d: role = %q, want %q", i, got[i], want[i])
		}
	}
	if groups.calls != 1 {
		t.Fatalf("group bindings resolved %d times, want exactly 1", groups.calls)
	}
}

// The M-1 invariant, restated for the bulk path: break-glass grants arrive
// through GrantStore and never through a binding source, so BoundRoles cannot
// see them — an elevated caller's cap stays their durable role.
func TestBoundRolesIgnoresBreakGlass(t *testing.T) {
	e := New(&fakeBindings{byUser: map[string][]*store.RoleBinding{
		"u": {direct("project", "viewer", ptr("P"), nil)},
	}}).WithGrants(&fakeGrants{byUser: map[string][]*store.BreakGlassGrant{
		"u": {grant("u", "project", ptr("P"), nil, "owner", time.Now().Add(time.Hour))},
	}})
	got, err := e.BoundRoles(context.Background(), "u", []Resource{{ProjectID: "P"}})
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != RoleViewer {
		t.Fatalf("bound role = %q, want viewer — a break-glass grant must not raise the cap", got[0])
	}
}

// Fails CLOSED: a group-store fault denies rather than quietly answering from
// direct bindings alone, which would be a silent permission change.
func TestBoundRolesFailsClosedOnGroupError(t *testing.T) {
	e := New(&fakeBindings{byUser: map[string][]*store.RoleBinding{
		"u": {direct("instance", "owner", nil, nil)},
	}}).WithGroups(&fakeGroupBindings{err: errBoom})
	if _, err := e.BoundRoles(context.Background(), "u", []Resource{Instance()}); err == nil {
		t.Fatal("a group-store error must propagate")
	}
}
