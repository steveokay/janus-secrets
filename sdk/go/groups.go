package janus

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// Group kinds. A group is IdP-fed (GroupKindOIDC) or admin-curated
// (GroupKindLocal) and never both — the split is enforced in the database
// schema, not just at the API, so a hand-added member of an IdP-fed group is
// unrepresentable. That is what makes "access granted through an IdP group is
// fully described by the IdP" a statement you can rely on during an access
// review.
const (
	GroupKindOIDC  = "oidc"
	GroupKindLocal = "local"
)

// Group is a subject a role binding can target instead of a user.
//
// Group operations are part of the instance-scoped group CATALOG and need
// `group:manage` (admin or owner). A config- or environment-scoped
// `janus_svc_...` read token — the usual credential for this SDK — will get a
// 403 (ErrForbidden) from all of them except MyGroups.
type Group struct {
	// ID is the group UUID.
	ID string `json:"id"`
	// Name is unique across BOTH kinds, so an IdP group and a local group can
	// never quietly become the same group.
	Name string `json:"name"`
	// Kind is GroupKindOIDC or GroupKindLocal.
	Kind string `json:"kind"`
	// ClaimValue is the exact value the identity provider emits for this group.
	// Set only for GroupKindOIDC groups; empty otherwise.
	ClaimValue string `json:"claim_value"`
	// Description is display material, never secret material.
	Description string `json:"description"`
	// CanCreateProjects is the narrow delegated project-creation capability —
	// deliberately a capability rather than a role, since any role carrying
	// project:create at instance scope would also carry project:read there.
	CanCreateProjects bool `json:"can_create_projects"`

	// MembersSeen is how many users Janus has RECORDED in this group.
	//
	// It is deliberately not called MemberCount. For a GroupKindOIDC group,
	// membership is a snapshot refreshed at each sign-in, so this counts only
	// users who have actually signed in since the group existed — never the
	// identity provider's membership list. Do not present it as "the size of the
	// team".
	MembersSeen int `json:"member_count"`

	// BindingCount is how many scopes this group is bound at.
	BindingCount int `json:"binding_count"`

	// CreatedAt is when the group was created.
	CreatedAt time.Time `json:"created_at"`
}

// GroupMember is one recorded membership row.
//
// For a GroupKindOIDC group the list of these covers only users who have signed
// in — a member the IdP knows about who has never logged into Janus does not
// appear, because Janus has never seen a token for them.
type GroupMember struct {
	// UserID is the Janus user UUID.
	UserID string `json:"user_id"`
	// AddedAt is when the membership row was created: when an admin added the
	// user for a local group, or when a login sync first recorded them for an
	// oidc group.
	AddedAt time.Time `json:"created_at"`
}

// GroupInput is the payload for CreateGroup.
type GroupInput struct {
	// Name is required and unique across both kinds.
	Name string `json:"name"`
	// Kind is GroupKindOIDC or GroupKindLocal. Required.
	Kind string `json:"kind"`
	// ClaimValue is required for GroupKindOIDC and forbidden for
	// GroupKindLocal.
	ClaimValue string `json:"claim_value,omitempty"`
	// Description is optional free text.
	Description string `json:"description,omitempty"`
	// CanCreateProjects delegates project creation to the group.
	CanCreateProjects bool `json:"can_create_projects,omitempty"`
}

// validate enforces the two-kinds rule locally, so an impossible combination
// fails without a round trip.
func (in GroupInput) validate() error {
	if in.Name == "" {
		return errors.New("janus: group name is required")
	}
	switch in.Kind {
	case GroupKindLocal:
		if in.ClaimValue != "" {
			return errors.New("janus: a local group cannot have a claim value; its membership is the explicit member list")
		}
	case GroupKindOIDC:
		if in.ClaimValue == "" {
			return errors.New("janus: an oidc group requires a claim value; without one it matches nothing a token can assert")
		}
	default:
		return fmt.Errorf("janus: group kind must be %q or %q, got %q", GroupKindOIDC, GroupKindLocal, in.Kind)
	}
	return nil
}

// groupPage is one cursor page of the group catalog.
type groupPage struct {
	Groups     []Group `json:"groups"`
	NextCursor *string `json:"next_cursor"`
}

// ListGroups returns every group in the catalog, following cursor pagination.
// Needs instance-scoped `group:manage`.
func (c *Client) ListGroups(ctx context.Context) ([]Group, error) {
	out := []Group{}
	cursor := ""
	for {
		path := "/v1/groups?limit=100"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		var page groupPage
		if err := c.do(ctx, http.MethodGet, path, nil, &page); err != nil {
			return nil, err
		}
		out = append(out, page.Groups...)
		if page.NextCursor == nil || *page.NextCursor == "" {
			return out, nil
		}
		cursor = *page.NextCursor
	}
}

