package secretsync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
)

// cloudflareAPIBase is the Cloudflare API v4 root. Overridable in tests via the
// provider's baseURL field so no live call is ever made.
const cloudflareAPIBase = "https://api.cloudflare.com/client/v4"

// cfIDRe constrains the account_id/script_name that are interpolated into the
// request URL to a safe path segment charset. This rejects any value that could
// smuggle extra path segments, query, or fragment into the request target
// (gosec G107) — the URL is built only from validated components + fixed path.
var cfIDRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// cloudflareProvider mirrors a config's resolved secrets to a deployed
// Cloudflare Worker script's secret bindings via the Workers Scripts API.
// Credentials are sent as a Bearer API token (Workers Scripts Edit).
type cloudflareProvider struct {
	hc      *http.Client
	baseURL string // cloudflareAPIBase in prod; overridden by tests
}

func (cloudflareProvider) Name() string { return ProviderCloudflare }

// secretsBase returns the validated ".../scripts/:script/secrets" URL. Both
// account_id and script_name are validated against cfIDRe so neither can inject
// additional path/query into the request target.
func (c cloudflareProvider) secretsBase(a Addr) (string, error) {
	if !cfIDRe.MatchString(a.AccountID) || !cfIDRe.MatchString(a.ScriptName) {
		return "", ErrInvalidConfig
	}
	base := c.baseURL
	if base == "" {
		base = cloudflareAPIBase
	}
	return base + "/accounts/" + a.AccountID + "/workers/scripts/" + a.ScriptName + "/secrets", nil
}

// cfEnvelope is Cloudflare's uniform response wrapper. Only success is read —
// it drives the error path; the errors/result fields are NEVER echoed (they may
// carry request context), so they are deliberately not decoded here.
type cfEnvelope struct {
	Success bool `json:"success"`
}

// cfSecretBody is the upsert payload. type is always "secret_text".
type cfSecretBody struct {
	Name string `json:"name"`
	Text string `json:"text"`
	Type string `json:"type"`
}

func (c cloudflareProvider) Apply(ctx context.Context, creds Creds, addr Addr, desired map[string]string,
	managedKeys []string, prune bool) (ApplyResult, error) {
	if creds.APIToken == "" || addr.AccountID == "" || addr.ScriptName == "" {
		return ApplyResult{}, ErrInvalidConfig
	}
	base, err := c.secretsBase(addr)
	if err != nil {
		return ApplyResult{}, err
	}

	res := ApplyResult{Skipped: map[string]string{}}
	for key, val := range desired {
		if err := c.upsert(ctx, creds.APIToken, base, key, val); err != nil {
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
			if err := c.delete(ctx, creds.APIToken, base, k); err != nil {
				return res, err
			}
		}
	}
	return res, nil
}

// upsert PUTs a secret to the Worker script. Cloudflare's PUT .../secrets
// endpoint creates-or-updates by name.
func (c cloudflareProvider) upsert(ctx context.Context, token, base, key, val string) error {
	body, _ := json.Marshal(cfSecretBody{Name: key, Text: val, Type: "secret_text"})
	_, err := c.do(ctx, http.MethodPut, token, base, body, true)
	return err
}

// delete removes a managed secret no longer desired. A 404 (already gone) is
// treated as success for an idempotent prune.
func (c cloudflareProvider) delete(ctx context.Context, token, base, key string) error {
	// key is a Cloudflare secret binding name; it is not URL-encoded by the
	// upstream API path — validate it to the same safe charset so it cannot
	// smuggle path/query. Skip (do not fail) any name that would be unsafe.
	if !cfIDRe.MatchString(key) {
		return nil
	}
	_, err := c.do(ctx, http.MethodDelete, token, base+"/"+key, nil, false)
	return err
}

// do performs one Cloudflare API call. It checks both the HTTP status and the
// {success,errors} envelope. On failure it returns a value-free ErrApplyFailed
// — the response body (which may contain secret/context) is never echoed.
// checkEnvelope controls whether the JSON envelope's success flag is parsed
// (PUT returns one; a 204/404 DELETE may not).
func (c cloudflareProvider) do(ctx context.Context, method, token, target string, body []byte, checkEnvelope bool) (int, error) {
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
	resp, err := c.hc.Do(req)
	if err != nil {
		return 0, fmt.Errorf("%w: request error", ErrApplyFailed)
	}
	defer resp.Body.Close()

	// DELETE prune: a 404 means already-gone → idempotent success.
	if method == http.MethodDelete && resp.StatusCode == http.StatusNotFound {
		return resp.StatusCode, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("%w: cloudflare status %d", ErrApplyFailed, resp.StatusCode)
	}
	if checkEnvelope {
		var env cfEnvelope
		if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
			return resp.StatusCode, fmt.Errorf("%w: bad response", ErrApplyFailed)
		}
		if !env.Success {
			// Do NOT echo env.Errors — value-free category only.
			return resp.StatusCode, fmt.Errorf("%w: cloudflare api error", ErrApplyFailed)
		}
	}
	return resp.StatusCode, nil
}
