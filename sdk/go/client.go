// Package janus is a typed Go client for the Janus secrets manager's /v1 REST
// API. It provides programmatic secret reads with an in-process, memory-only
// TTL cache and (optional) dynamic-credential lease management.
//
// The SDK talks to Janus over HTTP using a scoped service token
// (janus_svc_...). It never imports the Janus server's internal packages and
// never writes secret values to disk: the cache is memory-only, and no method
// logs secret values. Reads go through the audited reveal endpoints, so every
// GetSecret / GetSecrets is recorded server-side as a secret.reveal event —
// that is expected and intentional.
//
// Basic usage:
//
//	c, err := janus.NewClient("https://janus.example.com",
//	        janus.WithToken(os.Getenv("JANUS_TOKEN")))
//	if err != nil {
//	        log.Fatal(err)
//	}
//	secrets, err := c.GetSecrets(ctx, configID)
//	if err != nil {
//	        log.Fatal(err)
//	}
//	// use secrets["DATABASE_URL"] — never log the value.
package janus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// DefaultCacheTTL is the default time-to-live for cached config reads when no
// WithCacheTTL option is supplied.
const DefaultCacheTTL = 30 * time.Second

// maxErrorBody bounds how much of an error response body the SDK will read
// when parsing the error envelope, so a misbehaving endpoint can't force
// unbounded allocation.
const maxErrorBody = 1 << 16 // 64 KiB

// Client is a Janus API client. It is safe for concurrent use by multiple
// goroutines.
type Client struct {
	baseURL string
	token   string
	http    *http.Client

	cacheTTL time.Duration // zero disables caching

	// now is the clock used for cache expiry; overridable in tests.
	now func() time.Time

	mu    sync.Mutex
	cache map[string]cacheEntry // key: configID
}

type cacheEntry struct {
	secrets   map[string]string
	expiresAt time.Time
}

// Option configures a Client.
type Option func(*Client)

// WithToken sets the service token (a janus_svc_... token) sent as
// "Authorization: Bearer <token>" on every request.
func WithToken(token string) Option {
	return func(c *Client) { c.token = token }
}

// WithHTTPClient sets the underlying *http.Client, letting callers control
// timeouts, transport, and TLS. If unset, a client with a 30s timeout is used.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) {
		if h != nil {
			c.http = h
		}
	}
}

// WithCacheTTL sets the in-process cache TTL for config reads. A zero or
// negative duration disables caching entirely (every read hits the server).
func WithCacheTTL(ttl time.Duration) Option {
	return func(c *Client) { c.cacheTTL = ttl }
}

// withClock overrides the clock; used by tests to make cache expiry
// deterministic.
func withClock(now func() time.Time) Option {
	return func(c *Client) {
		if now != nil {
			c.now = now
		}
	}
}

// NewClient constructs a Client for the given Janus base URL (e.g.
// "https://janus.example.com" — the "/v1" prefix is added automatically).
func NewClient(baseURL string, opts ...Option) (*Client, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, errors.New("janus: baseURL is required")
	}
	if _, err := url.Parse(baseURL); err != nil {
		return nil, fmt.Errorf("janus: invalid baseURL: %w", err)
	}
	c := &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		http:     &http.Client{Timeout: 30 * time.Second},
		cacheTTL: DefaultCacheTTL,
		now:      time.Now,
		cache:    make(map[string]cacheEntry),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// GetSecrets returns a config's resolved secrets as a key/value map. References
// are resolved server-side. Results are cached in memory for the configured
// TTL; within the TTL, repeated calls return the cached map without hitting the
// server. This is an audited reveal (secret.reveal) on cache miss.
//
// The returned map is a copy; mutating it does not affect the cache.
func (c *Client) GetSecrets(ctx context.Context, configID string) (map[string]string, error) {
	if configID == "" {
		return nil, errors.New("janus: configID is required")
	}

	if c.cacheTTL > 0 {
		c.mu.Lock()
		if e, ok := c.cache[configID]; ok && c.now().Before(e.expiresAt) {
			out := copyMap(e.secrets)
			c.mu.Unlock()
			return out, nil
		}
		c.mu.Unlock()
	}

	secrets, err := c.fetchSecrets(ctx, configID)
	if err != nil {
		return nil, err
	}

	if c.cacheTTL > 0 {
		c.mu.Lock()
		c.cache[configID] = cacheEntry{
			secrets:   copyMap(secrets),
			expiresAt: c.now().Add(c.cacheTTL),
		}
		c.mu.Unlock()
	}
	return copyMap(secrets), nil
}

// GetSecret returns a single resolved secret value from a config. When caching
// is enabled and the config is already cached (and fresh), the value is served
// from the cached batch; otherwise the config is fetched (and cached) via the
// batch reveal. A missing key yields ErrNotFound.
func (c *Client) GetSecret(ctx context.Context, configID, key string) (string, error) {
	if configID == "" {
		return "", errors.New("janus: configID is required")
	}
	if key == "" {
		return "", errors.New("janus: key is required")
	}
	secrets, err := c.GetSecrets(ctx, configID)
	if err != nil {
		return "", err
	}
	v, ok := secrets[key]
	if !ok {
		return "", &APIError{Status: http.StatusNotFound, Code: "not_found", Message: "secret key not found"}
	}
	return v, nil
}

// Refresh evicts the cached secrets for a config so the next read re-fetches
// from the server. If configID is "", the entire cache is cleared.
func (c *Client) Refresh(configID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if configID == "" {
		c.cache = make(map[string]cacheEntry)
		return
	}
	delete(c.cache, configID)
}

// batchRevealResponse mirrors the value-bearing shape of
// GET /v1/configs/{cid}/secrets?reveal=true.
type batchRevealResponse struct {
	Version int               `json:"version"`
	Secrets map[string]string `json:"secrets"`
}

func (c *Client) fetchSecrets(ctx context.Context, configID string) (map[string]string, error) {
	path := fmt.Sprintf("/v1/configs/%s/secrets?reveal=true", url.PathEscape(configID))
	var resp batchRevealResponse
	if err := c.do(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	if resp.Secrets == nil {
		resp.Secrets = map[string]string{}
	}
	return resp.Secrets, nil
}

// do performs an HTTP request against the Janus API, adding the bearer token,
// decoding a JSON response into out (if non-nil), and translating error
// responses into typed errors.
func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var reqBody io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("janus: marshal request: %w", err)
		}
		reqBody = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return fmt.Errorf("janus: build request: %w", err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("janus: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return parseAPIError(resp)
	}

	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("janus: decode response: %w", err)
		}
	}
	return nil
}

// errorEnvelope mirrors {"error":{"code","message"}}.
type errorEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func parseAPIError(resp *http.Response) error {
	apiErr := &APIError{Status: resp.StatusCode}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	var env errorEnvelope
	if len(data) > 0 && json.Unmarshal(data, &env) == nil {
		apiErr.Code = env.Error.Code
		apiErr.Message = env.Error.Message
	}
	return apiErr
}

func copyMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
