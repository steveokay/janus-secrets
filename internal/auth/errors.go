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
	// ErrInvalidOIDCState is returned when the OIDC callback state is
	// unknown, expired, or already consumed.
	ErrInvalidOIDCState = errors.New("auth: invalid or expired oidc state")
	// ErrOIDCExchange covers code exchange and ID token verification
	// failures (bad code, signature, issuer, audience, or nonce mismatch).
	ErrOIDCExchange = errors.New("auth: oidc token exchange or verification failed")
	// ErrOIDCDenied is returned when the provider denies or the user
	// declines the login.
	ErrOIDCDenied = errors.New("auth: oidc login denied")
)
