package authz

import (
	"context"
	"errors"
	"testing"

	"github.com/steveokay/janus-secrets/internal/auth"
	"github.com/steveokay/janus-secrets/internal/store"
)

type fakeBindings struct {
	byUser  map[string][]*store.RoleBinding
	byScope []*store.RoleBinding
	owners  int

	created *store.RoleBindingInput
	deleted bool
}

func (f *fakeBindings) ListForUser(_ context.Context, uid string) ([]*store.RoleBinding, error) {
	return f.byUser[uid], nil
}
func (f *fakeBindings) ListForScope(context.Context, string, string) ([]*store.RoleBinding, error) {
	return f.byScope, nil
}
func (f *fakeBindings) ListForScopePage(context.Context, string, string, int, *store.Cursor) ([]*store.RoleBinding, error) {
	return f.byScope, nil
}
func (f *fakeBindings) Create(_ context.Context, in store.RoleBindingInput) (*store.RoleBinding, error) {
	f.created = &in
	return &store.RoleBinding{SubjectUserID: in.SubjectUserID, Role: in.Role}, nil
}
func (f *fakeBindings) DeleteForSubjectScope(context.Context, string, string, *string, *string) error {
	f.deleted = true
	return nil
}
func (f *fakeBindings) CountInstanceOwners(context.Context) (int, error) { return f.owners, nil }

func ptr(s string) *string { return &s }

var errBoom = errors.New("boom")

type errBindings struct{}

func (errBindings) ListForUser(context.Context, string) ([]*store.RoleBinding, error) {
	return nil, errBoom
}
func (errBindings) ListForScope(context.Context, string, string) ([]*store.RoleBinding, error) {
	return nil, errBoom
}
func (errBindings) ListForScopePage(context.Context, string, string, int, *store.Cursor) ([]*store.RoleBinding, error) {
	return nil, errBoom
}
func (errBindings) Create(context.Context, store.RoleBindingInput) (*store.RoleBinding, error) {
	return nil, errBoom
}
func (errBindings) DeleteForSubjectScope(context.Context, string, string, *string, *string) error {
	return errBoom
}
func (errBindings) CountInstanceOwners(context.Context) (int, error) { return 0, errBoom }

