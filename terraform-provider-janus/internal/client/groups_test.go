package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func groupClient(t *testing.T, h http.Handler) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, "janus_svc_test", &http.Client{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func encode(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// The scope triple maps onto exactly the three group-member route families.
func TestBindingScopePath(t *testing.T) {
	for _, tc := range []struct {
		name      string
		scope     BindingScope
		wantLevel string
		wantPath  string
		wantErr   bool
	}{
		{"instance", BindingScope{}, ScopeLevelInstance, "/v1/instance/group-members", false},
		{"project", BindingScope{ProjectID: "p1"}, ScopeLevelProject, "/v1/projects/p1/group-members", false},
		{
			"environment", BindingScope{ProjectID: "p1", EnvironmentID: "e1"},
			ScopeLevelEnvironment, "/v1/projects/p1/environments/e1/group-members", false,
		},
		// The environment route is NESTED under its project, so an environment
		// with no project has no URL to call.
		{"environment without project", BindingScope{EnvironmentID: "e1"}, ScopeLevelEnvironment, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.scope.Level(); got != tc.wantLevel {
				t.Errorf("Level() = %q, want %q", got, tc.wantLevel)
			}
			got, err := tc.scope.path()
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr && got != tc.wantPath {
				t.Errorf("path = %q, want %q", got, tc.wantPath)
			}
		})
	}
}

// The two-kinds rule and the never-owner rule are enforced in the client too, so
// nothing that the plan would reject can reach the API by another route.
func TestGroupInputAndRoleValidation(t *testing.T) {
	for _, tc := range []struct {
		name       string
		kind       string
		claim      string
		wantErrSub string
	}{
		{"local", GroupKindLocal, "", ""},
		{"oidc with claim", GroupKindOIDC, "grp-1", ""},
		{"local with claim", GroupKindLocal, "grp-1", "cannot have a claim_value"},
		{"oidc without claim", GroupKindOIDC, "", "requires a claim_value"},
		{"unknown kind", "ldap", "", "must be"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateGroupInput(tc.kind, tc.claim)
			if tc.wantErrSub == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErrSub) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.wantErrSub)
			}
		})
	}

	for _, role := range []string{GroupRoleViewer, GroupRoleDeveloper, GroupRoleAdmin} {
		if err := ValidateGroupRole(role); err != nil {
			t.Errorf("role %q rejected: %v", role, err)
		}
	}
	if err := ValidateGroupRole(GroupRoleOwner); err == nil || !strings.Contains(err.Error(), "never be") {
		t.Errorf("owner must be refused with an explanation, got %v", err)
	}
	if err := ValidateGroupRole("root"); err == nil {
		t.Error("an unknown role must be refused")
	}
}

// Neither pre-flight may reach the network: an invalid request is refused
// locally so a practitioner never burns a round-trip on a 400.
func TestGroupWritesRefusedBeforeAnyRequest(t *testing.T) {
	calls := 0
	c := groupClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusNoContent)
	}))
	if _, err := c.CreateGroup(context.Background(), GroupInput{Name: "t", Kind: GroupKindOIDC}); err == nil {
		t.Error("oidc group with no claim must be refused")
	}
	if err := c.PutGroupBinding(context.Background(), BindingScope{}, "g1", GroupRoleOwner); err == nil {
		t.Error("an owner group binding must be refused")
	}
	if calls != 0 {
		t.Errorf("%d requests issued; both must fail locally", calls)
	}
}

// Membership and binding lookups walk every cursor page before answering "no",
// so a member on page 2 is not reported as drift.
func TestGroupLookupsFollowCursorPages(t *testing.T) {
	c := groupClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cursor := r.URL.Query().Get("cursor")
		switch {
		case strings.HasSuffix(r.URL.Path, "/members"):
			if cursor == "" {
				next := "page2"
				encode(w, 200, map[string]any{
					"members":     []map[string]string{{"user_id": "someone-else"}},
					"next_cursor": &next,
				})
				return
			}
			encode(w, 200, map[string]any{
				"members":     []map[string]string{{"user_id": "u-target"}},
				"next_cursor": nil,
			})
		case strings.HasSuffix(r.URL.Path, "/group-members"):
			if cursor == "" {
				next := "page2"
				encode(w, 200, map[string]any{
					"bindings":    []GroupBinding{{GroupID: "other", Role: "viewer"}},
					"next_cursor": &next,
				})
				return
			}
			encode(w, 200, map[string]any{
				"bindings":    []GroupBinding{{GroupID: "g-target", Role: "admin"}},
				"next_cursor": nil,
			})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))

	ok, err := c.HasGroupMember(context.Background(), "g1", "u-target")
	if err != nil || !ok {
		t.Fatalf("HasGroupMember = %v, %v; want true on page 2", ok, err)
	}
	missing, err := c.HasGroupMember(context.Background(), "g1", "u-nobody")
	if err != nil || missing {
		t.Fatalf("HasGroupMember(unknown) = %v, %v; want false", missing, err)
	}

	b, err := c.GetGroupBinding(context.Background(), BindingScope{ProjectID: "p1"}, "g-target")
	if err != nil || b.Role != "admin" {
		t.Fatalf("GetGroupBinding = %v, %v; want the page-2 binding", b, err)
	}
	// A group bound nowhere at this scope reads as 404 so Read can drift it out.
	if _, err := c.GetGroupBinding(context.Background(), BindingScope{ProjectID: "p1"}, "g-unbound"); !IsNotFound(err) {
		t.Fatalf("unbound group should surface as 404, got %v", err)
	}
}

// GetGroup unwraps the {"group":…,"bindings":[…]} envelope.
func TestGetGroupUnwrapsEnvelope(t *testing.T) {
	claim := "8f14e45f"
	c := groupClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		encode(w, 200, map[string]any{
			"group":    Group{ID: "g1", Name: "Team Payments", Kind: GroupKindOIDC, ClaimValue: &claim},
			"bindings": []GroupBinding{{GroupID: "g1", Role: "developer"}},
		})
	}))
	g, err := c.GetGroup(context.Background(), "g1")
	if err != nil {
		t.Fatal(err)
	}
	if g.Kind != GroupKindOIDC || g.ClaimValue == nil || *g.ClaimValue != claim {
		t.Fatalf("group = %+v", g)
	}
}
