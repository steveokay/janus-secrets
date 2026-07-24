package janus

import (
	"errors"
	"fmt"
)

// Sentinel errors returned by the SDK. Use errors.Is to test for them; an
// APIError parsed from the server envelope wraps the appropriate sentinel so
// that, e.g., errors.Is(err, ErrNotFound) is true for a 404 response.
var (
	// ErrUnauthorized indicates the token was missing, invalid, or expired
	// (HTTP 401).
	ErrUnauthorized = errors.New("janus: unauthorized")

	// ErrNotFound indicates the requested config, key, or resource does not
	// exist or is not visible to the token (HTTP 404).
	ErrNotFound = errors.New("janus: not found")

	// ErrSealed indicates the server is sealed and cannot serve secret
	// operations until unsealed (HTTP 503, error code "sealed").
	ErrSealed = errors.New("janus: server is sealed")

	// ErrForbidden indicates the token is authenticated but not authorized for
	// the operation (HTTP 403).
	ErrForbidden = errors.New("janus: forbidden")
)

// APIError is the structured error the Janus API returns in its
// {"error":{"code","message"}} envelope. It carries the HTTP status alongside
// the server-supplied machine code and human message.
//
// APIError never contains a secret value: the server's error envelope is
// value-free by design, and this type only ever holds the code and message it
// parsed from that envelope.
type APIError struct {
	// Status is the HTTP status code of the response (e.g. 403, 404, 503).
	Status int
	// Code is the machine-readable error code (e.g. "forbidden", "sealed").
	Code string
	// Message is the human-readable message.
	Message string
}

func (e *APIError) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("janus: api error (status %d)", e.Status)
	}
	return fmt.Sprintf("janus: api error %s (status %d): %s", e.Code, e.Status, e.Message)
}

// Unwrap maps the API error onto a sentinel where one applies, so callers can
// use errors.Is against the exported sentinels. Mapping is by HTTP status
// (with the "sealed" code preferred for 503), which keeps behaviour stable
// even if the server refines its code strings.
func (e *APIError) Unwrap() error {
	switch {
	case e.Status == 401:
		return ErrUnauthorized
	case e.Status == 403:
		return ErrForbidden
	case e.Status == 404:
		return ErrNotFound
	case e.Status == 503, e.Code == "sealed":
		return ErrSealed
	default:
		return nil
	}
}
