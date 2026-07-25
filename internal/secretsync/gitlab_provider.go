package secretsync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// defaultGitLabURL is used when Addr.GitLabURL is empty (SaaS gitlab.com).
const defaultGitLabURL = "https://gitlab.com"

// gitlabProjectRe constrains Addr.Project — which is interpolated verbatim into
// the ".../projects/:id/variables" request URL — to a safe path-segment value.
// Accepts EITHER a numeric project id (42) OR an already-URL-encoded
// namespace/project path (group%2Fproj). The encoded-path charset deliberately
// excludes raw '/', '?', '#', '&', whitespace, and control chars so a crafted
// value cannot smuggle extra path segments, query params, or a fragment into the
// authenticated request target (gosec G107). Matches the sibling providers'
// cfIDRe / vcIDRe / nfIDRe style.
var gitlabProjectRe = regexp.MustCompile(`^([0-9]+|[A-Za-z0-9._~%-]+)$`)

// gitlabProvider mirrors a config's resolved secrets to a GitLab project's
// CI/CD variables via the GitLab REST API. Credentials are sent as a
// PRIVATE-TOKEN header (PAT or project access token with the `api` scope).
type gitlabProvider struct {
	hc *http.Client
}

func (gitlabProvider) Name() string { return ProviderGitLab }

// variablesBase returns the validated ".../api/v4/projects/:id/variables" URL.
// The base host is parsed (never string-concatenated blindly) so a malformed
// gitlab_url is rejected rather than smuggled into a request target (gosec
// G107). :id is validated against gitlabProjectRe — either a numeric id or an
// already-URL-encoded group/proj path (e.g. "g%2Fp") — so a crafted Project
// cannot inject extra path/query/fragment into the request target. Enforced
// here defensively even though validateInput also rejects a bad Project.
func (g gitlabProvider) variablesBase(a Addr) (string, error) {
	raw := a.GitLabURL
	if raw == "" {
		raw = defaultGitLabURL
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", ErrInvalidConfig
	}
	if !gitlabProjectRe.MatchString(a.Project) {
		return "", ErrInvalidConfig
	}
	base := strings.TrimRight(u.Scheme+"://"+u.Host+u.Path, "/")
	return base + "/api/v4/projects/" + a.Project + "/variables", nil
}

func (g gitlabProvider) Apply(ctx context.Context, creds Creds, addr Addr, desired map[string]string,
	managedKeys []string, prune bool) (ApplyResult, error) {
	if creds.Token == "" || addr.Project == "" {
		return ApplyResult{}, ErrInvalidConfig
	}
	base, err := g.variablesBase(addr)
	if err != nil {
		return ApplyResult{}, err
	}

	res := ApplyResult{Skipped: map[string]string{}}
	for key, val := range desired {
		if err := g.upsert(ctx, creds.Token, base, key, val, addr.EnvironmentScope); err != nil {
			return res, err
		}
		res.Applied = append(res.Applied, key)
	}

	if prune {
		desiredSet := map[string]bool{}
		for _, k := range res.Applied {
			desiredSet[k] = true
		}
		for _, k := range managedKeys {
			if desiredSet[k] {
				continue
			}
			if err := g.delete(ctx, creds.Token, base, k); err != nil {
				return res, err
			}
		}
	}
	return res, nil
}

// ── drift verification ───────────────────────────────────────────────────────
//
// GitLab's CI/CD variables list endpoint returns each variable's value with the
// same `api`-scoped token the sync already uses, so this provider supports real
// value drift detection.

func (gitlabProvider) Capability() Capability { return CapValues }

// glVarRead is one entry of the variables-list response.
type glVarRead struct {
	Key              string `json:"key"`
	Value            string `json:"value"`
	EnvironmentScope string `json:"environment_scope"`
}

// glListPerPage / glListMaxPages bound the paginated variables listing.
const (
	glListPerPage  = 100
	glListMaxPages = 20
)

