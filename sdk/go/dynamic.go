package janus

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// Lease is a dynamic database credential lease issued by Janus. The Password
// is returned exactly once, at issue time, and is never persisted or audited
// in plaintext by the server; the SDK likewise holds it only in memory and
// never logs it. Renew and Revoke operate on this lease's ID.
type Lease struct {
	ID        string    `json:"lease_id"`
	Username  string    `json:"username"`
	Password  string    `json:"password"`
	ExpiresAt time.Time `json:"expires_at"`

	client *Client
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
	return &l, nil
}

// Renew extends the lease's expiry (capped server-side at the role's max TTL)
// and updates the lease's ExpiresAt. It does not change the password. Returns
// an APIError wrapping the server response on failure (e.g. 409 when the lease
// is no longer active).
func (l *Lease) Renew(ctx context.Context) error {
	if l.client == nil {
		return errors.New("janus: lease not bound to a client")
	}
	if l.ID == "" {
		return errors.New("janus: lease has no ID")
	}
	path := fmt.Sprintf("/v1/dynamic/leases/%s/renew", url.PathEscape(l.ID))
	var v leaseView
	if err := l.client.do(ctx, http.MethodPost, path, nil, &v); err != nil {
		return err
	}
	if !v.ExpiresAt.IsZero() {
		l.ExpiresAt = v.ExpiresAt
	}
	return nil
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
