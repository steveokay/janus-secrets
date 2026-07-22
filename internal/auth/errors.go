package auth

import (
	"errors"
	"fmt"
	"time"
)

// AccountLockedError is returned by Login when a caller supplies the CORRECT
// password for an account that is currently locked out. It carries the remaining
// window so the API layer can set a Retry-After header. It is only ever revealed
// to the password-holder — a wrong password against a locked account still
// returns the byte-identical ErrInvalidCredentials (no enumeration/lock oracle).
type AccountLockedError struct {
	// RetryAfter is the remaining lock window (rounded up to whole seconds by the
	// API layer). Always > 0 when the error is returned.
	RetryAfter time.Duration
}

func (e *AccountLockedError) Error() string {
	return fmt.Sprintf("auth: account locked; retry in %s", e.RetryAfter.Round(time.Second))
}

// AsAccountLocked reports whether err is (or wraps) an *AccountLockedError and,
// if so, returns it. Convenience over errors.As at call sites.
func AsAccountLocked(err error) (*AccountLockedError, bool) {
	var e *AccountLockedError
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

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
	// ErrFederationNotConfigured is returned when a federated CI token
	// exchange is attempted but no federation config has been set.
	ErrFederationNotConfigured = errors.New("auth: federation not configured")
	// ErrFederationVerify covers ID token verification failures for a
	// federated CI identity (bad signature, issuer, or audience).
	ErrFederationVerify = errors.New("auth: federation token verification failed")
	// ErrFederationNoMatch is returned when no enabled binding's claim
	// conditions match the federated identity token.
	ErrFederationNoMatch = errors.New("auth: no federation binding matched")
	// ErrFederationAmbiguous is returned when more than one enabled binding
	// matches the federated identity token.
	ErrFederationAmbiguous = errors.New("auth: multiple federation bindings matched")
	// ErrSessionExpired is returned when a session exceeded the configured
	// inactivity window. Distinct from ErrUnauthenticated so the API can tell
	// the (previously authenticated) caller why they were logged out.
	ErrSessionExpired = errors.New("auth: session expired due to inactivity")
	// ErrTOTPRequired is returned by Login when the password is correct but the
	// user has an activated TOTP factor and supplied no second-factor code.
	ErrTOTPRequired = errors.New("auth: totp code required")
	// ErrTOTPState is returned for invalid TOTP enroll/confirm/disable states
	// (e.g. confirming without a pending enrollment, or enrolling while active).
	ErrTOTPState = errors.New("auth: invalid totp state")
)
