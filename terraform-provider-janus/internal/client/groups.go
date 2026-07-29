package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// Group kinds. A group is IdP-fed ("oidc") or admin-curated ("local") and never
// both — the split is what makes "access granted through an IdP group is fully
// described by the IdP" a true statement, and it is enforced in the database
// schema, not just at the API.
const (
	GroupKindOIDC  = "oidc"
	GroupKindLocal = "local"
)

// Group roles a BINDING may hold. Owner is deliberately absent: a group binding
// can never be owner (a CHECK constraint and a 400 at the API both say so), so
// the provider rejects it at plan time instead.
const (
	GroupRoleViewer    = "viewer"
	GroupRoleDeveloper = "developer"
	GroupRoleAdmin     = "admin"
	// GroupRoleOwner is named ONLY so the plan-time error can explain why it is
	// refused. It is never sent.
	GroupRoleOwner = "owner"
)

// Binding scope levels, mirroring the three group-member route families.
const (
	ScopeLevelInstance    = "instance"
	ScopeLevelProject     = "project"
	ScopeLevelEnvironment = "environment"
)

// Group mirrors the group view returned by /v1/groups.
//
// MemberCount is deliberately NOT modelled: for an `oidc` group the member list
// is a snapshot refreshed at each sign-in, so it covers only users who have
// actually signed in. Surfacing it as a Terraform attribute would read as a
// complete membership list and it is not one.
type Group struct {
	ID                string  `json:"id"`
	Name              string  `json:"name"`
	Kind              string  `json:"kind"`
	ClaimValue        *string `json:"claim_value"`
	Description       string  `json:"description"`
	CanCreateProjects bool    `json:"can_create_projects"`
}

// GroupInput is the create body for POST /v1/groups.
type GroupInput struct {
	Name              string `json:"name"`
	Kind              string `json:"kind"`
	ClaimValue        string `json:"claim_value,omitempty"`
	Description       string `json:"description,omitempty"`
	CanCreateProjects bool   `json:"can_create_projects,omitempty"`
}

// GroupBinding is one row of a scope's group-member listing.
type GroupBinding struct {
	GroupID       string  `json:"group_id"`
	ScopeLevel    string  `json:"scope_level"`
	ProjectID     *string `json:"project_id"`
	EnvironmentID *string `json:"environment_id"`
	Role          string  `json:"role"`
}

// BindingScope names WHERE a group binding applies. Exactly one of ProjectID /
// EnvironmentID is set, or neither for an instance-wide binding — the same
// three-way split the API expresses as three route families.
type BindingScope struct {
	ProjectID     string
	EnvironmentID string
}

// Level reports the scope level this BindingScope addresses.
func (s BindingScope) Level() string {
	switch {
	case s.EnvironmentID != "":
		return ScopeLevelEnvironment
	case s.ProjectID != "":
		return ScopeLevelProject
	default:
		return ScopeLevelInstance
	}
}

// path returns the group-members collection route for this scope. An
// environment-scoped binding needs the project id too, because the route is
// nested under it.
func (s BindingScope) path() (string, error) {
	switch s.Level() {
	case ScopeLevelEnvironment:
		if s.ProjectID == "" {
			return "", errors.New("janus: an environment-scoped group binding also needs project_id")
		}
		return fmt.Sprintf("/v1/projects/%s/environments/%s/group-members",
			url.PathEscape(s.ProjectID), url.PathEscape(s.EnvironmentID)), nil
	case ScopeLevelProject:
		return "/v1/projects/" + url.PathEscape(s.ProjectID) + "/group-members", nil
	default:
		return "/v1/instance/group-members", nil
	}
}

// ---- group catalog (instance-scoped `group:manage`) ----