// Fetch lists the project's CI/CD variables. When the target pins an
// environment_scope, only variables in that scope are considered — otherwise a
// same-named variable scoped elsewhere would masquerade as the managed one.
func (g gitlabProvider) Fetch(ctx context.Context, creds Creds, addr Addr, _ []string) (RemoteState, error) {
	if creds.Token == "" || addr.Project == "" {
		return RemoteState{}, ErrInvalidConfig
	}
	base, err := g.variablesBase(addr)
	if err != nil {
		return RemoteState{}, err
	}
	var names []string
	values := map[string]string{}
	for page := 1; page <= glListMaxPages; page++ {
		target := fmt.Sprintf("%s?per_page=%d&page=%d", base, glListPerPage, page)
		vars, err := g.listPage(ctx, creds.Token, target)
		if err != nil {
			return RemoteState{}, err
		}
		for _, v := range vars {
			if addr.EnvironmentScope != "" && v.EnvironmentScope != "" &&
				v.EnvironmentScope != addr.EnvironmentScope {
				continue
			}
			names = append(names, v.Key)
			values[v.Key] = v.Value
		}
		if len(vars) < glListPerPage {
			break
		}
	}
	return RemoteState{Names: names, Values: values}, nil
}

// listPage GETs one page of variables. On any non-2xx the error carries only
// the status code — never the token or a variable value.
func (g gitlabProvider) listPage(ctx context.Context, token, target string) ([]glVarRead, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, bytes.NewReader(nil))
	if err != nil {
		return nil, ErrInvalidConfig
	}
	req.Header.Set("PRIVATE-TOKEN", token)
	resp, err := g.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: request error", ErrApplyFailed)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: gitlab status %d", ErrApplyFailed, resp.StatusCode)
	}
	var out []glVarRead
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("%w: bad response", ErrApplyFailed)
	}
	return out, nil
}

// glVarBody is the create/update payload. masked and protected are pinned
// false: GitLab rejects masked=true for values that don't match its mask regex,
// which would turn ordinary secrets into spurious sync failures. Masking is a
// documented follow-up, not a silent default.
type glVarBody struct {
	Key              string `json:"key"`
	Value            string `json:"value"`
	Masked           bool   `json:"masked"`
	Protected        bool   `json:"protected"`
	EnvironmentScope string `json:"environment_scope,omitempty"`
}

// upsert creates the variable; if it already exists (409), it updates it.
func (g gitlabProvider) upsert(ctx context.Context, token, base, key, val, envScope string) error {
	body, _ := json.Marshal(glVarBody{
		Key: key, Value: val, Masked: false, Protected: false, EnvironmentScope: envScope,
	})
	// Try create.
	status, err := g.do(ctx, http.MethodPost, token, base, body)
	if err != nil {
		return err
	}
	if status == http.StatusConflict {
		// Already exists → update by key.
		upBody, _ := json.Marshal(glVarBody{
			Value: val, Masked: false, Protected: false, EnvironmentScope: envScope,
		})
		st, err := g.do(ctx, http.MethodPut, token, base+"/"+url.PathEscape(key), upBody)
		if err != nil {
			return err
		}
		if st < 200 || st >= 300 {
			return fmt.Errorf("%w: gitlab status %d", ErrApplyFailed, st)
		}
		return nil
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("%w: gitlab status %d", ErrApplyFailed, status)
	}
	return nil
}

func (g gitlabProvider) delete(ctx context.Context, token, base, key string) error {
	st, err := g.do(ctx, http.MethodDelete, token, base+"/"+url.PathEscape(key), nil)
	if err != nil {
		return err
	}
	// 404 = already gone; treat as success (idempotent prune).
	if st == http.StatusNotFound {
		return nil
	}
	if st < 200 || st >= 300 {
		return fmt.Errorf("%w: gitlab status %d", ErrApplyFailed, st)
	}
	return nil
}

// do performs one GitLab API call and returns the status code. Errors carry
// only a category — never the token, request body, or secret value.
func (g gitlabProvider) do(ctx context.Context, method, token, target string, body []byte) (int, error) {
	var r *bytes.Reader
	if body != nil {
		r = bytes.NewReader(body)
	} else {
		r = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, r)
	if err != nil {
		return 0, ErrInvalidConfig
	}
	req.Header.Set("PRIVATE-TOKEN", token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := g.hc.Do(req)
	if err != nil {
		return 0, fmt.Errorf("%w: request error", ErrApplyFailed)
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}
