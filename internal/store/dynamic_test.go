package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// newDynamicRole builds a minimal valid DynamicRole for the given project/config,
// with a fresh id and default encrypted-blob placeholders.
func newDynamicRole(t *testing.T, s *Store, projectID, configID, name string) *DynamicRole {
	t.Helper()
	ctx := context.Background()
	id, err := s.NewID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return &DynamicRole{
		ID:                  id,
		ProjectID:           projectID,
		ConfigID:            configID,
		Name:                name,
		DefaultTTLSeconds:   3600,
		MaxTTLSeconds:       86400,
		ConfigCT:            []byte("ct-v1"),
		ConfigNonce:         []byte("nonce-v1"),
		ConfigWrappedDEK:    []byte("wrapped-v1"),
		ConfigDEKKEKVersion: 1,
		CreatedBy:           "user:tester",
	}
}

// newDynamicLease builds a minimal valid DynamicLease for the given role/project,
// with a fresh id. issuedIn positions expiry relative to now (negative = already
// expired).
func newDynamicLease(t *testing.T, s *Store, roleID, projectID, username string, expiresIn time.Duration) *DynamicLease {
	t.Helper()
	ctx := context.Background()
	id, err := s.NewID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	return &DynamicLease{
		ID:           id,
		RoleID:       roleID,
		ProjectID:    projectID,
		DBUsername:   username,
		ExpiresAt:    now.Add(expiresIn),
		MaxExpiresAt: now.Add(24 * time.Hour),
		CreatedBy:    "user:tester",
	}
}

