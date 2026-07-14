package main

import "fmt"

// resolveConfigID walks projects→environments→configs, matching project & env by
// slug and config by name, and returns the config uuid the secret routes require.
func (c *apiClient) resolveConfigID(project, environment, config string) (string, error) {
	var pl struct {
		Projects []struct{ ID, Slug string } `json:"projects"`
	}
	if err := c.call("GET", "/v1/projects", nil, &pl); err != nil {
		return "", err
	}
	pid := ""
	for _, p := range pl.Projects {
		if p.Slug == project {
			pid = p.ID
			break
		}
	}
	if pid == "" {
		return "", fmt.Errorf("project %q not found", project)
	}

	var el struct {
		Environments []struct{ ID, Slug string } `json:"environments"`
	}
	if err := c.call("GET", "/v1/projects/"+pid+"/environments", nil, &el); err != nil {
		return "", err
	}
	eid := ""
	for _, e := range el.Environments {
		if e.Slug == environment {
			eid = e.ID
			break
		}
	}
	if eid == "" {
		return "", fmt.Errorf("environment %q not found in project %q", environment, project)
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
