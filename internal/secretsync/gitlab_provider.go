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