// GetGroup returns one group. Needs instance-scoped `group:manage`.
func (c *Client) GetGroup(ctx context.Context, groupID string) (*Group, error) {
	if groupID == "" {
		return nil, errors.New("janus: groupID is required")
	}
	var out struct {
		Group Group `json:"group"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/groups/"+url.PathEscape(groupID), nil, &out); err != nil {
		return nil, err
	}
	return &out.Group, nil
}

// CreateGroup creates a group. The kind/claim pairing is checked locally first.
// Needs instance-scoped `group:manage`.
func (c *Client) CreateGroup(ctx context.Context, in GroupInput) (*Group, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	var out Group
	if err := c.do(ctx, http.MethodPost, "/v1/groups", in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteGroup deletes a group. Membership and every binding it conferred
// cascade, so access granted through it is gone on the next request — Janus
// resolves permissions per request and never freezes them into a session.
// Needs instance-scoped `group:manage`.
func (c *Client) DeleteGroup(ctx context.Context, groupID string) error {
	if groupID == "" {
		return errors.New("janus: groupID is required")
	}
	return c.do(ctx, http.MethodDelete, "/v1/groups/"+url.PathEscape(groupID), nil, nil)
}

// SetGroupProjectCreation toggles the group's delegated project-creation
// capability. Needs instance-scoped `group:manage`.
func (c *Client) SetGroupProjectCreation(ctx context.Context, groupID string, allowed bool) error {
	if groupID == "" {
		return errors.New("janus: groupID is required")
	}
	body := map[string]bool{"can_create_projects": allowed}
	return c.do(ctx, http.MethodPut, "/v1/groups/"+url.PathEscape(groupID)+"/capabilities", body, nil)
}

// ListGroupMembers returns a group's recorded members, following cursor
// pagination. Needs instance-scoped `group:manage`.
//
// For an oidc group this is the login-time snapshot: it covers only users who
// have signed in, so treat it as "members seen at sign-in", never as the
// group's membership. The identity provider is the record for those groups.
func (c *Client) ListGroupMembers(ctx context.Context, groupID string) ([]GroupMember, error) {
	if groupID == "" {
		return nil, errors.New("janus: groupID is required")
	}
	out := []GroupMember{}
	cursor := ""
	for {
		path := "/v1/groups/" + url.PathEscape(groupID) + "/members?limit=100"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		var page struct {
			Members    []GroupMember `json:"members"`
			NextCursor *string       `json:"next_cursor"`
		}
		if err := c.do(ctx, http.MethodGet, path, nil, &page); err != nil {
			return nil, err
		}
		out = append(out, page.Members...)
		if page.NextCursor == nil || *page.NextCursor == "" {
			return out, nil
		}
		cursor = *page.NextCursor
	}
}

// AddGroupMember adds a user to a LOCAL group. Needs instance-scoped
// `group:manage`.
//
// An oidc group is refused with HTTP 409: its membership comes from the
// identity provider and is refreshed at each sign-in, and the schema makes a
// hand-added row unrepresentable. Check Kind first if you want to fail before
// the request.
func (c *Client) AddGroupMember(ctx context.Context, groupID, userID string) error {
	if groupID == "" || userID == "" {
		return errors.New("janus: groupID and userID are required")
	}
	return c.do(ctx, http.MethodPut, groupMemberPath(groupID, userID), nil, nil)
}

// RemoveGroupMember removes a user from a local group. Effective on that user's
// next request. Needs instance-scoped `group:manage`.
func (c *Client) RemoveGroupMember(ctx context.Context, groupID, userID string) error {
	if groupID == "" || userID == "" {
		return errors.New("janus: groupID and userID are required")
	}
	return c.do(ctx, http.MethodDelete, groupMemberPath(groupID, userID), nil, nil)
}

func groupMemberPath(groupID, userID string) string {
	return "/v1/groups/" + url.PathEscape(groupID) + "/members/" + url.PathEscape(userID)
}

// MyGroups returns the groups the CALLER belongs to.
//
// Unlike the rest of this file it needs no special authority — it is
// authenticated-only, because it reveals nothing but the caller's own
// memberships, and never the catalog. A service token belongs to no groups and
// gets an empty slice rather than an error, so it is safe to call
// unconditionally.
func (c *Client) MyGroups(ctx context.Context) ([]Group, error) {
	var out struct {
		Groups []Group `json:"groups"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/auth/me/groups", nil, &out); err != nil {
		return nil, err
	}
	if out.Groups == nil {
		out.Groups = []Group{}
	}
	return out.Groups, nil
}
