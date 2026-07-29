package janus

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// obviously-fake fixtures (not real identifiers)
const (
	testGroupID    = "grp-00000000-0000-0000-0000-000000000001"
	testMemberID   = "usr-00000000-0000-0000-0000-000000000002"
	testClaimValue = "8f14e45f-ceea-467a-9d0e-7f4b2a1c9c33"
)

func groupServer(t *testing.T, h http.Handler) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := NewClient(srv.URL, WithToken(testToken))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

// The two-kinds rule is checked locally, so an impossible group never costs a
// round trip.
func TestCreateGroupValidatesKindLocally(t *testing.T) {
	var calls atomic.Int32
	c := groupServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeJSON(w, 201, Group{ID: testGroupID})
	}))

	for _, tc := range []struct {
		name string
		in   GroupInput
		sub  string
	}{
		{"local with claim", GroupInput{Name: "t", Kind: GroupKindLocal, ClaimValue: "x"}, "cannot have a claim value"},
		{"oidc without claim", GroupInput{Name: "t", Kind: GroupKindOIDC}, "requires a claim value"},
		{"unknown kind", GroupInput{Name: "t", Kind: "ldap"}, "must be"},
		{"no name", GroupInput{Kind: GroupKindLocal}, "name is required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := c.CreateGroup(context.Background(), tc.in)
			if err == nil || !strings.Contains(err.Error(), tc.sub) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.sub)
			}
		})
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("%d requests issued; every invalid input must fail locally", n)
	}
}

func TestCreateAndGetGroup(t *testing.T) {
	var body map[string]any
	c := groupServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/groups":
			_ = json.NewDecoder(r.Body).Decode(&body)
			writeJSON(w, 201, map[string]any{
				"id": testGroupID, "name": "Team Payments", "kind": GroupKindOIDC,
				"claim_value": testClaimValue, "member_count": 2, "binding_count": 1,
				"created_at": "2026-07-29T10:00:00Z",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/groups/"+testGroupID:
			writeJSON(w, 200, map[string]any{
				"group": map[string]any{
					"id": testGroupID, "name": "Team Payments", "kind": GroupKindOIDC,
					"claim_value": testClaimValue, "member_count": 2,
				},
				"bindings": []map[string]any{{"group_id": testGroupID, "role": "developer"}},
			})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))

	g, err := c.CreateGroup(context.Background(), GroupInput{
		Name: "Team Payments", Kind: GroupKindOIDC, ClaimValue: testClaimValue,
	})
	if err != nil {
		t.Fatal(err)
	}
	if body["kind"] != GroupKindOIDC || body["claim_value"] != testClaimValue {
		t.Errorf("create body = %v", body)
	}
	if g.MembersSeen != 2 {
		t.Errorf("MembersSeen = %d, want 2", g.MembersSeen)
	}
	if g.CreatedAt.IsZero() {
		t.Error("CreatedAt not parsed")
	}

	got, err := c.GetGroup(context.Background(), testGroupID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ClaimValue != testClaimValue || got.Kind != GroupKindOIDC {
		t.Fatalf("group = %+v", got)
	}
}

// The catalog is paginated; the SDK must walk every page rather than return the
// first hundred and look complete.
func TestListGroupsAndMembersFollowCursor(t *testing.T) {
	c := groupServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cursor := r.URL.Query().Get("cursor")
		next := "page2"
		switch {
		case r.URL.Path == "/v1/groups":
			if cursor == "" {
				writeJSON(w, 200, map[string]any{
					"groups":      []Group{{ID: "g1", Kind: GroupKindLocal}},
					"next_cursor": &next,
				})
				return
			}
			writeJSON(w, 200, map[string]any{
				"groups": []Group{{ID: "g2", Kind: GroupKindOIDC}}, "next_cursor": nil,
			})
		case r.URL.Path == "/v1/groups/"+testGroupID+"/members":
			if cursor == "" {
				writeJSON(w, 200, map[string]any{
					"members":     []map[string]any{{"user_id": "u1", "created_at": "2026-07-01T00:00:00Z"}},
					"next_cursor": &next,
				})
				return
			}
			writeJSON(w, 200, map[string]any{
				"members": []map[string]any{{"user_id": "u2"}}, "next_cursor": nil,
			})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))

	gs, err := c.ListGroups(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(gs) != 2 || gs[0].ID != "g1" || gs[1].ID != "g2" {
		t.Fatalf("groups = %+v", gs)
	}

	ms, err := c.ListGroupMembers(context.Background(), testGroupID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 || ms[0].UserID != "u1" || ms[1].UserID != "u2" {
		t.Fatalf("members = %+v", ms)
	}
	if want := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC); !ms[0].AddedAt.Equal(want) {
		t.Errorf("AddedAt = %v, want %v", ms[0].AddedAt, want)
	}
}

func TestGroupMembershipWrites(t *testing.T) {
	memberPath := "/v1/groups/" + testGroupID + "/members/" + testMemberID
	var methods []string
	c := groupServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != memberPath && r.URL.Path != "/v1/groups/"+testGroupID+"/capabilities" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		methods = append(methods, r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))

	if err := c.AddGroupMember(context.Background(), testGroupID, testMemberID); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveGroupMember(context.Background(), testGroupID, testMemberID); err != nil {
		t.Fatal(err)
	}
	if err := c.SetGroupProjectCreation(context.Background(), testGroupID, true); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"PUT " + memberPath,
		"DELETE " + memberPath,
		"PUT /v1/groups/" + testGroupID + "/capabilities",
	}
	if len(methods) != len(want) {
		t.Fatalf("requests = %v, want %v", methods, want)
	}
	for i := range want {
		if methods[i] != want[i] {
			t.Errorf("request %d = %q, want %q", i, methods[i], want[i])
		}
	}
}

