package secretsync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
)

// vercelAPIBase is the Vercel REST API root. Overridable in tests via the
// provider's baseURL field so no live call is ever made.
//
// Endpoint choices (documented stable v-endpoints as of 2026-07):
//   - upsert: POST /v10/projects/{idOrName}/env?upsert=true — create-or-update
//     by key in a single call (the `upsert` query param avoids a list+PATCH
//     dance; on an existing key it replaces the value).
//   - prune:  GET  /v10/projects/{idOrName}/env  → {envs:[{id,key}]} to map a
//     managed key name to its env-var id, then
//     DELETE /v10/projects/{idOrName}/env/{envId}.
//
// Values are written with type "encrypted" so Vercel stores them encrypted at
// rest. teamId is forwarded as a query param when Addr.VercelTeamID is set.
const vercelAPIBase = "https://api.vercel.com"

// vcIDRe constrains project id / team id that are interpolated into the request
// URL to a safe path/query-segment charset. This rejects any value that could
// smuggle extra path segments, query, or fragment into the request target
// (gosec G107) — the URL is built only from validated components + fixed path.
var vcIDRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// vercelTargets are the valid Vercel deployment environments.
var vercelTargets = map[string]bool{"production": true, "preview": true, "development": true}

// vercelProvider mirrors a config's resolved secrets to a Vercel project's
// Environment Variables via the Vercel REST API. Credentials are sent as a
// Bearer API token.
type vercelProvider struct {
	hc      *http.Client
	baseURL string // vercelAPIBase in prod; overridden by tests
}

func (vercelProvider) Name() string { return ProviderVercel }

// envBase returns the validated ".../v10/projects/:id/env" URL. VercelProject
// and (if set) VercelTeamID are validated against vcIDRe so neither can inject
// additional path/query into the request target.
func (p vercelProvider) envBase(a Addr) (string, error) {
	if !vcIDRe.MatchString(a.VercelProject) {
		return "", ErrInvalidConfig
	}
	if a.VercelTeamID != "" && !vcIDRe.MatchString(a.VercelTeamID) {
		return "", ErrInvalidConfig
	}
	base := p.baseURL
	if base == "" {
		base = vercelAPIBase
	}
	return base + "/v10/projects/" + a.VercelProject + "/env", nil
}

// teamQuery returns "?teamId=..." (or "&teamId=...") when a team is configured.
func (p vercelProvider) teamQuery(a Addr, sep string) string {
	if a.VercelTeamID == "" {
		return ""
	}
	return sep + "teamId=" + url.QueryEscape(a.VercelTeamID)
}

// vercelEnvBody is the upsert payload. type is always "encrypted".
type vercelEnvBody struct {
	Key    string   `json:"key"`
	Value  string   `json:"value"`
	Type   string   `json:"type"`
	Target []string `json:"target"`
}

// vercelUpsertResp is Vercel's create-response envelope. Only failed[] is read —
// it drives the error path; the created/error details are NEVER echoed (they may
// carry request context/value), so only the presence of a failure is used.
type vercelUpsertResp struct {
	Failed []json.RawMessage `json:"failed"`
}

// vercelListResp is the env-list response used only for prune (id↔key mapping).
type vercelListResp struct {
	Envs []struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	} `json:"envs"`
}

