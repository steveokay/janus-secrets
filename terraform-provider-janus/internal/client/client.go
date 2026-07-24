// Package client is a minimal, dependency-free HTTP client for the Janus
// secrets-manager /v1 REST API, used by the Terraform provider.
//
// It intentionally does NOT import the Janus server's internal packages nor
// the sdk/go client; it re-implements just the request/response shapes the
// provider needs (mirroring docs/openapi.yaml) so the provider module stays
// self-contained. Secret values pass through Value fields; the client never
// logs them.
package client

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
)

// maxErrorBody bounds how much of an error response body is read when parsing
// the {"error":{code,message}} envelope.
const maxErrorBody = 1 << 16 // 64 KiB

// Client talks to a Janus instance over HTTP using a bearer service token.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// New constructs a Client. baseURL is the Janus base URL (the "/v1" prefix is
// added per-call). httpClient must be non-nil (the provider configures it with
// a timeout).
func New(baseURL, token string, httpClient *http.Client) (*Client, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, errors.New("janus: endpoint is required")
	}
	if _, err := url.Parse(baseURL); err != nil {
		return nil, fmt.Errorf("janus: invalid endpoint: %w", err)
	}
	if httpClient == nil {
		return nil, errors.New("janus: http client is required")
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    httpClient,
	}, nil
}

// APIError is the structured error parsed from the Janus error envelope. It is
// value-free by design (only the machine code + human message).
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("janus: api error (status %d)", e.Status)
	}
	return fmt.Sprintf("janus: %s (status %d): %s", e.Code, e.Status, e.Message)
}

// IsNotFound reports whether err is an APIError with HTTP 404, used by resource
// Read to drop a deleted resource from state (drift).
func IsNotFound(err error) bool {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Status == http.StatusNotFound
	}
	return false
}

type errorEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// do performs an HTTP request, adds the bearer token, decodes a JSON success
// body into out (if non-nil), and maps error responses to *APIError.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
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
