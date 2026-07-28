package authz

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/steveokay/janus-secrets/internal/auth"
	"github.com/steveokay/janus-secrets/internal/store"
)

func has(set []Action, a Action) bool { return slices.Contains(set, a) }

// AllActions is derived from the owner bundle. If a new action is ever added to
// a role, it must show up here — and if one is added to NO role, it is
// ungrantable and correctly absent.
func TestAllActionsCoversEveryRoleBundle(t *testing.T) {
	all := AllActions()
	for _, bundle := range roleActions {
		for a := range bundle {
			if !has(all, a) {
				t.Fatalf("AllActions() is missing %q, which some role grants", a)
			}
		}
	}
	if !slices.IsSorted(all) {
		t.Fatal("AllActions() must be sorted so responses are stable")
	}
}

// The instance/anywhere split is the point of the whole type: a project-scoped
// binding must NOT report instance-scoped reach, or the UI shows a screen the
// server will refuse.
func TestEffectiveSeparatesInstanceFromProjectScope(t *testing.T) {
	ctx := context.Background()
	uid := "u1"
	e := New(&fakeBindings{byUser: map[string][]*store.RoleBinding{uid: {
		{SubjectUserID: uid, ScopeLevel: "project", ProjectID: ptr("P"), Role: "admin"},
	}}})
	user := auth.Principal{Kind: auth.KindUser, ID: uid}

	eff, err := e.Effective(ctx, user, nil)
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	if len(eff.Instance) != 0 {
		t.Fatalf("a project binding must confer NO instance reach, got %v", eff.Instance)
	}
	for _, a := range []Action{SecretRead, MemberManage, SyncManage} {
		if !has(eff.Anywhere, a) {
			t.Fatalf("project admin should hold %q somewhere, got %v", a, eff.Anywhere)
		}
	}
	// The trap this guards: a project admin's bundle contains group:manage, but
	// the group catalog is instance-scoped, so Can denies it at Instance().
	if has(eff.Instance, GroupManage) {
		t.Fatal("group:manage is instance-scoped and must not leak from a project binding")
	}
	if err := e.Can(ctx, user, nil, GroupManage, Instance()); err == nil {
		t.Fatal("precondition failed: Can should deny group:manage at instance scope here")
	}
}

func TestEffectiveInstanceOwnerHoldsEverything(t *testing.T) {
	ctx := context.Background()
	uid := "u1"
	e := New(&fakeBindings{byUser: map[string][]*store.RoleBinding{uid: {
		{SubjectUserID: uid, ScopeLevel: "instance", Role: "owner"},
	}}})

	eff, err := e.Effective(ctx, auth.Principal{Kind: auth.KindUser, ID: uid}, nil)
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	if len(eff.Instance) != len(AllActions()) {
		t.Fatalf("instance owner should hold every action, got %d of %d", len(eff.Instance), len(AllActions()))
	}
	if len(eff.Anywhere) != len(eff.Instance) {
		t.Fatal("an instance binding applies to every resource, so anywhere == instance")
	}
}

func TestEffectiveEmptyForUnboundUser(t *testing.T) {
	ctx := context.Background()
	e := New(&fakeBindings{byUser: map[string][]*store.RoleBinding{}})

	eff, err := e.Effective(ctx, auth.Principal{Kind: auth.KindUser, ID: "nobody"}, nil)
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	if len(eff.Instance) != 0 || len(eff.Anywhere) != 0 {
		t.Fatalf("an unbound user holds nothing, got %+v", eff)
	}
	// Non-nil so the JSON encodes as [] rather than null: "no permissions" and
	// "permissions unknown" must not look the same to a client.
	if eff.Instance == nil || eff.Anywhere == nil {
		t.Fatal("empty sets must be non-nil slices")
	}
}

// Group-derived bindings are ordinary bindings to the engine, so they must
// contribute reach exactly as a direct binding does.
func TestEffectiveIncludesGroupDerivedBindings(t *testing.T) {
	ctx := context.Background()
	uid := "u1"
	e := New(&fakeBindings{byUser: map[string][]*store.RoleBinding{}}).
		WithGroups(&fakeGroupBindings{byUser: map[string][]*store.RoleBinding{uid: {
			viaGroup("g1", "project", "developer", ptr("P"), nil),
		}}})

	eff, err := e.Effective(ctx, auth.Principal{Kind: auth.KindUser, ID: uid}, nil)
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	if !has(eff.Anywhere, SecretWrite) {
		t.Fatalf("a group-derived developer binding should confer secret:write somewhere, got %v", eff.Anywhere)
	}
}

// An active break-glass grant is part of what the principal can do right now.
// Omitting it would hide the very screens someone elevated in order to reach.
func TestEffectiveIncludesActiveBreakGlassGrant(t *testing.T) {
	ctx := context.Background()
	uid := "u1"
	future := time.Now().Add(time.Hour)
	e := New(&fakeBindings{byUser: map[string][]*store.RoleBinding{uid: {
		{SubjectUserID: uid, ScopeLevel: "project", ProjectID: ptr("P"), Role: "viewer"},
	}}}).WithGrants(&fakeGrants{byUser: map[string][]*store.BreakGlassGrant{uid: {
		grant(uid, "project", ptr("P"), nil, "admin", future),
	}}})

	eff, err := e.Effective(ctx, auth.Principal{Kind: auth.KindUser, ID: uid}, nil)
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	if !has(eff.Anywhere, MemberManage) {
		t.Fatalf("an active grant elevating to admin should surface member:manage, got %v", eff.Anywhere)
	}
}