func TestDynamicRoleLifecycle(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	roleRepo := NewDynamicRoleRepo(s)

	projectID, _, configID := mkConfig(t, s, "prod")

	// Create -> Get round trip.
	in := newDynamicRole(t, s, projectID, configID, "readonly")
	got, err := roleRepo.Create(ctx, in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.ID != in.ID || got.ProjectID != projectID || got.ConfigID != configID {
		t.Fatalf("ids mismatch: %+v", got)
	}
	if got.Name != "readonly" || got.DefaultTTLSeconds != 3600 || got.MaxTTLSeconds != 86400 {
		t.Fatalf("fields mismatch: %+v", got)
	}
	if string(got.ConfigCT) != "ct-v1" || string(got.ConfigNonce) != "nonce-v1" ||
		string(got.ConfigWrappedDEK) != "wrapped-v1" || got.ConfigDEKKEKVersion != 1 {
		t.Fatalf("config blob mismatch: %+v", got)
	}
	if got.CreatedBy != "user:tester" {
		t.Fatalf("want created_by preserved, got %q", got.CreatedBy)
	}

	// Duplicate (config_id, name) -> ErrAlreadyExists.
	dup := newDynamicRole(t, s, projectID, configID, "readonly")
	if _, err := roleRepo.Create(ctx, dup); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("dup: want ErrAlreadyExists, got %v", err)
	}

	// A second, distinct role for ListByConfig ordering (newest first).
	second := newDynamicRole(t, s, projectID, configID, "readwrite")
	if _, err := roleRepo.Create(ctx, second); err != nil {
		t.Fatalf("Create second: %v", err)
	}
	list, err := roleRepo.ListByConfig(ctx, configID)
	if err != nil {
		t.Fatalf("ListByConfig: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 roles, got %d", len(list))
	}
	if list[0].Name != "readwrite" || list[1].Name != "readonly" {
		t.Fatalf("want newest first (readwrite, readonly), got (%s, %s)", list[0].Name, list[1].Name)
	}

	// Update: TTLs + config blob rotation.
	newDefault := int64(7200)
	newMax := int64(172800)
	newVer := 2
	if err := roleRepo.Update(ctx, got.ID, &newDefault, &newMax,
		[]byte("ct-v2"), []byte("nonce-v2"), []byte("wrapped-v2"), &newVer); err != nil {
		t.Fatalf("Update: %v", err)
	}
	afterUpdate, err := roleRepo.Get(ctx, got.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterUpdate.DefaultTTLSeconds != 7200 || afterUpdate.MaxTTLSeconds != 172800 {
		t.Fatalf("after TTL update: %+v", afterUpdate)
	}
	if string(afterUpdate.ConfigCT) != "ct-v2" || afterUpdate.ConfigDEKKEKVersion != 2 {
		t.Fatalf("after blob update: %+v", afterUpdate)
	}
	// nil config args are a no-op — blob unchanged, TTLs unchanged.
	if err := roleRepo.Update(ctx, got.ID, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("no-op Update: %v", err)
	}
	afterNoop, err := roleRepo.Get(ctx, got.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterNoop.ConfigCT) != "ct-v2" || afterNoop.DefaultTTLSeconds != 7200 {
		t.Fatalf("no-op Update should leave fields unchanged: %+v", afterNoop)
	}

	// Delete then Get -> ErrNotFound.
	if err := roleRepo.Delete(ctx, got.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := roleRepo.Get(ctx, got.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: want ErrNotFound, got %v", err)
	}
	if err := roleRepo.Delete(ctx, got.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("double delete: want ErrNotFound, got %v", err)
	}
}

func TestDynamicLeaseLifecycle(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	roleRepo := NewDynamicRoleRepo(s)
	leaseRepo := NewDynamicLeaseRepo(s)

	projectID, _, configID := mkConfig(t, s, "prod")

	role, err := roleRepo.Create(ctx, newDynamicRole(t, s, projectID, configID, "readonly"))
	if err != nil {
		t.Fatalf("Create role: %v", err)
	}

	// Create a lease -> defaults to status 'creating'.
	lease := newDynamicLease(t, s, role.ID, projectID, "janus_dyn_abc", time.Hour)
	if err := leaseRepo.Create(ctx, lease); err != nil {
		t.Fatalf("Create lease: %v", err)
	}
	created, err := leaseRepo.Get(ctx, lease.ID)
	if err != nil {
		t.Fatalf("Get lease: %v", err)
	}
	if created.Status != "creating" {
		t.Fatalf("want status creating, got %q", created.Status)
	}
	if created.RoleID != role.ID || created.ProjectID != projectID || created.DBUsername != "janus_dyn_abc" {
		t.Fatalf("lease fields mismatch: %+v", created)
	}
	if created.RenewedAt != nil || created.RevokedAt != nil || created.LastError != nil {
		t.Fatalf("want nil renewed/revoked/last_error, got %+v", created)
	}

	// ListByRole finds it.
	byRole, err := leaseRepo.ListByRole(ctx, role.ID)
	if err != nil {
		t.Fatalf("ListByRole: %v", err)
	}
	if len(byRole) != 1 || byRole[0].ID != lease.ID {
		t.Fatalf("ListByRole mismatch: %+v", byRole)
	}

	now := time.Now().UTC()

	// ClaimDue: a young 'creating' lease (created_at == now) is NOT due when the
	// grace cutoff is in the past. Its expiry is in the future, so it also isn't
	// an expired-active lease.
	creatingBefore := now.Add(-time.Minute)
	due, err := leaseRepo.ClaimDue(ctx, now, creatingBefore, 100)
	if err != nil {
		t.Fatalf("ClaimDue (young creating): %v", err)
	}
	for _, l := range due {
		if l.ID == lease.ID {
			t.Fatalf("ClaimDue must not return young creating lease: %+v", l)
		}
	}

	// Activate flips creating -> active.
	if err := leaseRepo.Activate(ctx, lease.ID); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	activated, err := leaseRepo.Get(ctx, lease.ID)
	if err != nil {
		t.Fatal(err)
	}
	if activated.Status != "active" {
		t.Fatalf("want status active after Activate, got %q", activated.Status)
	}
	// Re-activating an already-active lease is a no-op miss (guarded by status).
	if err := leaseRepo.Activate(ctx, lease.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("re-Activate: want ErrNotFound (no matching creating row), got %v", err)
	}

	// Now create an EXPIRED active lease and confirm ClaimDue returns it.
	expired := newDynamicLease(t, s, role.ID, projectID, "janus_dyn_old", -time.Hour)
	if err := leaseRepo.Create(ctx, expired); err != nil {
		t.Fatalf("Create expired lease: %v", err)
	}
	if err := leaseRepo.Activate(ctx, expired.ID); err != nil {
		t.Fatalf("Activate expired: %v", err)
	}
	due, err = leaseRepo.ClaimDue(ctx, now, creatingBefore, 100)
	if err != nil {
		t.Fatalf("ClaimDue (expired active): %v", err)
	}
	var foundExpired bool
	for _, l := range due {
		if l.ID == expired.ID {
			foundExpired = true
		}
		if l.ID == lease.ID {
			t.Fatalf("ClaimDue must not return the not-yet-expired active lease: %+v", l)
		}
	}
	if !foundExpired {
		t.Fatalf("ClaimDue must return the expired active lease; got %d rows: %+v", len(due), due)
	}

	// MarkRevoked sets terminal status + revoked_at, clears last_error.
	revokedAt := now.Truncate(time.Second)
	if err := leaseRepo.MarkRevoked(ctx, expired.ID, "expired", revokedAt); err != nil {
		t.Fatalf("MarkRevoked: %v", err)
	}
	revoked, err := leaseRepo.Get(ctx, expired.ID)
	if err != nil {
		t.Fatal(err)
	}
	if revoked.Status != "expired" {
		t.Fatalf("want status expired, got %q", revoked.Status)
	}
	if revoked.RevokedAt == nil || !revoked.RevokedAt.Equal(revokedAt) {
		t.Fatalf("want revoked_at %v, got %v", revokedAt, revoked.RevokedAt)
	}
	if revoked.LastError != nil {
		t.Fatalf("want last_error nil after MarkRevoked, got %v", *revoked.LastError)
	}

	// A revoked (terminal) lease is no longer due.
	due, err = leaseRepo.ClaimDue(ctx, now, creatingBefore, 100)
	if err != nil {
		t.Fatalf("ClaimDue (after revoke): %v", err)
	}
	for _, l := range due {
		if l.ID == expired.ID {
			t.Fatalf("ClaimDue must not return a terminal lease: %+v", l)
		}
	}

	// MarkRevokeFailed flips to revoke_failed and records a sanitized error;
	// ClaimDue then re-selects it for retry.
	if err := leaseRepo.MarkRevokeFailed(ctx, lease.ID, "connection refused"); err != nil {
		t.Fatalf("MarkRevokeFailed: %v", err)
	}
	failed, err := leaseRepo.Get(ctx, lease.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != "revoke_failed" || failed.LastError == nil || *failed.LastError != "connection refused" {
		t.Fatalf("MarkRevokeFailed mismatch: %+v", failed)
	}
	due, err = leaseRepo.ClaimDue(ctx, now, creatingBefore, 100)
	if err != nil {
		t.Fatalf("ClaimDue (revoke_failed retry): %v", err)
	}
	var foundFailed bool
	for _, l := range due {
		if l.ID == lease.ID {
			foundFailed = true
		}
	}
	if !foundFailed {
		t.Fatalf("ClaimDue must re-select a revoke_failed lease for retry")
	}
}
