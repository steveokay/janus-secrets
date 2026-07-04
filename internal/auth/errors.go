package auth

import "errors"

var (
	// ErrInvalidCredentials covers wrong password, unknown user, and disabled
	// user indistinguishably — no enumeration oracle.
	ErrInvalidCredentials = errors.New("auth: invalid credentials")
	// ErrUnauthenticated is returned for absent/invalid/expired/revoked
	// sessions and tokens.
	ErrUnauthenticated = errors.New("auth: unauthenticated")
	// ErrValidation is returned for invalid input (bad scope kind, empty name).
	ErrValidation = errors.New("auth: invalid input")
	// ErrNotFound is returned when a referenced entity does not exist.
	ErrNotFound = errors.New("auth: not found")
)
