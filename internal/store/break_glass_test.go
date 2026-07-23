package store

import (
	"context"
	"testing"
	"time"
)

// seedBreakGlassUser inserts a user and returns its id (obviously-fake fixture).
func seedBreakGlassUser(t *testing.T, st *Store, email string) string {
	t.Helper()
	var id string
	// password_hash is a non-secret fake fixture (low-entropy).
	err := st.pool.QueryRow(context.Background(),
		`INSERT INTO users (email, password_hash) VALUES ($1, 'fake-hash') RETURNING id::text`,
		email).Scan(&id)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return id
}

func TestBreakGlassCreateGetActive(t *testing.T) {
	st := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	uid := seedBreakGlassUser(t, st, "bg-user@example.test")
	repo := NewBreakGlassRepo(st)

	now := time.Now()
	g, err := repo.Create(ctx, BreakGlassGrantInput{
		UserID:       uid,
		ScopeLevel:   "instance",
		ElevatedRole: "owner",
		Reason:       "prod outage — need to seal",
		ExpiresAt:    now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if g.ID == "" || g.Reason != "prod outage — need to seal" || g.ElevatedRole != "owner" {
		t.Fatalf("unexpected grant: %+v", g)
	}
	if !g.Active(now) {
		t.Fatalf("fresh grant must be active")
	}

	got, err := repo.Get(ctx, g.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != g.ID {
		t.Fatalf("get mismatch")
	}

	// ListActiveForUser returns it.
	live, err := repo.ListActiveForUser(ctx, uid, now)
	if err != nil {
		t.Fatalf("list active: %v", err)
	}
	if len(live) != 1 {
		t.Fatalf("want 1 active grant, got %d", len(live))
	}

	// Instance-wide listing sees it too.
	all, err := repo.ListActive(ctx, now)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("want 1, got %d", len(all))
	}
}

func TestBreakGlassExpiredNotActive(t *testing.T) {
	st := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	uid := seedBreakGlassUser(t, st, "bg-exp@example.test")
	repo := NewBreakGlassRepo(st)

	past := time.Now().Add(-2 * time.Hour)
	g, err := repo.Create(ctx, BreakGlassGrantInput{
		UserID:       uid,
		ScopeLevel:   "instance",
		ElevatedRole: "admin",
		Reason:       "test-expired",
		ExpiresAt:    past.Add(time.Hour), // already in the past
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	now := time.Now()
	if g.Active(now) {
		t.Fatalf("expired grant must not be active")
	}
	live, err := repo.ListActiveForUser(ctx, uid, now)
	if err != nil {
		t.Fatalf("list active: %v", err)
	}
	if len(live) != 0 {
		t.Fatalf("expired grant must not be listed, got %d", len(live))
	}
}

func TestBreakGlassRevoke(t *testing.T) {
	st := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	uid := seedBreakGlassUser(t, st, "bg-rev@example.test")
	repo := NewBreakGlassRepo(st)

	now := time.Now()
	g, err := repo.Create(ctx, BreakGlassGrantInput{
		UserID:       uid,
		ScopeLevel:   "instance",
		ElevatedRole: "admin",
		Reason:       "test-revoke",
		ExpiresAt:    now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.Revoke(ctx, g.ID, now); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	// Second revoke → ErrNotFound (no live row).
	if err := repo.Revoke(ctx, g.ID, now); err != ErrNotFound {
		t.Fatalf("double revoke want ErrNotFound, got %v", err)
	}
	live, err := repo.ListActiveForUser(ctx, uid, now)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(live) != 0 {
		t.Fatalf("revoked grant must not be active, got %d", len(live))
	}
}

func TestBreakGlassSweepExpired(t *testing.T) {
	st := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	uid := seedBreakGlassUser(t, st, "bg-sweep@example.test")
	repo := NewBreakGlassRepo(st)

	// One expired, one live.
	_, err := repo.Create(ctx, BreakGlassGrantInput{
		UserID: uid, ScopeLevel: "instance", ElevatedRole: "admin",
		Reason: "expired-one", ExpiresAt: time.Now().Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf("create expired: %v", err)
	}
	_, err = repo.Create(ctx, BreakGlassGrantInput{
		UserID: uid, ScopeLevel: "instance", ElevatedRole: "admin",
		Reason: "live-one", ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("create live: %v", err)
	}

	swept, err := repo.SweepExpired(ctx, time.Now())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(swept) != 1 || swept[0].Reason != "expired-one" {
		t.Fatalf("sweep should transition exactly the expired grant, got %+v", swept)
	}
	// Idempotent: a second sweep transitions nothing.
	swept2, err := repo.SweepExpired(ctx, time.Now())
	if err != nil {
		t.Fatalf("sweep2: %v", err)
	}
	if len(swept2) != 0 {
		t.Fatalf("second sweep should be a no-op, got %d", len(swept2))
	}
}

func TestBreakGlassScopeShapeConstraint(t *testing.T) {
	st := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	uid := seedBreakGlassUser(t, st, "bg-shape@example.test")

	// project scope with a NULL project_id violates break_glass_scope_shape.
	_, err := st.pool.Exec(ctx,
		`INSERT INTO break_glass_grants (user_id, scope_level, elevated_role, reason, expires_at)
		 VALUES ($1::uuid, 'project', 'admin', 'bad-shape', now() + interval '1 hour')`,
		uid)
	if err == nil {
		t.Fatalf("expected scope-shape constraint violation")
	}
}