func (p vercelProvider) Apply(ctx context.Context, creds Creds, addr Addr, desired map[string]string,
	managedKeys []string, prune bool) (ApplyResult, error) {
	if creds.APIToken == "" || addr.VercelProject == "" {
		return ApplyResult{}, ErrInvalidConfig
	}
	targets, err := vercelResolveTargets(addr)
	if err != nil {
		return ApplyResult{}, err
	}
	base, err := p.envBase(addr)
	if err != nil {
		return ApplyResult{}, err
	}

	res := ApplyResult{Skipped: map[string]string{}}
	for key, val := range desired {
		if err := p.upsert(ctx, creds.APIToken, base, addr, key, val, targets); err != nil {
			return res, err
		}
		res.Applied = append(res.Applied, key)
	}

	if prune {
		desiredSet := map[string]bool{}
		for _, k := range res.Applied {
			desiredSet[k] = true
		}
		// Only list (which reveals no plaintext) if there is a chance of a delete.
		toPrune := false
		for _, k := range managedKeys {
			if !desiredSet[k] {
				toPrune = true
				break
			}
		}
		if toPrune {
			idByKey, err := p.list(ctx, creds.APIToken, base, addr)
			if err != nil {
				return res, err
			}
			for _, k := range managedKeys {
				if desiredSet[k] {
					continue
				}
				id, ok := idByKey[k]
				if !ok {
					continue // already gone → idempotent
				}
				if err := p.delete(ctx, creds.APIToken, base, addr, id); err != nil {
					return res, err
				}
			}
		}
	}
	return res, nil
}

// vercelResolveTargets validates the configured targets, defaulting to
// ["production"] when none are set.
func vercelResolveTargets(a Addr) ([]string, error) {
	if len(a.VercelTargets) == 0 {
		return []string{"production"}, nil
	}
	for _, tgt := range a.VercelTargets {
		if !vercelTargets[tgt] {
			return nil, ErrInvalidConfig
		}
	}
	return a.VercelTargets, nil
}

// upsert POSTs one env var with upsert=true (create-or-update by key).
func (p vercelProvider) upsert(ctx context.Context, token, base string, a Addr, key, val string, targets []string) error {
	body, _ := json.Marshal(vercelEnvBody{Key: key, Value: val, Type: "encrypted", Target: targets})
	target := base + "?upsert=true" + p.teamQuery(a, "&")
	resp, err := p.do(ctx, http.MethodPost, token, target, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: vercel status %d", ErrApplyFailed, resp.StatusCode)
	}
	// A 2xx can still carry per-key failures in failed[]; treat any as an error.
	var out vercelUpsertResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return fmt.Errorf("%w: bad response", ErrApplyFailed)
	}
	if len(out.Failed) > 0 {
		// Do NOT echo the failed[] entries — value-free category only.
		return fmt.Errorf("%w: vercel rejected env var", ErrApplyFailed)
	}
	return nil
}

// list returns key→env-var-id for every env var on the project (prune only).
func (p vercelProvider) list(ctx context.Context, token, base string, a Addr) (map[string]string, error) {
	target := base + p.teamQuery(a, "?")
	resp, err := p.do(ctx, http.MethodGet, token, target, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: vercel status %d", ErrApplyFailed, resp.StatusCode)
	}
	var out vercelListResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("%w: bad response", ErrApplyFailed)
	}
	idByKey := make(map[string]string, len(out.Envs))
	for _, e := range out.Envs {
		idByKey[e.Key] = e.ID
	}
	return idByKey, nil
}

// delete removes a managed env var by id. envID is validated to the safe
// charset so it cannot smuggle path/query; a 404 is idempotent success.
func (p vercelProvider) delete(ctx context.Context, token, base string, a Addr, envID string) error {
	if !vcIDRe.MatchString(envID) {
		return nil // unsafe id from the remote list → skip rather than fail
	}
	target := base + "/" + envID + p.teamQuery(a, "?")
	resp, err := p.do(ctx, http.MethodDelete, token, target, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: vercel status %d", ErrApplyFailed, resp.StatusCode)
	}
	return nil
}

// do performs one Vercel API call and returns the live response (caller closes
// the body). Transport errors are mapped to a value-free ErrApplyFailed — the
// token and secret value are never echoed.
func (p vercelProvider) do(ctx context.Context, method, token, target string, body []byte) (*http.Response, error) {
	var r *bytes.Reader
	if body != nil {
		r = bytes.NewReader(body)
	} else {
		r = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, r)
	if err != nil {
		return nil, ErrInvalidConfig
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: request error", ErrApplyFailed)
	}
	return resp, nil
}
