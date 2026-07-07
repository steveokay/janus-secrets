package store

import (
	"context"
	"testing"
)

// seedProjectEnv inserts a project + a single "prod" environment, returning
// their ids as text.
func seedProjectEnv(t *testing.T, slug, name string) (projID, envID string) {
	t.Helper()
	ctx := context.Background()
	if err := testStore.pool.QueryRow(ctx,
		`INSERT INTO projects (slug, name, wrapped_kek) VALUES ($1, $2, '\x00'::bytea) RETURNING id::text`,
		slug, name).Scan(&projID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if err := testStore.pool.QueryRow(ctx,
		`INSERT INTO environments (project_id, slug, name) VALUES ($1, 'prod', 'prod') RETURNING id::text`,
		projID).Scan(&envID); err != nil {
		t.Fatalf("seed environment: %v", err)
	}
	return projID, envID
}

func seedConfig(t *testing.T, envID, name string) (configID string) {
	t.Helper()
	if err := testStore.pool.QueryRow(context.Background(),
		`INSERT INTO configs (environment_id, name) VALUES ($1, $2) RETURNING id::text`,
		envID, name).Scan(&configID); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	return configID
}

func seedUser(t *testing.T, email string) (userID string) {
	t.Helper()
	if err := testStore.pool.QueryRow(context.Background(),
		`INSERT INTO users (email) VALUES ($1) RETURNING id::text`, email).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return userID
}

func seedToken(t *testing.T, userID, name string, hmac []byte, scopeID string) (tokenID string) {
	t.Helper()
	if err := testStore.pool.QueryRow(context.Background(),
		`INSERT INTO service_tokens (name, token_hmac, created_by, scope_kind, scope_id, access)
		 VALUES ($1, $2, $3, 'config', $4, 'read') RETURNING id::text`,
		name, hmac, userID, scopeID).Scan(&tokenID); err != nil {
		t.Fatalf("seed token: %v", err)
	}
	return tokenID
}

// seedReveal inserts one secret.reveal audit row against configID. age is a
// Postgres interval string subtracted from now() (e.g. "10 minutes",
// "30 hours"). For token reveals pass actorKind="service_token" and the token
// uuid as actorID; for user reveals pass actorKind="user".
func seedReveal(t *testing.T, seq int64, configID, actorKind, actorID, result, age string) {
	t.Helper()
	_, err := testStore.pool.Exec(context.Background(),
		`INSERT INTO audit_events
		   (seq, occurred_at, actor_kind, actor_id, actor_name, action, resource, result, ip, prev_hash, hash)
		 VALUES ($1, now() - $2::interval, $3, $4, 'seed', 'secret.reveal',
		         'configs/' || $5 || '/secrets', $6, '1.2.3.4', '\x00'::bytea, '\x00'::bytea)`,
		seq, age, actorKind, nullableActor(actorID), configID, result)
	if err != nil {
		t.Fatalf("seed reveal: %v", err)
	}
}

// nullableActor turns "" into a real NULL actor_id (user reveals may omit it),
// otherwise passes the id through.
func nullableActor(id string) any {
	if id == "" {
		return nil
	}
	return id
}

func TestReads24h(t *testing.T) {
	if testStore == nil {
		t.Skip("postgres/docker not available")
	}
	resetDB(t)
	ctx := context.Background()
	repo := NewMetricsRepo(testStore)

	// Project A: configs c1, c2. Project B: config c3.
	projA, envA := seedProjectEnv(t, "proj-a", "Project A")
	projB, envB := seedProjectEnv(t, "proj-b", "Project B")
	c1 := seedConfig(t, envA, "root")
	c2 := seedConfig(t, envA, "branch")
	c3 := seedConfig(t, envB, "root")

	uid := seedUser(t, "seed@janus.test")
	t1 := seedToken(t, uid, "ci-a", []byte("hmac-token-1-000000000000000000"), c1)
	t2 := seedToken(t, uid, "ci-b", []byte("hmac-token-2-000000000000000000"), c3)

	var seq int64
	next := func() int64 { seq++; return seq }

	// Fresh successful reveals in window.
	// c1: 2 user + 2 token(t1) = 4
	seedReveal(t, next(), c1, "user", "", "success", "10 minutes")
	seedReveal(t, next(), c1, "user", "", "success", "20 minutes")
	seedReveal(t, next(), c1, "service_token", t1, "success", "5 minutes")
	seedReveal(t, next(), c1, "service_token", t1, "success", "6 minutes")
	// c2: 1 user = 1
	seedReveal(t, next(), c2, "user", "", "success", "15 minutes")
	// c3 (project B): 1 user + 1 token(t2) = 2
	seedReveal(t, next(), c3, "user", "", "success", "8 minutes")
	seedReveal(t, next(), c3, "service_token", t2, "success", "9 minutes")

	// Excluded rows on c1: one stale (outside 24h) and one denied.
	seedReveal(t, next(), c1, "user", "", "success", "30 hours")
	seedReveal(t, next(), c1, "user", "", "denied", "3 minutes")

	// --- Instance-wide ---
	inst, err := repo.Reads24h(ctx, nil)
	if err != nil {
		t.Fatalf("Reads24h(nil): %v", err)
	}
	if inst.Total != 7 {
		t.Fatalf("instance Total = %d, want 7", inst.Total)
	}
	if len(inst.TopConfigs) == 0 || inst.TopConfigs[0].ConfigID != c1 ||
		inst.TopConfigs[0].Reads != 4 || inst.TopConfigs[0].ProjectName != "Project A" {
		t.Fatalf("instance top config = %+v, want c1(%s) reads=4 project=Project A", inst.TopConfigs, c1)
	}
	// c2 and c3 both present (c3 reads=2 ranks above c2 reads=1).
	if len(inst.TopConfigs) != 3 {
		t.Fatalf("instance TopConfigs len = %d, want 3 (%+v)", len(inst.TopConfigs), inst.TopConfigs)
	}
	if inst.TopConfigs[1].ConfigID != c3 || inst.TopConfigs[1].Reads != 2 {
		t.Fatalf("instance 2nd config = %+v, want c3 reads=2", inst.TopConfigs[1])
	}
	if len(inst.TopTokens) == 0 || inst.TopTokens[0].TokenID != t1 ||
		inst.TopTokens[0].Reads != 2 || inst.TopTokens[0].TokenName != "ci-a" {
		t.Fatalf("instance top token = %+v, want t1(%s) reads=2 name=ci-a", inst.TopTokens, t1)
	}
	if len(inst.TopTokens) != 2 {
		t.Fatalf("instance TopTokens len = %d, want 2 (%+v)", len(inst.TopTokens), inst.TopTokens)
	}

	// --- Project A scoped: excludes c3 / t2 (project B) ---
	scoped, err := repo.Reads24h(ctx, &projA)
	if err != nil {
		t.Fatalf("Reads24h(&projA): %v", err)
	}
	if scoped.Total != 5 {
		t.Fatalf("project A Total = %d, want 5", scoped.Total)
	}
	if len(scoped.TopConfigs) != 2 {
		t.Fatalf("project A TopConfigs len = %d, want 2 (%+v)", len(scoped.TopConfigs), scoped.TopConfigs)
	}
	if scoped.TopConfigs[0].ConfigID != c1 || scoped.TopConfigs[0].Reads != 4 {
		t.Fatalf("project A top config = %+v, want c1 reads=4", scoped.TopConfigs[0])
	}
	for _, cr := range scoped.TopConfigs {
		if cr.ConfigID == c3 {
			t.Fatalf("project A leaked config c3 from project B: %+v", scoped.TopConfigs)
		}
	}
	if len(scoped.TopTokens) != 1 || scoped.TopTokens[0].TokenID != t1 || scoped.TopTokens[0].Reads != 2 {
		t.Fatalf("project A top tokens = %+v, want only t1 reads=2", scoped.TopTokens)
	}

	_ = projB // referenced via seeded rows only
}
