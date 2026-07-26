package janus

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// Lease is a dynamic database credential lease issued by Janus. The Password
// is returned exactly once, at issue time, and is never persisted or audited
// in plaintext by the server; the SDK likewise holds it only in memory and
// never logs it. Renew and Revoke operate on this lease's ID.
//
// ID, Username and Password are immutable after issue and safe to read from
// any goroutine. ExpiresAt is mutated by Renew and by a background renewer:
// while a Renewer (see StartAutoRenew) or RunWithDynamic is active, read the
// expiry with Expiry, not from the field, or the read races the renewer.
type Lease struct {
	ID       string `json:"lease_id"`
	Username string `json:"username"`
	Password string `json:"password"`

	// ExpiresAt is the lease expiry. Prefer Expiry() — see the type doc.
	ExpiresAt time.Time `json:"expires_at"`

	client *Client

	// mu guards the mutable expiry state so a background renewer and a reader
	// calling Expiry/MaxExpiry never race.
	mu           sync.Mutex
	expiresAt    time.Time
	maxExpiresAt time.Time
}

// Expiry returns the lease's current expiry. Unlike reading the ExpiresAt
// field, it is safe to call while a background renewer is running.
func (l *Lease) Expiry() time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.expiresAt
}

// MaxExpiry returns the hard ceiling past which the server will not extend this
// lease, or the zero time if it is not known yet. Janus only reports it on a
// renew response, so it is zero until the first successful renew.
func (l *Lease) MaxExpiry() time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.maxExpiresAt
}

// leaseView mirrors the DynamicLease metadata shape (no password) returned by
// the renew endpoint.
type leaseView struct {
	ID         string    `json:"id"`
	RoleID     string    `json:"role_id"`
	Status     string    `json:"status"`
	Username   string    `json:"db_username"`
	ExpiresAt  time.Time `json:"expires_at"`
	MaxExpires time.Time `json:"max_expires_at"`
}

// IssueDynamic issues a new dynamic credential lease for the given dynamic role
// ID (POST /v1/dynamic/roles/{id}/creds). The returned Lease carries the
// one-time password; store it in memory only.
//
// Note: roleID identifies a dynamic role, not a config. Dynamic roles are
// authored via the admin API; see docs/guides/go-sdk.md.
func (c *Client) IssueDynamic(ctx context.Context, roleID string) (*Lease, error) {
	if roleID == "" {
		return nil, errors.New("janus: roleID is required")
	}
	path := fmt.Sprintf("/v1/dynamic/roles/%s/creds", url.PathEscape(roleID))
	var l Lease
	if err := c.do(ctx, http.MethodPost, path, nil, &l); err != nil {
		return nil, err
	}
	l.client = c
	l.expiresAt = l.ExpiresAt
	return &l, nil
}

// Renew extends the lease's expiry (capped server-side at the role's max TTL)
// and updates the lease's ExpiresAt. It does not change the password. Returns
// an APIError wrapping the server response on failure (e.g. 409 when the lease
// is no longer active).
func (l *Lease) Renew(ctx context.Context) error {
	_, err := l.renewView(ctx)
	return err
}

// renewView performs the renew and returns the server's lease view, so callers
// that care (the background renewer) can see max_expires_at. It updates the
// lease's expiry state under the lease mutex.
func (l *Lease) renewView(ctx context.Context) (leaseView, error) {
	if l.client == nil {
		return leaseView{}, errors.New("janus: lease not bound to a client")
	}
	if l.ID == "" {
		return leaseView{}, errors.New("janus: lease has no ID")
	}
	path := fmt.Sprintf("/v1/dynamic/leases/%s/renew", url.PathEscape(l.ID))
	var v leaseView
	if err := l.client.do(ctx, http.MethodPost, path, nil, &v); err != nil {
		return leaseView{}, err
	}
	l.mu.Lock()
	if !v.ExpiresAt.IsZero() {
		l.ExpiresAt = v.ExpiresAt
		l.expiresAt = v.ExpiresAt
	}
	if !v.MaxExpires.IsZero() {
		l.maxExpiresAt = v.MaxExpires
	}
	l.mu.Unlock()
	return v, nil
}

// Revoke revokes the lease immediately (drops the underlying database role).
// After a successful revoke the credentials are no longer valid.
func (l *Lease) Revoke(ctx context.Context) error {
	if l.client == nil {
		return errors.New("janus: lease not bound to a client")
	}
	if l.ID == "" {
		return errors.New("janus: lease has no ID")
	}
	path := fmt.Sprintf("/v1/dynamic/leases/%s/revoke", url.PathEscape(l.ID))
	return l.client.do(ctx, http.MethodPost, path, nil, nil)
}
