package store

import (
	"context"
	"testing"
)

func TestDestroyProjectCascades(t *testing.T) {
	if testStore == nil {
		t.Skip("postgres/docker not available")
	}
	resetDB(t)
	ctx := context.Background()
	pr := NewProjectRepo(testStore)
	er := NewEnvironmentRepo(testStore)
	cr := NewConfigRepo(testStore)

	id, err := testStore.NewID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, err := pr.Create(ctx, id, "web", "Web", []byte("dummy-wrapped-kek"), 1)
	if err != nil {
		t.Fatal(err)
	}
	env, err := er.Create(ctx, p.ID, "prod", "Prod")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := cr.Create(ctx, env.ID, "root", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Leaf rows: one config version + one secret value + one entry (raw insert;
	// this test is about FK cascade, not the secret-write API).
	var cvID, svID string
	if err := testStore.pool.QueryRow(ctx,
		`INSERT INTO config_versions (config_id, version, message) VALUES ($1::uuid,1,'init') RETURNING id::text`,
		cfg.ID).Scan(&cvID); err != nil {
		t.Fatal(err)
	}
	if err := testStore.pool.QueryRow(ctx,
		`INSERT INTO secret_values (config_id, key, value_version, wrapped_dek, ciphertext, nonce)
		 VALUES ($1::uuid,'K',1,'\x00','\x00','\x00') RETURNING id::text`,
		cfg.ID).Scan(&svID); err != nil {
		t.Fatal(err)
	}
	if _, err := testStore.pool.Exec(ctx,
		`INSERT INTO config_version_entries (config_version_id, key, secret_value_id)
		 VALUES ($1::uuid,'K',$2::uuid)`, cvID, svID); err != nil {
		t.Fatal(err)
	}

	// Destroy the project — must succeed and take the whole subtree with it.
	if err := pr.Destroy(ctx, p.ID); err != nil {
		t.Fatalf("destroy: %v", err)
	}

	for _, q := range []struct {
		name, sql string
	}{
		{"environments", `SELECT count(*) FROM environments WHERE project_id = $1::uuid`},
		{"configs", `SELECT count(*) FROM configs WHERE environment_id = $1::uuid`},
		{"config_versions", `SELECT count(*) FROM config_versions WHERE config_id = $1::uuid`},
		{"secret_values", `SELECT count(*) FROM secret_values WHERE config_id = $1::uuid`},
	} {
		arg := env.ID
		if q.name == "environments" {
			arg = p.ID
		}
		if q.name == "config_versions" || q.name == "secret_values" {
			arg = cfg.ID
		}
		var n int
		if err := testStore.pool.QueryRow(ctx, q.sql, arg).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("%s not cascaded: %d rows remain", q.name, n)
		}
	}
}

func TestDestroyConfigBlockedByInheritance(t *testing.T) {
	if testStore == nil {
		t.Skip("postgres/docker not available")
	}
	resetDB(t)
	ctx := context.Background()
	pr := NewProjectRepo(testStore)
	er := NewEnvironmentRepo(testStore)
	cr := NewConfigRepo(testStore)
	id, _ := testStore.NewID(ctx)
	p, _ := pr.Create(ctx, id, "web", "Web", []byte("k"), 1)
	env, _ := er.Create(ctx, p.ID, "prod", "Prod")
	base, _ := cr.Create(ctx, env.ID, "base", nil)
	if _, err := cr.Create(ctx, env.ID, "branch", &base.ID); err != nil {
		t.Fatal(err)
	}
	// inherits_from stays NO ACTION: destroying the still-referenced base fails.
	if err := cr.Destroy(ctx, base.ID); err == nil {
		t.Fatal("expected destroy of an inheritance base to fail")
	}
}
