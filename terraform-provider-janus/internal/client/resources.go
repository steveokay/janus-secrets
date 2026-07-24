package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// ---- Projects ----

// CreateProject creates a project (slug required, name optional).
func (c *Client) CreateProject(ctx context.Context, slug, name string) (*Project, error) {
	body := map[string]string{"slug": slug}
	if name != "" {
		body["name"] = name
	}
	var out Project
	if err := c.do(ctx, http.MethodPost, "/v1/projects", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetProject fetches a project by id.
func (c *Client) GetProject(ctx context.Context, id string) (*Project, error) {
	var out Project
	if err := c.do(ctx, http.MethodGet, "/v1/projects/"+url.PathEscape(id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateProject renames a project (name only; slug is immutable server-side).
func (c *Client) UpdateProject(ctx context.Context, id, name string) (*Project, error) {
	var out Project
	body := map[string]string{"name": name}
	if err := c.do(ctx, http.MethodPatch, "/v1/projects/"+url.PathEscape(id), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteProject soft-deletes (destroy=false) or hard-destroys a project.
func (c *Client) DeleteProject(ctx context.Context, id string, destroy bool) error {
	path := "/v1/projects/" + url.PathEscape(id)
	if destroy {
		path += "?destroy=true"
	}
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}

// ---- Environments ----

// CreateEnvironment creates an environment under a project.
func (c *Client) CreateEnvironment(ctx context.Context, projectID, slug, name string) (*Environment, error) {
	body := map[string]string{"slug": slug}
	if name != "" {
		body["name"] = name
	}
	var out Environment
	path := fmt.Sprintf("/v1/projects/%s/environments", url.PathEscape(projectID))
	if err := c.do(ctx, http.MethodPost, path, body, &out); err != nil {
		return nil, err
	}
	// The server sets project_id; backfill defensively for older builds.
	if out.ProjectID == "" {
		out.ProjectID = projectID
	}
	return &out, nil
}

// GetEnvironment fetches one environment.
func (c *Client) GetEnvironment(ctx context.Context, projectID, envID string) (*Environment, error) {
	var out Environment
	path := fmt.Sprintf("/v1/projects/%s/environments/%s", url.PathEscape(projectID), url.PathEscape(envID))
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	if out.ProjectID == "" {
		out.ProjectID = projectID
	}
	return &out, nil
}

// UpdateEnvironment renames an environment (name only).
func (c *Client) UpdateEnvironment(ctx context.Context, projectID, envID, name string) (*Environment, error) {
	var out Environment
	body := map[string]string{"name": name}
	path := fmt.Sprintf("/v1/projects/%s/environments/%s", url.PathEscape(projectID), url.PathEscape(envID))
	if err := c.do(ctx, http.MethodPatch, path, body, &out); err != nil {
		return nil, err
	}
	if out.ProjectID == "" {
		out.ProjectID = projectID
	}
	return &out, nil
}

// DeleteEnvironment soft-deletes or hard-destroys an environment.
func (c *Client) DeleteEnvironment(ctx context.Context, projectID, envID string, destroy bool) error {
	path := fmt.Sprintf("/v1/projects/%s/environments/%s", url.PathEscape(projectID), url.PathEscape(envID))
	if destroy {
		path += "?destroy=true"
	}
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}

// ---- Configs ----

// CreateConfig creates a config under an environment. inheritsFrom may be nil.
func (c *Client) CreateConfig(ctx context.Context, projectID, envID, name string, inheritsFrom *string) (*Config, error) {
	body := map[string]any{"name": name}
	if inheritsFrom != nil && *inheritsFrom != "" {
		body["inherits_from"] = *inheritsFrom
	}
	var out Config
	path := fmt.Sprintf("/v1/projects/%s/environments/%s/configs", url.PathEscape(projectID), url.PathEscape(envID))
	if err := c.do(ctx, http.MethodPost, path, body, &out); err != nil {
		return nil, err
	}
	if out.EnvironmentID == "" {
		out.EnvironmentID = envID
	}
	return &out, nil
}

// GetConfig fetches a config by id (config routes are addressed by cid alone).
func (c *Client) GetConfig(ctx context.Context, configID string) (*Config, error) {
	var out Config
	if err := c.do(ctx, http.MethodGet, "/v1/configs/"+url.PathEscape(configID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteConfig soft-deletes or hard-destroys a config.
func (c *Client) DeleteConfig(ctx context.Context, configID string, destroy bool) error {
	path := "/v1/configs/" + url.PathEscape(configID)
	if destroy {
		path += "?destroy=true"
	}
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}

// ---- Secrets ----

// SetSecret writes a single secret key, creating a new config version.
func (c *Client) SetSecret(ctx context.Context, configID, key, value string) error {
	body := map[string]string{"value": value}
	path := fmt.Sprintf("/v1/configs/%s/secrets/%s", url.PathEscape(configID), url.PathEscape(key))
	return c.do(ctx, http.MethodPut, path, body, nil)
}

// GetSecret reveals a single secret's raw stored value (audited server-side).
// raw=true avoids reference resolution so the value round-trips exactly.
func (c *Client) GetSecret(ctx context.Context, configID, key string) (string, error) {
	var out struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	path := fmt.Sprintf("/v1/configs/%s/secrets/%s?raw=true", url.PathEscape(configID), url.PathEscape(key))
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return "", err
	}
	return out.Value, nil
}

// DeleteSecret removes a single secret key (creates a new config version).
func (c *Client) DeleteSecret(ctx context.Context, configID, key string) error {
	path := fmt.Sprintf("/v1/configs/%s/secrets/%s", url.PathEscape(configID), url.PathEscape(key))
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}

// ---- Service tokens ----

// MintToken mints a service token; the raw token is returned exactly once.
// scopeKind is "config" or "environment"; access is "read" or "readwrite".
func (c *Client) MintToken(ctx context.Context, name, scopeKind, scopeID, access string) (*MintedToken, error) {
	body := map[string]any{
		"name":   name,
		"scope":  map[string]string{"kind": scopeKind, "id": scopeID},
		"access": access,
	}
	var out MintedToken
	if err := c.do(ctx, http.MethodPost, "/v1/tokens", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetTokenMeta looks up a token's metadata by id from the (paginated) list.
// Returns an APIError{Status:404} when not present so Read can drift it out.
func (c *Client) GetTokenMeta(ctx context.Context, id string) (*TokenMeta, error) {
	cursor := ""
	for {
		path := "/v1/tokens?limit=100"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		var page struct {
			Tokens     []TokenMeta `json:"tokens"`
			NextCursor *string     `json:"next_cursor"`
		}
		if err := c.do(ctx, http.MethodGet, path, nil, &page); err != nil {
			return nil, err
		}
		for i := range page.Tokens {
			if page.Tokens[i].ID == id {
				return &page.Tokens[i], nil
			}
		}
		if page.NextCursor == nil || *page.NextCursor == "" {
			return nil, &APIError{Status: http.StatusNotFound, Code: "not_found", Message: "service token not found"}
		}
		cursor = *page.NextCursor
	}
}

// RevokeToken revokes a service token.
func (c *Client) RevokeToken(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/tokens/"+url.PathEscape(id), nil, nil)
}
