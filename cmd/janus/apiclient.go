package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// apiClient issues authenticated JSON requests to the /v1 API.
type apiClient struct {
	address string
	cred    credential
	hc      *http.Client
}

// newAPIClient resolves address + credential from flags/env/stored state.
func newAPIClient(flagAddr, flagToken string) (*apiClient, error) {
	cred, err := resolveCredential(flagToken)
	if err != nil {
		return nil, err
	}
	return &apiClient{
		address: resolveAddress(flagAddr),
		cred:    cred,
		hc:      &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// call issues method+path with optional JSON in/out. Non-2xx decodes the
// {"error":{...}} envelope and returns a rewritten, user-facing error.
func (c *apiClient) call(method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.address+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	switch {
	case c.cred.Bearer != "":
		req.Header.Set("Authorization", "Bearer "+c.cred.Bearer)
	case c.cred.Cookie != "":
		req.AddCookie(&http.Cookie{Name: "janus_session", Value: c.cred.Cookie})
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return rewriteAPIError(decodeAPIError(resp))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// decodeAPIError reads the error envelope into an *apiError (type from client.go).
func decodeAPIError(resp *http.Response) *apiError {
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&env)
	if env.Error.Message == "" {
		env.Error.Message = http.StatusText(resp.StatusCode)
	}
	if env.Error.Code == "" {
		env.Error.Code = "unknown"
	}
	return &apiError{Status: resp.StatusCode, Code: env.Error.Code, Message: env.Error.Message}
}

// rewriteAPIError turns common auth/seal statuses into actionable CLI guidance.
func rewriteAPIError(e *apiError) error {
	switch e.Status {
	case http.StatusUnauthorized:
		return fmt.Errorf("not authenticated — run `janus login` (server said: %s)", e.Message)
	case http.StatusForbidden:
		return fmt.Errorf("access denied: %s", e.Message)
	case http.StatusServiceUnavailable:
		return fmt.Errorf("server is sealed — unseal it first (%s)", e.Message)
	}
	return e
}