// CreateGroup creates a group. The kind/claim-value pairing is validated here as
// well as at plan time so an invalid combination can never reach the API.
func (c *Client) CreateGroup(ctx context.Context, in GroupInput) (*Group, error) {
	if err := ValidateGroupInput(in.Kind, in.ClaimValue); err != nil {
		return nil, err
	}
	var out Group
	if err := c.do(ctx, http.MethodPost, "/v1/groups", in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ValidateGroupInput enforces the two-kinds rule locally: a `local` group has an
// explicit member list and no claim, an `oidc` group is defined BY its claim
// value and must carry one (an empty claim would match a group nothing can ever
// assert).
func ValidateGroupInput(kind, claimValue string) error {
	switch kind {
	case GroupKindLocal:
		if claimValue != "" {
			return errors.New(`janus: a "local" group cannot have a claim_value — its membership is the explicit member list`)
		}
	case GroupKindOIDC:
		if claimValue == "" {
			return errors.New(`janus: an "oidc" group requires a claim_value — it is the value the IdP emits for this group`)
		}
	default:
		return fmt.Errorf(`janus: group kind must be %q or %q, got %q`, GroupKindOIDC, GroupKindLocal, kind)
	}
	return nil
}

// GetGroup fetches one group. The endpoint answers {"group":…,"bindings":[…]};
// only the group is modelled here (bindings are their own resource).
func (c *Client) GetGroup(ctx context.Context, id string) (*Group, error) {
	var out struct {
		Group Group `json:"group"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/groups/"+url.PathEscape(id), nil, &out); err != nil {
		return nil, err
	}
	return &out.Group, nil
}

// DeleteGroup deletes a group. Membership and every binding it conferred cascade
// away, so the access is gone on the next request.
func (c *Client) DeleteGroup(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/groups/"+url.PathEscape(id), nil, nil)
}

// SetGroupCapability toggles delegated project creation for a group. This is the
// one mutable field of a group: everything else is create-only server-side.
func (c *Client) SetGroupCapability(ctx context.Context, id string, canCreateProjects bool) error {
	body := map[string]bool{"can_create_projects": canCreateProjects}
	return c.do(ctx, http.MethodPut, "/v1/groups/"+url.PathEscape(id)+"/capabilities", body, nil)
}

// ---- local group membership (instance-scoped `group:manage`) ----

// AddGroupMember adds a user to a LOCAL group. The server answers 409 for an
// `oidc` group; callers should reject that combination before getting here so
// the practitioner sees a useful message.
func (c *Client) AddGroupMember(ctx context.Context, groupID, userID string) error {
	return c.do(ctx, http.MethodPut, groupMemberPath(groupID, userID), nil, nil)
}

// RemoveGroupMember removes a user from a local group.
func (c *Client) RemoveGroupMember(ctx context.Context, groupID, userID string) error {
	return c.do(ctx, http.MethodDelete, groupMemberPath(groupID, userID), nil, nil)
}

func groupMemberPath(groupID, userID string) string {
	return "/v1/groups/" + url.PathEscape(groupID) + "/members/" + url.PathEscape(userID)
}

// HasGroupMember reports whether a user appears in a group's member list,
// walking the cursor pages. A missing group surfaces as an APIError 404 so the
// caller can drift the resource out of state.
//
// For an `oidc` group a false answer means "not seen at sign-in", not "not in
// the IdP group" — which is why janus_group_member is refused for oidc groups
// in the first place.
func (c *Client) HasGroupMember(ctx context.Context, groupID, userID string) (bool, error) {
	cursor := ""
	for {
		path := "/v1/groups/" + url.PathEscape(groupID) + "/members?limit=100"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		var page struct {
			Members []struct {
				UserID string `json:"user_id"`
			} `json:"members"`
			NextCursor *string `json:"next_cursor"`
		}
		if err := c.do(ctx, http.MethodGet, path, nil, &page); err != nil {
			return false, err
		}
		for _, m := range page.Members {
			if m.UserID == userID {
				return true, nil
			}
		}
		if page.NextCursor == nil || *page.NextCursor == "" {
			return false, nil
		}
		cursor = *page.NextCursor
	}
}

// ---- group bindings (scope-level `member:manage`, capped by your bound role) ----

// PutGroupBinding binds a group at a scope with a role. The route family is
// chosen by the scope, which is why BindingScope exists rather than a string.
//
// This is a DIFFERENT authority from the catalog calls above: it needs
// `member:manage` at that scope and is capped by the caller's own bound role, so
// a token that can create groups may still be unable to bind them anywhere.
func (c *Client) PutGroupBinding(ctx context.Context, scope BindingScope, groupID, role string) error {
	if err := ValidateGroupRole(role); err != nil {
		return err
	}
	base, err := scope.path()
	if err != nil {
		return err
	}
	body := map[string]string{"role": role}
	return c.do(ctx, http.MethodPut, base+"/"+url.PathEscape(groupID), body, nil)
}

// DeleteGroupBinding unbinds a group from a scope.
func (c *Client) DeleteGroupBinding(ctx context.Context, scope BindingScope, groupID string) error {
	base, err := scope.path()
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodDelete, base+"/"+url.PathEscape(groupID), nil, nil)
}

// GetGroupBinding finds a group's binding at a scope by listing that scope's
// group members. Returns an APIError{Status:404} when the group is not bound
// there, so resource Read can drift it out of state.
//
// Only the value-free binding rows are consumed; the endpoint's
// `derived_members` (who reaches the scope through those groups) is deliberately
// ignored — Terraform manages the grant, not the people it happens to cover.
func (c *Client) GetGroupBinding(ctx context.Context, scope BindingScope, groupID string) (*GroupBinding, error) {
	base, err := scope.path()
	if err != nil {
		return nil, err
	}
	cursor := ""
	for {
		path := base + "?limit=100"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		var page struct {
			Bindings   []GroupBinding `json:"bindings"`
			NextCursor *string        `json:"next_cursor"`
		}
		if err := c.do(ctx, http.MethodGet, path, nil, &page); err != nil {
			return nil, err
		}
		for i := range page.Bindings {
			if page.Bindings[i].GroupID == groupID {
				return &page.Bindings[i], nil
			}
		}
		if page.NextCursor == nil || *page.NextCursor == "" {
			return nil, &APIError{Status: http.StatusNotFound, Code: "not_found", Message: "group binding not found at this scope"}
		}
		cursor = *page.NextCursor
	}
}

// ValidateGroupRole rejects a role a group binding may not hold. Owner gets its
// own message because "invalid role" would be misleading — owner is a perfectly
// valid role, just never for a group.
func ValidateGroupRole(role string) error {
	switch role {
	case GroupRoleViewer, GroupRoleDeveloper, GroupRoleAdmin:
		return nil
	case GroupRoleOwner:
		return errors.New(OwnerRoleRefusal)
	default:
		return fmt.Errorf("janus: role must be %q, %q or %q, got %q",
			GroupRoleViewer, GroupRoleDeveloper, GroupRoleAdmin, role)
	}
}

// OwnerRoleRefusal is the single wording for "a group can never be owner",
// shared by the plan-time validator and the pre-flight check so a practitioner
// gets the same explanation either way.
const OwnerRoleRefusal = `janus: a group binding can never be "owner" — owner rotates the master key, ` +
	`prunes the audit chain and destroys secret history, so it must be a direct binding on a person. ` +
	`Bind an owner with a user binding instead.`