// Adding a member to an IdP-fed group is refused server-side with 409; the SDK
// surfaces it as a typed APIError rather than swallowing it.
func TestAddGroupMemberSurfacesOIDCConflict(t *testing.T) {
	c := groupServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w,
			`{"error":{"code":"validation","message":"membership of an oidc group comes from the identity provider"}}`)
	}))
	err := c.AddGroupMember(context.Background(), testGroupID, testMemberID)
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != http.StatusConflict {
		t.Fatalf("err = %v, want an APIError with status 409", err)
	}
	if !strings.Contains(ae.Message, "identity provider") {
		t.Errorf("message = %q", ae.Message)
	}
}

// The catalog needs instance group:manage; a plain read token gets 403, and that
// must map onto the SDK's ErrForbidden sentinel so callers can branch on it.
func TestGroupCatalogForbiddenMapsToSentinel(t *testing.T) {
	c := groupServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":{"code":"forbidden","message":"forbidden"}}`)
	}))
	if _, err := c.ListGroups(context.Background()); !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
}

// MyGroups is authenticated-only and answers a service token with an empty list
// rather than an error, so it is safe to call unconditionally.
func TestMyGroupsHandlesEmptyList(t *testing.T) {
	c := groupServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/auth/me/groups" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		writeJSON(w, 200, map[string]any{"groups": []Group{}})
	}))
	gs, err := c.MyGroups(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(gs) != 0 {
		t.Fatalf("groups = %+v, want empty", gs)
	}
}

// Empty identifiers are rejected locally rather than producing a request to a
// malformed path.
func TestGroupCallsRejectEmptyIdentifiers(t *testing.T) {
	c := groupServer(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
	}))
	ctx := context.Background()
	if _, err := c.GetGroup(ctx, ""); err == nil {
		t.Error("GetGroup(\"\") must fail")
	}
	if err := c.DeleteGroup(ctx, ""); err == nil {
		t.Error("DeleteGroup(\"\") must fail")
	}
	if _, err := c.ListGroupMembers(ctx, ""); err == nil {
		t.Error("ListGroupMembers(\"\") must fail")
	}
	if err := c.AddGroupMember(ctx, testGroupID, ""); err == nil {
		t.Error("AddGroupMember with no user must fail")
	}
	if err := c.RemoveGroupMember(ctx, "", testMemberID); err == nil {
		t.Error("RemoveGroupMember with no group must fail")
	}
	if err := c.SetGroupProjectCreation(ctx, "", true); err == nil {
		t.Error("SetGroupProjectCreation(\"\") must fail")
	}
}