func TestEffectiveExpiredGrantConfersNothing(t *testing.T) {
	ctx := context.Background()
	uid := "u1"
	past := time.Now().Add(-time.Hour)
	e := New(&fakeBindings{byUser: map[string][]*store.RoleBinding{uid: {
		{SubjectUserID: uid, ScopeLevel: "project", ProjectID: ptr("P"), Role: "viewer"},
	}}}).WithGrants(&fakeGrants{byUser: map[string][]*store.BreakGlassGrant{uid: {
		grant(uid, "project", ptr("P"), nil, "owner", past),
	}}})

	eff, err := e.Effective(ctx, auth.Principal{Kind: auth.KindUser, ID: uid}, nil)
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	if has(eff.Anywhere, MemberManage) {
		t.Fatal("an EXPIRED grant must confer nothing")
	}
}

// A store failure must propagate, not resolve to a partial answer: reporting
// "you have no permissions" and reporting "the database is down" are different
// facts, and the handler decides what to do about it.
func TestEffectivePropagatesStoreError(t *testing.T) {
	e := New(errBindings{})
	if _, err := e.Effective(context.Background(), auth.Principal{Kind: auth.KindUser, ID: "u1"}, nil); err == nil {
		t.Fatal("a binding-store error must propagate")
	}
}

func TestEffectiveForServiceTokens(t *testing.T) {
	ctx := context.Background()
	e := New(&fakeBindings{})
	tok := auth.Principal{Kind: auth.KindServiceToken, ID: "t1"}

	t.Run("config token reaches only its own config", func(t *testing.T) {
		eff, err := e.Effective(ctx, tok, &TokenScope{Kind: "config", ID: "c1", Access: "readwrite"})
		if err != nil {
			t.Fatalf("Effective: %v", err)
		}
		if len(eff.Instance) != 0 {
			t.Fatalf("a config token has no instance reach, got %v", eff.Instance)
		}
		for _, a := range []Action{SecretRead, SecretWrite, ConfigRead} {
			if !has(eff.Anywhere, a) {
				t.Fatalf("a readwrite config token should hold %q, got %v", a, eff.Anywhere)
			}
		}
		if has(eff.Anywhere, MemberManage) {
			t.Fatal("a config token must not report management actions")
		}
	})

	t.Run("read-only token cannot write", func(t *testing.T) {
		eff, err := e.Effective(ctx, tok, &TokenScope{Kind: "config", ID: "c1", Access: "read"})
		if err != nil {
			t.Fatalf("Effective: %v", err)
		}
		if has(eff.Anywhere, SecretWrite) {
			t.Fatal("a read token must not report secret:write")
		}
	})

	t.Run("scopeless token holds nothing", func(t *testing.T) {
		eff, err := e.Effective(ctx, tok, nil)
		if err != nil {
			t.Fatalf("Effective: %v", err)
		}
		if len(eff.Anywhere) != 0 {
			t.Fatalf("a token with no scope holds nothing, got %v", eff.Anywhere)
		}
	})
}

// The contract that matters: Effective must agree with Can for every action it
// reports and every action it omits. A disagreement is either a nav item that
// 403s or a hidden feature that still works by URL.
func TestEffectiveAgreesWithCan(t *testing.T) {
	ctx := context.Background()
	uid := "u1"
	e := New(&fakeBindings{byUser: map[string][]*store.RoleBinding{uid: {
		{SubjectUserID: uid, ScopeLevel: "instance", Role: "viewer"},
		{SubjectUserID: uid, ScopeLevel: "project", ProjectID: ptr("P"), Role: "developer"},
		{SubjectUserID: uid, ScopeLevel: "environment", EnvironmentID: ptr("E"), Role: "admin"},
	}}})
	user := auth.Principal{Kind: auth.KindUser, ID: uid}

	eff, err := e.Effective(ctx, user, nil)
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	for _, a := range AllActions() {
		wantInstance := e.Can(ctx, user, nil, a, Instance()) == nil
		if got := has(eff.Instance, a); got != wantInstance {
			t.Errorf("instance %q: Effective=%v Can=%v", a, got, wantInstance)
		}
		// "Anywhere" must hold exactly when Can allows it on instance, the bound
		// project, or the bound environment.
		wantAnywhere := wantInstance ||
			e.Can(ctx, user, nil, a, Resource{ProjectID: "P"}) == nil ||
			e.Can(ctx, user, nil, a, Resource{EnvID: "E"}) == nil
		if got := has(eff.Anywhere, a); got != wantAnywhere {
			t.Errorf("anywhere %q: Effective=%v Can=%v", a, got, wantAnywhere)
		}
	}
}
