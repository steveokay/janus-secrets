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

// netlifyAPIBase is the Netlify REST API root. Overridable in tests via the
// provider's baseURL field so no live call is ever made.
//
// Endpoint choices (documented stable v1 endpoints as of 2026-07):
//   - create: POST   /api/v1/accounts/{account_id}/env?site_id={site_id}
//     (array body). Returns 409/400 when a key already exists.
//   - update: PUT    /api/v1/accounts/{account_id}/env/{key}?site_id={site_id}
//     replaces all values of an existing key — used as the create→conflict
//     fallback so Apply is an upsert.
//   - prune:  DELETE /api/v1/accounts/{account_id}/env/{key}?site_id={site_id}
//
// Values are written with context "all" (applies to every deploy context). The
// site_id query param scopes the variable to a single site rather than the whole
// team/account.
const netlifyAPIBase = "https://api.netlify.com"

// nfIDRe constrains account_id / site_id interpolated into the request URL to a
// safe path/query-segment charset, rejecting anything that could smuggle extra
// path segments, query, or fragment (gosec G107).
var nfIDRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// netlifyProvider mirrors a config's resolved secrets to a Netlify site's
// environment variables via the Netlify REST API. Credentials are sent as a
// Bearer personal access token.
type netlifyProvider struct {
	hc      *http.Client
	baseURL string // netlifyAPIBase in prod; overridden by tests
}

func (netlifyProvider) Name() string { return ProviderNetlify }

// envBase returns the validated ".../api/v1/accounts/:acct/env" URL. AccountID
// and (if set) SiteID are validated against nfIDRe so neither can inject
// additional path/query into the request target.
func (p netlifyProvider) envBase(a Addr) (string, error) {
	if !nfIDRe.MatchString(a.NetlifyAccountID) {
		return "", ErrInvalidConfig
	}
	if a.NetlifySiteID != "" && !nfIDRe.MatchString(a.NetlifySiteID) {
		return "", ErrInvalidConfig
	}
	base := p.baseURL
	if base == "" {
		base = netlifyAPIBase
	}
	return base + "/api/v1/accounts/" + a.NetlifyAccountID + "/env", nil
}

// siteQuery returns "?site_id=..." (or "&site_id=...") when a site is configured.
func (p netlifyProvider) siteQuery(a Addr, sep string) string {
	if a.NetlifySiteID == "" {
		return ""
	}
	return sep + "site_id=" + url.QueryEscape(a.NetlifySiteID)
}

// netlifyValue is one contextual value of an env var.
type netlifyValue struct {
	Value   string `json:"value"`
	Context string `json:"context"`
}

// netlifyEnvVar is the create/update payload for one variable. context "all"
// applies the value across every deploy context.
type netlifyEnvVar struct {
	Key    string         `json:"key"`
	Values []netlifyValue `json:"values"`
}

func (p netlifyProvider) Apply(ctx context.Context, creds Creds, addr Addr, desired map[string]string,
	managedKeys []string, prune bool) (ApplyResult, error) {
	if creds.APIToken == "" || addr.NetlifyAccountID == "" {
		return ApplyResult{}, ErrInvalidConfig
	}
	base, err := p.envBase(addr)
	if err != nil {
		return ApplyResult{}, err
	}

	res := ApplyResult{Skipped: map[string]string{}}
	for key, val := range desired {
		if err := p.upsert(ctx, creds.APIToken, base, addr, key, val); err != nil {
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
			if err := p.delete(ctx, creds.APIToken, base, addr, k); err != nil {
				return res, err
			}
		}
	}
	return res, nil
}

// upsert POSTs to create; on a 409/400 conflict (key exists) it PUTs to replace.
func (p netlifyProvider) upsert(ctx context.Context, token, base string, a Addr, key, val string) error {
	arr := []netlifyEnvVar{{Key: key, Values: []netlifyValue{{Value: val, Context: "all"}}}}
	body, _ := json.Marshal(arr)
	status, err := p.do(ctx, http.MethodPost, token, base+p.siteQuery(a, "?"), body)
	if err != nil {
		return err
	}
	if status == http.StatusConflict || status == http.StatusBadRequest {
		// Already exists → replace all values by key.
		// The key must be a safe path segment; if not, skip rather than smuggle.
		if !nfIDRe.MatchString(key) {
			return nil
		}
		one, _ := json.Marshal(netlifyEnvVar{Key: key, Values: []netlifyValue{{Value: val, Context: "all"}}})
		st, err := p.do(ctx, http.MethodPut, token, base+"/"+url.PathEscape(key)+p.siteQuery(a, "?"), one)
		if err != nil {
			return err
		}
		if st < 200 || st >= 300 {
			return fmt.Errorf("%w: netlify status %d", ErrApplyFailed, st)
		}
		return nil
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("%w: netlify status %d", ErrApplyFailed, status)
	}
	return nil
}

// delete removes a managed env var by key. An unsafe key name is skipped (never
// interpolated into the path); a 404 is idempotent success.
func (p netlifyProvider) delete(ctx context.Context, token, base string, a Addr, key string) error {
	if !nfIDRe.MatchString(key) {
		return nil
	}
	st, err := p.do(ctx, http.MethodDelete, token, base+"/"+url.PathEscape(key)+p.siteQuery(a, "?"), nil)
	if err != nil {
		return err
	}
	if st == http.StatusNotFound {
		return nil
	}
	if st < 200 || st >= 300 {
		return fmt.Errorf("%w: netlify status %d", ErrApplyFailed, st)
	}
	return nil
}

// do performs one Netlify API call and returns the status code. Errors carry
// only a category — never the token, request body, or secret value. The response
// body is drained-and-closed so the connection can be reused.
func (p netlifyProvider) do(ctx context.Context, method, token, target string, body []byte) (int, error) {
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
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.hc.Do(req)
	if err != nil {
		return 0, fmt.Errorf("%w: request error", ErrApplyFailed)
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}