func TestCanUserInheritanceAndUnion(t *testing.T) {
	uid := "u1"
	e := New(&fakeBindings{byUser: map[string][]*store.RoleBinding{uid: {
		{SubjectUserID: uid, ScopeLevel: "project", ProjectID: ptr("P"), Role: "viewer"},
		{SubjectUserID: uid, ScopeLevel: "environment", EnvironmentID: ptr("E"), Role: "admin"},
	}}})
	ctx := context.Background()
	user := auth.Principal{Kind: auth.KindUser, ID: uid}

	if err := e.Can(ctx, user, nil, SecretWrite, Resource{ProjectID: "P", EnvID: "E"}); err != nil {
		t.Fatalf("write in E (admin): %v", err)
	}
	if err := e.Can(ctx, user, nil, SecretRead, Resource{ProjectID: "P", EnvID: "E2"}); err != nil {
		t.Fatalf("read in sibling E2 (viewer): %v", err)
	}
	if err := e.Can(ctx, user, nil, SecretWrite, Resource{ProjectID: "P", EnvID: "E2"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("write in E2 must be forbidden: %v", err)
	}
	if err := e.Can(ctx, user, nil, SecretRead, Resource{ProjectID: "Q"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("read in other project must be forbidden: %v", err)
	}
}

func TestCanInstanceActions(t *testing.T) {
	ctx := context.Background()
	owner := New(&fakeBindings{byUser: map[string][]*store.RoleBinding{
		"o": {{SubjectUserID: "o", ScopeLevel: "instance", Role: "owner"}}}})
	if err := owner.Can(ctx, auth.Principal{Kind: auth.KindUser, ID: "o"}, nil, SysSeal, Instance()); err != nil {
		t.Fatalf("instance owner seal: %v", err)
	}
	proj := New(&fakeBindings{byUser: map[string][]*store.RoleBinding{
		"p": {{SubjectUserID: "p", ScopeLevel: "project", ProjectID: ptr("P"), Role: "admin"}}}})
	if err := proj.Can(ctx, auth.Principal{Kind: auth.KindUser, ID: "p"}, nil, SysSeal, Instance()); !errors.Is(err, ErrForbidden) {
		t.Fatalf("project admin seal must be forbidden: %v", err)
	}
}

func TestCanServiceToken(t *testing.T) {
	e := New(&fakeBindings{})
	ctx := context.Background()
	tok := auth.Principal{Kind: auth.KindServiceToken, ID: "t1", Name: "ci"}
	read := &TokenScope{Kind: "config", ID: "C", Access: "read"}

	if err := e.Can(ctx, tok, read, SecretRead, Resource{ProjectID: "P", EnvID: "E", ConfigID: "C"}); err != nil {
		t.Fatalf("token read in-scope: %v", err)
	}
	if err := e.Can(ctx, tok, read, SecretWrite, Resource{ConfigID: "C"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("read token must not write: %v", err)
	}
	if err := e.Can(ctx, tok, read, SecretRead, Resource{ConfigID: "OTHER"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("out-of-scope must be forbidden: %v", err)
	}
	if err := e.Can(ctx, tok, &TokenScope{Kind: "environment", ID: "E", Access: "readwrite"}, MemberManage, Resource{EnvID: "E"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("token management must be forbidden: %v", err)
	}
	if err := e.Can(ctx, tok, &TokenScope{Kind: "environment", ID: "E", Access: "readwrite"}, SecretWrite, Resource{EnvID: "E", ConfigID: "C"}); err != nil {
		t.Fatalf("env token write in-scope: %v", err)
	}
	if err := e.Can(ctx, tok, nil, SecretRead, Resource{ConfigID: "C"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("nil-scope token must be forbidden: %v", err)
	}
}

func TestEffectiveRole(t *testing.T) {
	uid := "u"
	e := New(&fakeBindings{byUser: map[string][]*store.RoleBinding{uid: {
		{ScopeLevel: "project", ProjectID: ptr("P"), Role: "developer"},
		{ScopeLevel: "instance", Role: "admin"},
	}}})
	got, err := e.EffectiveRole(context.Background(), uid, Resource{ProjectID: "P"})
	if err != nil || got != RoleAdmin {
		t.Fatalf("effective = %q (err %v)", got, err)
	}
}

// --- Part B: coverage completion ---

func TestCanTokenDefaultBranches(t *testing.T) {
	e := New(&fakeBindings{})
	ctx := context.Background()
	tok := auth.Principal{Kind: auth.KindServiceToken, ID: "t"}

	// Unknown scope Kind -> forbidden (tokenAllows default branch).
	if err := e.Can(ctx, tok, &TokenScope{Kind: "bogus", Access: "read"}, SecretRead, Resource{ConfigID: "C"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("unknown token kind must be forbidden: %v", err)
	}
	// Unknown access -> nil capabilities -> forbidden (tokenCapabilities default branch).
	if err := e.Can(ctx, tok, &TokenScope{Kind: "config", ID: "C", Access: "none"}, SecretRead, Resource{ConfigID: "C"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("unknown token access must be forbidden: %v", err)
	}
}

func TestCanUnknownPrincipalKind(t *testing.T) {
	e := New(&fakeBindings{})
	if err := e.Can(context.Background(), auth.Principal{Kind: "mystery", ID: "x"}, nil, SecretRead, Instance()); !errors.Is(err, ErrForbidden) {
		t.Fatalf("unknown principal kind must be forbidden: %v", err)
	}
}

func TestBindingAppliesUnknownScopeLevel(t *testing.T) {
	uid := "g"
	e := New(&fakeBindings{byUser: map[string][]*store.RoleBinding{uid: {
		{SubjectUserID: uid, ScopeLevel: "galaxy", Role: "owner"},
	}}})
	// Unknown ScopeLevel must never apply (bindingApplies default branch),
	// so even an owner role grants nothing.
	if err := e.Can(context.Background(), auth.Principal{Kind: auth.KindUser, ID: uid}, nil, SecretRead, Instance()); !errors.Is(err, ErrForbidden) {
		t.Fatalf("unknown scope level must be forbidden: %v", err)
	}
	// And EffectiveRole must return "" for it.
	got, err := e.EffectiveRole(context.Background(), uid, Instance())
	if err != nil || got != Role("") {
		t.Fatalf("effective role for unknown scope = %q (err %v)", got, err)
	}
}

func TestEngineManagement(t *testing.T) {
	ctx := context.Background()
	f := &fakeBindings{
		byScope: []*store.RoleBinding{{SubjectUserID: "u1", Role: "admin"}},
		owners:  3,
	}
	e := New(f)

	if err := e.Grant(ctx, store.RoleBindingInput{SubjectUserID: "u1", ScopeLevel: "project", ProjectID: ptr("P"), Role: "admin"}); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if f.created == nil || f.created.SubjectUserID != "u1" || f.created.Role != "admin" {
		t.Fatalf("grant did not forward input: %+v", f.created)
	}

	if err := e.Revoke(ctx, "u1", "project", ptr("P"), nil); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if !f.deleted {
		t.Fatalf("revoke did not call delete")
	}

	n, err := e.CountInstanceOwners(ctx)
	if err != nil || n != 3 {
		t.Fatalf("count owners = %d (err %v)", n, err)
	}

	members, err := e.ListMembers(ctx, "project", "P")
	if err != nil || len(members) != 1 || members[0].UserID != "u1" || members[0].Role != "admin" {
		t.Fatalf("members = %+v (err %v)", members, err)
	}
}

func TestEngineErrorPaths(t *testing.T) {
	ctx := context.Background()
	e := New(errBindings{})
	user := auth.Principal{Kind: auth.KindUser, ID: "u"}

	if err := e.Can(ctx, user, nil, SecretRead, Instance()); !errors.Is(err, errBoom) {
		t.Fatalf("Can must propagate store error, got %v", err)
	}
	if _, err := e.EffectiveRole(ctx, "u", Instance()); !errors.Is(err, errBoom) {
		t.Fatalf("EffectiveRole must propagate store error, got %v", err)
	}
	if _, err := e.ListMembers(ctx, "project", "P"); !errors.Is(err, errBoom) {
		t.Fatalf("ListMembers must propagate store error, got %v", err)
	}
	if err := e.Grant(ctx, store.RoleBindingInput{}); !errors.Is(err, errBoom) {
		t.Fatalf("Grant must propagate store error, got %v", err)
	}
	if err := e.Revoke(ctx, "u", "project", nil, nil); !errors.Is(err, errBoom) {
		t.Fatalf("Revoke must propagate store error, got %v", err)
	}
	if _, err := e.CountInstanceOwners(ctx); !errors.Is(err, errBoom) {
		t.Fatalf("CountInstanceOwners must propagate store error, got %v", err)
	}
}
