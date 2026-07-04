package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// defaultAddress resolves the sys-API address: --address flag > JANUS_ADDR >
// http://127.0.0.1:8200.
func defaultAddress() string {
	if v := os.Getenv("JANUS_ADDR"); v != "" {
		return v
	}
	return "http://127.0.0.1:8200"
}

// apiError is a decoded {"error":{...}} envelope.
type apiError struct {
	Status  int
	Code    string
	Message string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("%s (%s, HTTP %d)", e.Message, e.Code, e.Status)
}

// sysCall issues a JSON request to the sys API and decodes the response into
// out (if non-nil). Non-2xx responses are returned as *apiError.
func sysCall(address, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, address+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	hc := &http.Client{Timeout: 30 * time.Second}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		var env struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&env)
		return &apiError{Status: resp.StatusCode, Code: env.Error.Code, Message: env.Error.Message}
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
