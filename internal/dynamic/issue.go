package dynamic

import (
	"context"
	"time"

	"github.com/steveokay/janus-secrets/internal/audit"
	"github.com/steveokay/janus-secrets/internal/store"
)

// IssueCreds mints a short-lived Postgres role from the dynamic role's creation
// template and records a lease. Crash-safe persist->apply->commit: the lease is
// persisted 'creating' BEFORE any DB change, so a crash after CREATE ROLE leaves
// a row the lease manager reclaims (the caller received no password). The
// generated password is returned once and never persisted.
func (s *Service) IssueCreds(ctx context.Context, roleID, createdBy string) (Creds, error) {
	if s.kr.Sealed() {
		return Creds{}, ErrSealed
	}
	role, err := s.roles.Get(ctx, roleID)
	if err != nil {
		return Creds{}, mapStoreErr(err)
	}
	proj, err := s.projects.Get(ctx, role.ProjectID)
	if err != nil {
		return Creds{}, mapStoreErr(err)
	}
	cfg, err := s.openConfig(proj, role)
	if err != nil {
		return Creds{}, err
	}

	username, err := generateUsername(role.Name)
	if err != nil {
		return Creds{}, err
	}
	password, err := generatePassword(defaultPasswdLen)
	if err != nil {
		return Creds{}, err
	}
	now := s.now()
	expires := now.Add(time.Duration(role.DefaultTTLSeconds) * time.Second)
	maxExpires := now.Add(time.Duration(role.MaxTTLSeconds) * time.Second)

	id, err := s.st.NewID(ctx)
	if err != nil {
		return Creds{}, err
	}
	lease := &store.DynamicLease{
		ID: id, RoleID: role.ID, ProjectID: role.ProjectID, DBUsername: username,
		ExpiresAt: expires, MaxExpiresAt: maxExpires, CreatedBy: createdBy,
	}
	// Reserve.
	if err := s.leases.Create(ctx, lease); err != nil {
		return Creds{}, mapStoreErr(err)
	}
	// Apply.
	sql, err := interpolate(cfg.CreationStatements, username, password, expires)
	if err != nil {
		_ = s.revoke(ctx, lease, "revoked") // clean up the reserved row
		return Creds{}, err
	}
	if err := runStatements(ctx, cfg.AdminDSN, sql); err != nil {
		_ = s.revoke(ctx, lease, "revoked") // DROP ROLE IF EXISTS is idempotent
		return Creds{}, err
	}
	// Commit.
	if err := s.leases.Activate(ctx, lease.ID); err != nil {
		return Creds{}, mapStoreErr(err)
	}
	s.recordIssue(ctx, role, lease)
	return Creds{LeaseID: lease.ID, Username: username, Password: password, ExpiresAt: expires}, nil
}

// recordIssue audits a credential issue (role name + lease id + db_username;
// NEVER the password).
func (s *Service) recordIssue(ctx context.Context, role *store.DynamicRole, lease *store.DynamicLease) {
	if s.audit == nil {
		return
	}
	err := s.audit.Record(ctx, audit.Event{
		Actor:    audit.Actor{Kind: "system", Name: "dynamic:" + role.ID},
		Action:   "dynamic.creds.issue",
		Resource: "dynamic/roles/" + role.ID + "/leases/" + lease.ID,
		Detail:   "db_user=" + lease.DBUsername,
		Result:   "success",
	})
	if err != nil {
		s.logger.Warn("dynamic audit write failed", "lease", lease.ID, "err", err)
	}
}
