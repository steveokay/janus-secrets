package main

import "fmt"

// resolveConfigID walks projects→environments→configs, matching project & env by
// slug and config by name, and returns the config uuid the secret routes require.
func (c *apiClient) resolveConfigID(project, environment, config string) (string, error) {
	pid, eid, err := c.resolveEnvID(project, environment)
	if err != nil {
		return "", err
	}

	var cl struct {
		Configs []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"configs"`
	}
	if err := c.call("GET", "/v1/projects/"+pid+"/environments/"+eid+"/configs", nil, &cl); err != nil {
		return "", err
	}
	for _, cf := range cl.Configs {
		if cf.Name == config {
			return cf.ID, nil
		}
	}
	return "", fmt.Errorf("config %q not found in %s/%s", config, project, environment)
}

// resolveProjectID resolves a project slug to its uuid.
func (c *apiClient) resolveProjectID(project string) (string, error) {
	var pl struct {
		Projects []struct{ ID, Slug string } `json:"projects"`
	}
	if err := c.call("GET", "/v1/projects", nil, &pl); err != nil {
		return "", err
	}
	for _, p := range pl.Projects {
		if p.Slug == project {
			return p.ID, nil
		}
	}
	return "", fmt.Errorf("project %q not found", project)
}

// resolveEnvID resolves a project slug + environment slug to their uuids.
func (c *apiClient) resolveEnvID(project, environment string) (pid, eid string, err error) {
	pid, err = c.resolveProjectID(project)
	if err != nil {
		return "", "", err
	}
	var el struct {
		Environments []struct{ ID, Slug string } `json:"environments"`
	}
	if err = c.call("GET", "/v1/projects/"+pid+"/environments", nil, &el); err != nil {
		return "", "", err
	}
	for _, e := range el.Environments {
		if e.Slug == environment {
			return pid, e.ID, nil
		}
	}
	return "", "", fmt.Errorf("environment %q not found in project %q", environment, project)
}

// trashList is the shape of GET /v1/trash — soft-deleted items the caller may
// restore. Restore resolvers use this because the live list endpoints filter
// out deleted rows, so a deleted item's uuid is only discoverable here.
type trashList struct {
	Projects []struct {
		ID, Slug, Name string
	} `json:"projects"`
	Environments []struct {
		ID, Slug, Name string
		ProjectID      string `json:"project_id"`
	} `json:"environments"`
	Configs []struct {
		ID, Name      string
		EnvironmentID string `json:"environment_id"`
	} `json:"configs"`
}

func (c *apiClient) trash() (*trashList, error) {
	var t trashList
	if err := c.call("GET", "/v1/trash", nil, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// resolveDeletedProjectID finds a soft-deleted project by slug via GET /v1/trash.
func (c *apiClient) resolveDeletedProjectID(project string) (string, error) {
	t, err := c.trash()
	if err != nil {
		return "", err
	}
	for _, p := range t.Projects {
		if p.Slug == project {
			return p.ID, nil
		}
	}
	return "", fmt.Errorf("no soft-deleted project %q in trash", project)
}

// resolveDeletedEnvID resolves the (live) parent project slug to its uuid, then
// finds the soft-deleted environment by slug within it via GET /v1/trash.
func (c *apiClient) resolveDeletedEnvID(project, environment string) (pid, eid string, err error) {
	pid, err = c.resolveProjectID(project)
	if err != nil {
		return "", "", err
	}
	t, err := c.trash()
	if err != nil {
		return "", "", err
	}
	for _, e := range t.Environments {
		if e.Slug == environment && e.ProjectID == pid {
			return pid, e.ID, nil
		}
	}
	return "", "", fmt.Errorf("no soft-deleted environment %q in trash for project %q", environment, project)
}

// resolveDeletedConfigID resolves the (live) parent project+env, then finds the
// soft-deleted config by name within that environment via GET /v1/trash.
func (c *apiClient) resolveDeletedConfigID(project, environment, config string) (string, error) {
	_, eid, err := c.resolveEnvID(project, environment)
	if err != nil {
		return "", err
	}
	t, err := c.trash()
	if err != nil {
		return "", err
	}
	for _, cf := range t.Configs {
		if cf.Name == config && cf.EnvironmentID == eid {
			return cf.ID, nil
		}
	}
	return "", fmt.Errorf("no soft-deleted config %q in trash for %s/%s", config, project, environment)
}

// listEnvs returns a project's environments as a (slug→id) map plus the raw
// slice (which also carries id→slug, used to render pipelines in order).
func (c *apiClient) listEnvs(pid string) (map[string]string, []struct{ ID, Slug string }, error) {
	var el struct {
		Environments []struct{ ID, Slug string } `json:"environments"`
	}
	if err := c.call("GET", "/v1/projects/"+pid+"/environments", nil, &el); err != nil {
		return nil, nil, err
	}
	m := map[string]string{}
	for _, e := range el.Environments {
		m[e.Slug] = e.ID
	}
	return m, el.Environments, nil
}
