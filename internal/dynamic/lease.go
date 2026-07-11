package dynamic

import (
	"context"
	"strings"
	"time"

	"github.com/steveokay/janus-secrets/internal/audit"
	"github.com/steveokay/janus-secrets/internal/store"
)

// revoke drops the lease's DB role and marks it with the given terminal status
// ('revoked' or 'expired'). On failure it records 'revoke_failed' for retry and
// returns the error. Loads the owning role/project to decrypt the admin DSN.
func (s *Service) revoke(ctx context.Context, l *store.DynamicLease, terminal string) error {
	role, err := s.roles.Get(ctx, l.RoleID)
	if err != nil {
		return mapStoreErr(err)
	}
	proj, err := s.projects.Get(ctx, l.ProjectID)
	if err != nil {
		return mapStoreErr(err)
	}
	cfg, err := s.openConfig(proj, role) // ErrSealed while sealed
	if err != nil {
		return err
	}
	stmts := cfg.RevocationStatements
	if strings.TrimSpace(stmts) == "" {
		stmts = `DROP ROLE IF EXISTS "{{name}}";`
	}
	sql, err := interpolate(stmts, l.DBUsername, "", l.ExpiresAt)
	if err != nil {
		_ = s.leases.MarkRevokeFailed(ctx, l.ID, "invalid config")
		return err
	}
	if err := runStatements(ctx, cfg.AdminDSN, sql); err != nil {
		_ = s.leases.MarkRevokeFailed(ctx, l.ID, sanitize(err))
		return err
	}
	if err := s.leases.MarkRevoked(ctx, l.ID, terminal, s.now()); err != nil {
		return mapStoreErr(err)
	}
	return nil
}

// RevokeLease revokes a single lease by id. Idempotent for already-terminal
// leases. Returns ErrSealed while the server is sealed (the admin DSN cannot be
// decrypted).
func (s *Service) RevokeLease(ctx context.Context, id string) error {
	l, err := s.leases.Get(ctx, id)
	if err != nil {
		return mapStoreErr(err)
	}
	if l.Status == "revoked" || l.Status == "expired" {
		return nil // idempotent
	}
	if s.kr.Sealed() {
		return ErrSealed
	}
	if err := s.revoke(ctx, l, "revoked"); err != nil {
		return err
	}
	s.recordLease(ctx, l, "dynamic.lease.revoke")
	return nil
}

// RenewLease extends an active lease's expiry by the role's default TTL, capped
// at the lease's max expiry. It runs the role's renew statements (default ALTER
// ROLE ... VALID UNTIL) so Postgres honours the new expiry, since a VALID UNTIL
// set at creation is enforced regardless of our bookkeeping.
func (s *Service) RenewLease(ctx context.Context, id string) (LeaseView, error) {
	l, err := s.leases.Get(ctx, id)
	if err != nil {
		return LeaseView{}, mapStoreErr(err)
	}
	if l.Status != "active" {
		return LeaseView{}, ErrNotRenewable
	}
	if s.kr.Sealed() {
		return LeaseView{}, ErrSealed
	}
	role, err := s.roles.Get(ctx, l.RoleID)
	if err != nil {
		return LeaseView{}, mapStoreErr(err)
	}
	proj, err := s.projects.Get(ctx, l.ProjectID)
	if err != nil {
		return LeaseView{}, mapStoreErr(err)
	}
	cfg, err := s.openConfig(proj, role)
	if err != nil {
		return LeaseView{}, err
	}
	newExpiry := s.now().Add(time.Duration(role.DefaultTTLSeconds) * time.Second)
	if newExpiry.After(l.MaxExpiresAt) {
		newExpiry = l.MaxExpiresAt
	}
	stmts := cfg.RenewStatements
	if strings.TrimSpace(stmts) == "" {
		stmts = `ALTER ROLE "{{name}}" VALID UNTIL '{{expiration}}';`
	}
	sql, err := interpolate(stmts, l.DBUsername, "", newExpiry)
	if err != nil {
		return LeaseView{}, err
	}
	if err := runStatements(ctx, cfg.AdminDSN, sql); err != nil {
		return LeaseView{}, err
	}
	if err := s.leases.Renew(ctx, id, newExpiry, s.now()); err != nil {
		return LeaseView{}, mapStoreErr(err)
	}
	s.recordLease(ctx, l, "dynamic.lease.renew")
	updated, err := s.leases.Get(ctx, id)
	if err != nil {
		return LeaseView{}, mapStoreErr(err)
	}
	return leaseView(updated), nil
}

// recordLease writes a value-free audit event for a lease action. Never
// includes the password. System actor (scheduler/API both attribute here).
func (s *Service) recordLease(ctx context.Context, l *store.DynamicLease, action string) {
	if s.audit == nil {
		return
	}
	err := s.audit.Record(ctx, audit.Event{
		Actor:    audit.Actor{Kind: "system", Name: "dynamic:" + l.RoleID},
		Action:   action,
		Resource: "dynamic/roles/" + l.RoleID + "/leases/" + l.ID,
		Result:   "success",
	})
	if err != nil {
		s.logger.Warn("dynamic audit write failed", "lease", l.ID, "err", err)
	}
}
