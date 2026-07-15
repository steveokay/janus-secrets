# Environment Promotion (Phase A) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a user promote secrets forward along a project's release pipeline (dev→staging→prod) via a per-key selectable diff, applied as one new target config version — with admin-configured pipeline order, admin-locked keys, a `secret:promote` RBAC action, value-free audit, and a drag-to-env + diff-modal UI.

**Architecture:** A new `internal/promote` service computes a per-key Diff between two configs and applies selected keys through the existing `secrets.Service.SetSecrets` (source and target share one project KEK, so values are decrypted from the source and re-encrypted under the target — a reveal-then-set). Two small tables hold the ordered pipeline and locked keys. REST + CLI + a React UI (pipeline board drag → diff modal) drive it.

**Tech Stack:** Go, pgx v5, golang-migrate, chi, cobra, `crypto/*`+`x/crypto`, testcontainers; React + TS + Vite + Tailwind + TanStack Query (Nocturne tokens), vitest + msw.

**Spec:** `docs/superpowers/specs/2026-07-14-env-promotion-design.md`. **UI mockup:** `docs/design/ui-promotion-mockup.html`. Read both before starting.

**Key facts (verified against the codebase):**
- `configs` has **no** `project_id`; reach the project via `configs.environment_id → environments.project_id`. `environments` has `project_id`.
- `secrets.Service`: `NewService(st *store.Store, kr *crypto.Keyring) *Service`; `SetSecrets(ctx, configID string, changes []secrets.SecretChange, message, actor string) (store.ConfigVersion, error)`; `RevealConfig(ctx, configID string) (store.ConfigVersion, map[string]secrets.Secret, error)`. `secrets.SecretChange{Key string; Value []byte; Delete bool}`; `secrets.Secret{Key string; Value []byte; ValueVersion int}`.
- `store.SecretRepo`: `NewSecretRepo(s)`, `GetLatest(ctx, configID) (ConfigVersion, map[string]SecretValue, error)`, `GetVersion(ctx, configID, version) (…)`. `store.ConfigRepo`: `NewConfigRepo(s)`, `Get(ctx, id) (Config, error)`, `Create(...)`. `store.Config{ID, EnvironmentID, Name string; InheritsFrom *string; ...}` (verify exact fields when you open it). `store.EnvironmentRepo`: `NewEnvironmentRepo(s)`, `Get`, `List(ctx, projectID)`.
- Store infra: `Store.withTx(ctx, func(pgx.Tx) error) error`, `execAffectingOne`, `mapError`, `ErrNotFound`, `NewID(ctx)`. `pgx` = `github.com/jackc/pgx/v5`. Migrations end at `000015`; next is **000016**.
- `internal/authz/actions.go`: cumulative matrix `viewerActions`→`developerActions`→`adminActions`→`ownerActions`; `roleActions` map. Add actions to the right bundle.
- API idioms: `s.configResource(r) (authz.Resource, cid string, error)` resolves a config's scope chain from the `{cid}` URL param; `s.can(r, action, res) error`; `s.authorize(w,r,action,res,auditAction,auditResource) bool` (records denials); `s.record(r, action, resource, result, code, detail) error`; `s.writeServiceError(w, err)`; `s.writeAuthzError(w, err)`; `writeJSON(w, status, v)`; `writeError(w, status, code, msg)`; codes `CodeValidation`, `CodeInternal`, `CodeForbidden`. `authz.Resource{ProjectID, EnvID, ConfigID string}`. Services are wired in `internal/api/boot.go` (`Boot`) and `internal/api/server.go` (`New`) — a service that only needs `kr`+`st`+`svc` is constructed **inside `New`** like `s.projectKeys` (server.go:79). Routes register in `New` inside `r.Group(func(r chi.Router){ r.Use(RequireAuth(s.auth)); … })`.
- CLI: `cmd/janus/main.go:25` `root.AddCommand(...)`; command pattern in `cmd/janus/project_commands.go` (`newXCmd()`, `--address`/`--token`, `newAPIClient`, `c.call(method, path, in, out)`); secrets-binding pattern in `cmd/janus/secrets_cmd.go` (`secretFlags`, `f.resolveCID()`).
- Web: `web/src/home/ProjectBoard.tsx` renders env columns + `ConfigCard`; `web/src/lib/endpoints.ts` (`endpoints`, `Config`, `Environment` types), `web/src/lib/api.ts` (`api.get/post/put/del`), `web/src/operations/endpoints.ts` (per-feature endpoint module + msw pattern), `web/src/ui/{Modal,Sheet,Pill,ConfirmDialog,cn}.tsx`, `web/src/ui/env.ts` (`envTone`, `envDotClass`). Tokens only — never raw palette; enforced by `web/src/test/no-raw-palette.test.ts` + `dark-aa.test.ts`. Mock shapes MUST mirror Go wire JSON.

**Global rules:** TDD (failing test first). Never log/return/audit a secret **value**. Promotion re-encrypts under the target (never copies ciphertext). Zero plaintext after use (the existing set/reveal paths already do). Do NOT touch the running dev container/DB (ports 8210/5433) or run `make migrate`. Store/api/promote tests use testcontainers (real Postgres, Docker). Web tests: vitest + msw; every mock mirrors the Go handler's JSON.

---

## Backend

### Task 1: Migration `000016_promotion`

**Files:**
- Create: `migrations/000016_promotion.up.sql`
- Create: `migrations/000016_promotion.down.sql`
- Test: `internal/store/promotion_migration_test.go`

- [ ] **Step 1: Write the SQL.**

`migrations/000016_promotion.up.sql`:
```sql
CREATE TABLE promotion_pipeline_steps (
    project_id     uuid    NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    position       integer NOT NULL,
    environment_id uuid    NOT NULL REFERENCES environments (id) ON DELETE CASCADE,
    PRIMARY KEY (project_id, position),
    UNIQUE (project_id, environment_id)
);

CREATE TABLE config_locked_keys (
    config_id  uuid        NOT NULL REFERENCES configs (id) ON DELETE CASCADE,
    key        text        NOT NULL,
    created_by uuid        REFERENCES users (id),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (config_id, key)
);
```

`migrations/000016_promotion.down.sql`:
```sql
DROP TABLE IF EXISTS config_locked_keys;
DROP TABLE IF EXISTS promotion_pipeline_steps;
```

- [ ] **Step 2: Failing migration test** (mirror `internal/store/project_kek_versions_migration_test.go`).

`internal/store/promotion_migration_test.go`:
```go
package store

import (
	"context"
	"testing"
)

func TestMigration016CreatesPromotionTables(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	for _, tbl := range []string{"promotion_pipeline_steps", "config_locked_keys"} {
		var reg *string
		if err := s.pool.QueryRow(context.Background(),
			`SELECT to_regclass('public.'||$1)::text`, tbl).Scan(&reg); err != nil {
			t.Fatalf("query %s: %v", tbl, err)
		}
		if reg == nil || *reg != tbl {
			t.Fatalf("table %s not created, got %v", tbl, reg)
		}
	}
}
```

- [ ] **Step 3: Run — expect PASS.** `go test ./internal/store/ -run TestMigration016 -v`
- [ ] **Step 4: Commit.** `git add migrations/000016_promotion.* internal/store/promotion_migration_test.go && git commit -m "feat(store): promotion pipeline + locked-keys tables (migration 000016)"`

---

### Task 2: `PipelineRepo`

**Files:**
- Create: `internal/store/promotion.go`
- Test: `internal/store/pipeline_test.go`

**Context:** `ProjectRepo` is `struct{ s *Store }`. Follow that. Reuse `mapError`, `ErrNotFound`, `withTx`. `pgx` = `github.com/jackc/pgx/v5`.

- [ ] **Step 1: Failing test.** Uses `requireStore`/`resetDB` and the existing seeding helpers. Check `internal/store/*_test.go` for a project+env seeding helper (`mkProject`/`mkChain`/`seedProjectConfig`); if none returns env ids, create inline via `NewProjectRepo`/`NewEnvironmentRepo`.

`internal/store/pipeline_test.go`:
```go
package store

import (
	"context"
	"testing"
)

func TestPipelineRepoSetGetNext(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	pr := NewProjectRepo(s)
	er := NewEnvironmentRepo(s)
	plr := NewPipelineRepo(s)

	pid, _ := s.NewID(ctx)
	if _, err := pr.Create(ctx, pid, "proj", "Proj", []byte("kek"), 1); err != nil {
		t.Fatal(err)
	}
	mk := func(name string) string {
		id, _ := s.NewID(ctx)
		if _, err := er.Create(ctx, id, pid, name, name); err != nil { // verify Create signature when you open environments.go
			t.Fatalf("env %s: %v", name, err)
		}
		return id
	}
	dev, stg, prod := mk("dev"), mk("staging"), mk("prod")

	if err := plr.Set(ctx, pid, []string{dev, stg, prod}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := plr.Get(ctx, pid)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].EnvironmentID != dev || got[2].EnvironmentID != prod {
		t.Fatalf("Get = %+v", got)
	}
	next, ok, err := plr.NextEnv(ctx, pid, dev)
	if err != nil || !ok || next != stg {
		t.Fatalf("NextEnv(dev) = %q ok=%v err=%v, want staging", next, ok, err)
	}
	if _, ok, _ := plr.NextEnv(ctx, pid, prod); ok {
		t.Fatalf("NextEnv(prod) should be the last step (ok=false)")
	}
	// Set replaces the whole ordering.
	if err := plr.Set(ctx, pid, []string{dev, stg}); err != nil {
		t.Fatal(err)
	}
	if got, _ := plr.Get(ctx, pid); len(got) != 2 {
		t.Fatalf("after replace len = %d, want 2", len(got))
	}
}
```

- [ ] **Step 2: Run — expect FAIL** (undefined). `go test ./internal/store/ -run TestPipelineRepo -v`
- [ ] **Step 3: Implement.**

`internal/store/promotion.go`:
```go
package store

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// PipelineStep is one env in a project's ordered release pipeline.
type PipelineStep struct {
	Position      int
	EnvironmentID string
}

// PipelineRepo stores a project's ordered promotion pipeline.
type PipelineRepo struct{ s *Store }

func NewPipelineRepo(s *Store) *PipelineRepo { return &PipelineRepo{s: s} }

// Get returns the pipeline steps in order (empty if none configured).
func (r *PipelineRepo) Get(ctx context.Context, projectID string) ([]PipelineStep, error) {
	rows, err := r.s.pool.Query(ctx,
		`SELECT position, environment_id::text FROM promotion_pipeline_steps
		  WHERE project_id=$1::uuid ORDER BY position ASC`, projectID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	out := []PipelineStep{}
	for rows.Next() {
		var st PipelineStep
		if err := rows.Scan(&st.Position, &st.EnvironmentID); err != nil {
			return nil, mapError(err)
		}
		out = append(out, st)
	}
	return out, mapError(rows.Err())
}

// Set replaces the whole ordering in one transaction. envIDs is the ordered
// list; positions are assigned 0..n-1.
func (r *PipelineRepo) Set(ctx context.Context, projectID string, envIDs []string) error {
	return r.s.withTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`DELETE FROM promotion_pipeline_steps WHERE project_id=$1::uuid`, projectID); err != nil {
			return mapError(err)
		}
		for i, eid := range envIDs {
			if _, err := tx.Exec(ctx,
				`INSERT INTO promotion_pipeline_steps (project_id, position, environment_id)
				 VALUES ($1::uuid, $2, $3::uuid)`, projectID, i, eid); err != nil {
				return mapError(err)
			}
		}
		return nil
	})
}

// NextEnv returns the environment immediately after envID in the pipeline.
// ok is false when envID is the last step or not in the pipeline.
func (r *PipelineRepo) NextEnv(ctx context.Context, projectID, envID string) (string, bool, error) {
	var next string
	err := r.s.pool.QueryRow(ctx,
		`SELECT environment_id::text FROM promotion_pipeline_steps
		  WHERE project_id=$1::uuid
		    AND position = (SELECT position + 1 FROM promotion_pipeline_steps
		                     WHERE project_id=$1::uuid AND environment_id=$2::uuid)`,
		projectID, envID).Scan(&next)
	if err == pgx.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, mapError(err)
	}
	return next, true, nil
}
```

- [ ] **Step 4: Run — expect PASS.** `go test ./internal/store/ -run TestPipelineRepo -v`
- [ ] **Step 5: Commit.** `git add internal/store/promotion.go internal/store/pipeline_test.go && git commit -m "feat(store): PipelineRepo (ordered promotion pipeline)"`

---

### Task 3: `LockedKeyRepo`

**Files:**
- Modify: `internal/store/promotion.go` (append)
- Test: `internal/store/locked_keys_test.go`

- [ ] **Step 1: Failing test.**

`internal/store/locked_keys_test.go`:
```go
package store

import (
	"context"
	"testing"
)

func TestLockedKeyRepo(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	// seedProjectConfig returns (projectID, configID) — reuse the helper from
	// secrets_rewrap_test.go / secrets_test.go; if it differs, build a config inline.
	_, cid := seedProjectConfig(t, s)
	lr := NewLockedKeyRepo(s)

	if err := lr.Lock(ctx, cid, "DATABASE_URL", ""); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if err := lr.Lock(ctx, cid, "DATABASE_URL", ""); err != nil {
		t.Fatalf("Lock idempotent: %v", err) // re-locking the same key is a no-op, not an error
	}
	keys, err := lr.List(ctx, cid)
	if err != nil || len(keys) != 1 || keys[0] != "DATABASE_URL" {
		t.Fatalf("List = %v, %v", keys, err)
	}
	m, err := lr.AreLocked(ctx, cid, []string{"DATABASE_URL", "API_KEY"})
	if err != nil || !m["DATABASE_URL"] || m["API_KEY"] {
		t.Fatalf("AreLocked = %v, %v", m, err)
	}
	if err := lr.Unlock(ctx, cid, "DATABASE_URL"); err != nil {
		t.Fatal(err)
	}
	if keys, _ := lr.List(ctx, cid); len(keys) != 0 {
		t.Fatalf("after unlock List = %v", keys)
	}
}
```

- [ ] **Step 2: Run — expect FAIL.** `go test ./internal/store/ -run TestLockedKeyRepo -v`
- [ ] **Step 3: Implement (append to `internal/store/promotion.go`).**
```go
// LockedKeyRepo stores keys protected from promotion overwrite/removal, per config.
type LockedKeyRepo struct{ s *Store }

func NewLockedKeyRepo(s *Store) *LockedKeyRepo { return &LockedKeyRepo{s: s} }

// Lock marks a key protected on a config. Idempotent (re-locking is a no-op).
// createdBy may be "" (a service-token actor); stored as NULL.
func (r *LockedKeyRepo) Lock(ctx context.Context, configID, key, createdBy string) error {
	var by any
	if createdBy != "" {
		by = createdBy
	}
	_, err := r.s.pool.Exec(ctx,
		`INSERT INTO config_locked_keys (config_id, key, created_by)
		 VALUES ($1::uuid, $2, $3)
		 ON CONFLICT (config_id, key) DO NOTHING`, configID, key, by)
	return mapError(err)
}

// Unlock removes a key's protection. Removing an absent key is a no-op.
func (r *LockedKeyRepo) Unlock(ctx context.Context, configID, key string) error {
	_, err := r.s.pool.Exec(ctx,
		`DELETE FROM config_locked_keys WHERE config_id=$1::uuid AND key=$2`, configID, key)
	return mapError(err)
}

// List returns a config's locked keys, sorted.
func (r *LockedKeyRepo) List(ctx context.Context, configID string) ([]string, error) {
	rows, err := r.s.pool.Query(ctx,
		`SELECT key FROM config_locked_keys WHERE config_id=$1::uuid ORDER BY key ASC`, configID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, mapError(err)
		}
		out = append(out, k)
	}
	return out, mapError(rows.Err())
}

// AreLocked reports which of keys are locked on the config.
func (r *LockedKeyRepo) AreLocked(ctx context.Context, configID string, keys []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(keys) == 0 {
		return out, nil
	}
	rows, err := r.s.pool.Query(ctx,
		`SELECT key FROM config_locked_keys WHERE config_id=$1::uuid AND key = ANY($2)`, configID, keys)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, mapError(err)
		}
		out[k] = true
	}
	return out, mapError(rows.Err())
}
```

- [ ] **Step 4: Run — expect PASS.** `go test ./internal/store/ -run TestLockedKeyRepo -v`
- [ ] **Step 5: Commit.** `git add internal/store/promotion.go internal/store/locked_keys_test.go && git commit -m "feat(store): LockedKeyRepo (promotion-protected keys)"`

---

### Task 4: `internal/promote` engine (Diff + Preview + Apply)

**Files:**
- Create: `internal/promote/service.go`
- Test: `internal/promote/service_test.go`
- Test: `internal/promote/harness_test.go`

**Context:** Holds `*secrets.Service`, `*store.SecretRepo`, `*store.ConfigRepo`, `*store.PipelineRepo`, `*store.LockedKeyRepo`. Preview compares raw stored values from `secrets.RevealConfig` (which returns raw values via the reveal path). Apply stages `secrets.SecretChange` and calls `SetSecrets`. Both source & target are in the SAME project (same KEK) — `SetSecrets` re-encrypts under the target automatically.

- [ ] **Step 1: Harness + failing property test.** Build a real testcontainers store + unsealed keyring + `secrets.Service` (mirror `internal/secrets/harness_test.go`), plus `mkChain`-style helpers to make a project with two envs (dev, staging), a `default` config in each, and a configured pipeline.

`internal/promote/service_test.go` (core cases — expand per the checklist):
```go
package promote

import (
	"context"
	"testing"
)

// TestPreviewClassifiesAndApplyCreatesVersion is the end-to-end property:
// dev has A,B(changed),C(new); staging has A(same),B(diff),LEGACY(target-only).
func TestPreviewClassifiesAndApplyCreatesVersion(t *testing.T) {
	h := newHarness(t) // builds store+keyring+secrets.Service+promote.Service+two configs+pipeline
	ctx := context.Background()

	h.setSecrets(t, h.devCfg, map[string]string{"A": "1", "B": "dev", "C": "new"})
	h.setSecrets(t, h.stgCfg, map[string]string{"A": "1", "B": "stg", "LEGACY": "on"})

	diff, err := h.svc.Preview(ctx, h.devCfg, h.stgCfg, h.actor)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	byKey := map[string]DiffEntry{}
	for _, e := range diff.Entries {
		byKey[e.Key] = e
	}
	if byKey["A"].Status != StatusSame || byKey["B"].Status != StatusChange ||
		byKey["C"].Status != StatusAdd || byKey["LEGACY"].Status != StatusRemove {
		t.Fatalf("classification wrong: %+v", byKey)
	}
	if byKey["B"].SourceValue != "dev" || byKey["B"].TargetValue != "stg" {
		t.Fatalf("B values = %q/%q", byKey["B"].SourceValue, byKey["B"].TargetValue)
	}

	// Apply: set B and C, leave A (same) and LEGACY (remove) out.
	res, err := h.svc.Apply(ctx, ApplyRequest{
		SourceConfigID: h.devCfg, TargetConfigID: h.stgCfg, SourceVersion: diff.SourceVersion,
		Selections: []Selection{{Key: "B", Action: ActionSet}, {Key: "C", Action: ActionSet}},
		Actor:      h.actor,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(res.Applied) != 2 {
		t.Fatalf("applied = %+v", res.Applied)
	}
	// Staging now has B="dev" (promoted) and C="new"; A unchanged; LEGACY still present.
	vals := h.reveal(t, h.stgCfg)
	if vals["B"] != "dev" || vals["C"] != "new" || vals["A"] != "1" || vals["LEGACY"] != "on" {
		t.Fatalf("staging after apply = %v", vals)
	}
}
```

**Required cases (add as tests):**
- **Illegal step:** Preview/Apply where source→target is not the pipeline's next step → `ErrIllegalStep`.
- **Locked key rejected:** lock `B` on staging, then Apply selecting `B` → `ErrLockedKey` naming `B`; staging `B` unchanged.
- **Remove:** Apply `{LEGACY, ActionRemove}` → staging no longer has `LEGACY` (a new version with a tombstone); rollback restores it.
- **Drift:** delete `C` from dev after Preview, Apply selecting `C` → `C` reported in `res.Skipped`, not applied.
- **Create target:** target env has no `default` config; `Apply` with `CreateTarget:true` (+ TargetEnvID/TargetName) creates it then applies.
- **References copied raw:** dev `REF="${projects.x.dev.K}"` promotes to staging as the literal `${...}` (compare raw).
- **Value-free:** covered by the Task 9 leak test.

- [ ] **Step 2: Run — expect FAIL** (package undefined). `go test ./internal/promote/ -v`
- [ ] **Step 3: Implement.**

`internal/promote/service.go`:
```go
// Package promote moves selected secrets forward along a project's release
// pipeline. Source and target share one project KEK, so promotion decrypts each
// selected source value and re-encrypts it under the target via the secrets
// set path (never copies ciphertext). It never logs or returns a secret value.
package promote

import (
	"context"
	"errors"
	"fmt"

	"github.com/steveokay/janus-secrets/internal/secrets"
	"github.com/steveokay/janus-secrets/internal/store"
)

var (
	ErrIllegalStep = errors.New("promote: not the next pipeline step")
	ErrLockedKey   = errors.New("promote: target key is locked")
	ErrNoPipeline  = errors.New("promote: project has no pipeline configured")
)

type Status string

const (
	StatusAdd    Status = "add"
	StatusChange Status = "change"
	StatusRemove Status = "remove"
	StatusSame   Status = "same"
)

type DiffEntry struct {
	Key         string
	Status      Status
	SourceValue string // raw stored value; "" when absent (Status remove)
	TargetValue string // raw stored value; "" when absent (Status add)
	Locked      bool   // key is locked on the target config
}

type Diff struct {
	SourceVersion int
	TargetExists  bool
	Entries       []DiffEntry
}

type Action string

const (
	ActionSet    Action = "set"
	ActionRemove Action = "remove"
)

type Selection struct {
	Key    string
	Action Action
}

type ApplyRequest struct {
	SourceConfigID string
	TargetConfigID string // empty when creating the target
	TargetEnvID    string // required when TargetConfigID == "" (create)
	TargetName     string // required when creating
	CreateTarget   bool
	SourceVersion  int // pin source to the previewed version
	Selections     []Selection
	Actor          string
}

type ApplyResult struct {
	TargetVersion int
	Applied       []Selection
	Skipped       []string // keys whose source vanished (drift)
}

type Service struct {
	secrets   *secrets.Service
	secretRex *store.SecretRepo
	configs   *store.ConfigRepo
	envs      *store.EnvironmentRepo
	pipeline  *store.PipelineRepo
	locked    *store.LockedKeyRepo
}

func New(sec *secrets.Service, st *store.Store) *Service {
	return &Service{
		secrets:   sec,
		secretRex: store.NewSecretRepo(st),
		configs:   store.NewConfigRepo(st),
		envs:      store.NewEnvironmentRepo(st),
		pipeline:  store.NewPipelineRepo(st),
		locked:    store.NewLockedKeyRepo(st),
	}
}

// projectAndEnv returns (projectID, envID) for a config by walking config→env→project.
func (s *Service) projectAndEnv(ctx context.Context, configID string) (string, string, error) {
	c, err := s.configs.Get(ctx, configID)
	if err != nil {
		return "", "", err
	}
	e, err := s.envs.Get(ctx, c.EnvironmentID)
	if err != nil {
		return "", "", err
	}
	return e.ProjectID, c.EnvironmentID, nil // verify Environment field name (ProjectID)
}

// validateStep confirms sourceEnv→targetEnv is the pipeline's next step.
func (s *Service) validateStep(ctx context.Context, projectID, srcEnv, dstEnv string) error {
	next, ok, err := s.pipeline.NextEnv(ctx, projectID, srcEnv)
	if err != nil {
		return err
	}
	if !ok {
		steps, err := s.pipeline.Get(ctx, projectID)
		if err != nil {
			return err
		}
		if len(steps) == 0 {
			return ErrNoPipeline
		}
		return ErrIllegalStep
	}
	if next != dstEnv {
		return ErrIllegalStep
	}
	return nil
}

// Preview builds the per-key diff between source and target (raw values).
// The caller has already authorized secret:read on both and audited the reveal.
func (s *Service) Preview(ctx context.Context, sourceConfigID, targetConfigID, actor string) (Diff, error) {
	proj, srcEnv, err := s.projectAndEnv(ctx, sourceConfigID)
	if err != nil {
		return Diff{}, err
	}
	_, dstEnv, err := s.projectAndEnv(ctx, targetConfigID)
	if err != nil {
		return Diff{}, err
	}
	if err := s.validateStep(ctx, proj, srcEnv, dstEnv); err != nil {
		return Diff{}, err
	}
	srcVer, srcVals, err := s.secrets.RevealConfig(ctx, sourceConfigID)
	if err != nil {
		return Diff{}, err
	}
	_, dstVals, err := s.secrets.RevealConfig(ctx, targetConfigID)
	if err != nil {
		return Diff{}, err
	}
	lockedKeys, err := s.locked.List(ctx, targetConfigID)
	if err != nil {
		return Diff{}, err
	}
	lockedSet := map[string]bool{}
	for _, k := range lockedKeys {
		lockedSet[k] = true
	}
	seen := map[string]bool{}
	entries := []DiffEntry{}
	add := func(key string) {
		if seen[key] {
			return
		}
		seen[key] = true
		src, inSrc := srcVals[key]
		dst, inDst := dstVals[key]
		e := DiffEntry{Key: key, Locked: lockedSet[key]}
		switch {
		case inSrc && !inDst:
			e.Status, e.SourceValue = StatusAdd, string(src.Value)
		case !inSrc && inDst:
			e.Status, e.TargetValue = StatusRemove, string(dst.Value)
		case string(src.Value) == string(dst.Value):
			e.Status, e.SourceValue, e.TargetValue = StatusSame, string(src.Value), string(dst.Value)
		default:
			e.Status, e.SourceValue, e.TargetValue = StatusChange, string(src.Value), string(dst.Value)
		}
		entries = append(entries, e)
	}
	for k := range srcVals {
		add(k)
	}
	for k := range dstVals {
		add(k)
	}
	return Diff{SourceVersion: srcVer.Version, TargetExists: true, Entries: entries}, nil
}

// Apply promotes the selected keys as one new target config version. The caller
// has authorized secret:promote on target + secret:read on source (+ config:create
// if creating). Locked target keys are rejected. Drifted keys are skipped.
func (s *Service) Apply(ctx context.Context, req ApplyRequest) (ApplyResult, error) {
	proj, srcEnv, err := s.projectAndEnv(ctx, req.SourceConfigID)
	if err != nil {
		return ApplyResult{}, err
	}

	target := req.TargetConfigID
	if target == "" && req.CreateTarget {
		id, err := s.configs.NewIDCreate(ctx, req.TargetEnvID, req.TargetName) // see Step 3a
		if err != nil {
			return ApplyResult{}, err
		}
		target = id
	}
	_, dstEnv, err := s.projectAndEnv(ctx, target)
	if err != nil {
		return ApplyResult{}, err
	}
	if err := s.validateStep(ctx, proj, srcEnv, dstEnv); err != nil {
		return ApplyResult{}, err
	}

	// Reject locked keys among the selections (defense in depth beyond the UI).
	keys := make([]string, 0, len(req.Selections))
	for _, sel := range req.Selections {
		keys = append(keys, sel.Key)
	}
	lockedMap, err := s.locked.AreLocked(ctx, target, keys)
	if err != nil {
		return ApplyResult{}, err
	}
	for _, sel := range req.Selections {
		if lockedMap[sel.Key] {
			return ApplyResult{}, fmt.Errorf("%w: %s", ErrLockedKey, sel.Key)
		}
	}

	// Read source values at the pinned version (raw).
	_, srcState, err := s.secretRex.GetVersion(ctx, req.SourceConfigID, req.SourceVersion)
	if err != nil {
		return ApplyResult{}, err
	}
	// srcState carries wrapped values; reveal the pinned version to get plaintext.
	// Reuse the reveal path for the pinned version:
	srcVals, err := s.secrets.RevealConfigVersion(ctx, req.SourceConfigID, req.SourceVersion) // see Step 3b
	if err != nil {
		return ApplyResult{}, err
	}
	_ = srcState

	changes := make([]secrets.SecretChange, 0, len(req.Selections))
	applied := make([]Selection, 0, len(req.Selections))
	skipped := []string{}
	for _, sel := range req.Selections {
		switch sel.Action {
		case ActionRemove:
			changes = append(changes, secrets.SecretChange{Key: sel.Key, Delete: true})
			applied = append(applied, sel)
		case ActionSet:
			sec, ok := srcVals[sel.Key]
			if !ok {
				skipped = append(skipped, sel.Key) // drift: vanished from the source
				continue
			}
			changes = append(changes, secrets.SecretChange{Key: sel.Key, Value: append([]byte(nil), sec.Value...)})
			applied = append(applied, sel)
		}
	}
	if len(changes) == 0 {
		return ApplyResult{Applied: applied, Skipped: skipped}, nil
	}
	msg := fmt.Sprintf("promote from env %s v%d", srcEnv, req.SourceVersion)
	cv, err := s.secrets.SetSecrets(ctx, target, changes, msg, req.Actor)
	if err != nil {
		return ApplyResult{}, err
	}
	return ApplyResult{TargetVersion: cv.Version, Applied: applied, Skipped: skipped}, nil
}
```

- [ ] **Step 3a:** If `ConfigRepo` has no create-and-return-id helper, add a thin `ConfigRepo.CreateNamed(ctx, envID, name string) (string, error)` in `internal/store/configs.go` that `NewID`s and inserts a root config (no `inherits_from`), returning the id — check the existing `Create` signature first and prefer reusing it (`NewIDCreate` above is a placeholder name; use the real one).
- [ ] **Step 3b:** Add `secrets.Service.RevealConfigVersion(ctx, configID string, version int) (map[string]secrets.Secret, error)` to `internal/secrets/` (mirror `RevealConfig` but read `GetVersion(configID, version)` instead of `GetLatest`; same per-value decrypt path). This pins the promoted values to what the operator previewed. Write it TDD in this task (small test: write v1, write v2, `RevealConfigVersion(cfg,1)` returns v1's values).

- [ ] **Step 4: Run — expect PASS** (fill the harness). `go test ./internal/promote/ -v`
- [ ] **Step 5: Commit.** `git add internal/promote/ internal/secrets/ internal/store/configs.go && git commit -m "feat(promote): Diff/Preview/Apply engine (pipeline-checked, locked-aware, re-encrypt via set path)"`

---

### Task 5: authz actions

**Files:**
- Modify: `internal/authz/actions.go`
- Test: `internal/authz/promotion_actions_test.go`

- [ ] **Step 1: Failing test.**
```go
package authz

import "testing"

func TestPromotionActionMatrix(t *testing.T) {
	cases := []struct {
		role   Role
		action Action
		want   bool
	}{
		{RoleViewer, SecretPromote, false},
		{RoleDeveloper, SecretPromote, true},
		{RoleAdmin, SecretPromote, true},
		{RoleDeveloper, PromotionManage, false},
		{RoleAdmin, PromotionManage, true},
		{RoleOwner, PromotionManage, true},
	}
	for _, c := range cases {
		if got := roleAllows(c.role, c.action); got != c.want {
			t.Errorf("roleAllows(%s,%s)=%v want %v", c.role, c.action, got, c.want)
		}
	}
}
```
- [ ] **Step 2: Run — expect FAIL.** `go test ./internal/authz/ -run TestPromotionAction -v`
- [ ] **Step 3: Implement.** In the const block add:
```go
	SecretPromote   Action = "secret:promote"   // developer+, target-env scoped
	PromotionManage Action = "promotion:manage" // admin+, project-scoped (pipeline + locked keys)
```
Change the bundles:
```go
	developerActions = union(viewerActions, setOf(SecretWrite, ConfigCreate, TransitUse, DynamicIssue, SecretPromote))
	adminActions     = union(developerActions, setOf(
		ConfigDelete, EnvCreate, EnvDelete, ProjectCreate, MemberManage,
		TokenRead, TokenMint, TokenRevoke, UserManage, AuditRead, SysSeal, SysBackup, TransitManage, OIDCManage, RotationManage, SyncManage, DynamicManage, PromotionManage))
```
- [ ] **Step 4: Run — expect PASS**, and `go test ./internal/authz/` (no matrix regressions).
- [ ] **Step 5: Commit.** `git add internal/authz/ && git commit -m "feat(authz): secret:promote (developer+) and promotion:manage (admin+)"`

---

### Task 6: API — pipeline + locked-keys endpoints + service wiring

**Files:**
- Create: `internal/api/promotion_handlers.go`
- Modify: `internal/api/server.go` (field + construction + routes)
- Test: `internal/api/promotion_e2e_test.go`

**Context:** Wire `s.promote *promote.Service` in `New` (construct when `kr != nil && st != nil && svc != nil`, like `s.projectKeys`). Add a `s.pipelineRepo`/`s.lockedRepo` or just build them inline via `store.NewPipelineRepo(s.st)` in handlers. `s.configResource(r)` resolves a config's scope. For project-scoped pipeline routes use `authz.Resource{ProjectID: pid}` from `chi.URLParam(r, "pid")`.

- [ ] **Step 1: Failing e2e test** (mirror `internal/api/kek_e2e_test.go`): admin sets a pipeline `PUT /v1/projects/{pid}/pipeline`; a developer `GET`s it (200); a developer `POST /v1/configs/{cid}/locked-keys` → 403 (needs `promotion:manage`), admin → 200; `GET /v1/configs/{cid}/locked-keys` lists it; `DELETE` removes it. Use the e2e harness that boots a Server with real keyring+store and mints role tokens.
- [ ] **Step 2: Run — expect FAIL.** `go test ./internal/api/ -run Promotion -v`
- [ ] **Step 3: Implement handlers.** `internal/api/promotion_handlers.go`:
```go
package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/steveokay/janus-secrets/internal/authz"
	"github.com/steveokay/janus-secrets/internal/store"
)

func (s *Server) handlePipelineGet(w http.ResponseWriter, r *http.Request) {
	pid := chi.URLParam(r, "pid")
	if err := s.can(r, authz.ProjectRead, authz.Resource{ProjectID: pid}); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	steps, err := store.NewPipelineRepo(s.st).Get(r.Context(), pid)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	ids := make([]string, 0, len(steps))
	for _, st := range steps {
		ids = append(ids, st.EnvironmentID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"environment_ids": ids})
}

func (s *Server) handlePipelinePut(w http.ResponseWriter, r *http.Request) {
	pid := chi.URLParam(r, "pid")
	if !s.authorize(w, r, authz.PromotionManage, authz.Resource{ProjectID: pid}, "promotion.pipeline.set", "projects/"+pid+"/pipeline") {
		return
	}
	var body struct {
		EnvironmentIDs []string `json:"environment_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid body")
		return
	}
	if err := store.NewPipelineRepo(s.st).Set(r.Context(), pid, body.EnvironmentIDs); err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "promotion.pipeline.set", "projects/"+pid+"/pipeline", "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"environment_ids": body.EnvironmentIDs})
}

func (s *Server) handleLockedKeysList(w http.ResponseWriter, r *http.Request) {
	res, cid, err := s.configResource(r)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.can(r, authz.ConfigRead, res); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	keys, err := store.NewLockedKeyRepo(s.st).List(r.Context(), cid)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
}

func (s *Server) handleLockedKeyCreate(w http.ResponseWriter, r *http.Request) {
	res, cid, err := s.configResource(r)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if !s.authorize(w, r, authz.PromotionManage, res, "promotion.key.lock", "configs/"+cid+"/locked-keys") {
		return
	}
	var body struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Key == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "key is required")
		return
	}
	actor, _ := PrincipalFrom(r.Context()) // user id when a user; "" for a token
	if err := store.NewLockedKeyRepo(s.st).Lock(r.Context(), cid, body.Key, actorUserID(actor)); err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "promotion.key.lock", "configs/"+cid+"/locked-keys/"+body.Key, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": body.Key, "locked": true})
}

func (s *Server) handleLockedKeyDelete(w http.ResponseWriter, r *http.Request) {
	res, cid, err := s.configResource(r)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	key := chi.URLParam(r, "key")
	if !s.authorize(w, r, authz.PromotionManage, res, "promotion.key.unlock", "configs/"+cid+"/locked-keys/"+key) {
		return
	}
	if err := store.NewLockedKeyRepo(s.st).Unlock(r.Context(), cid, key); err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "promotion.key.unlock", "configs/"+cid+"/locked-keys/"+key, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": key, "locked": false})
}
```
> `actorUserID(actor)` extracts the user id from the principal (a helper; if `PrincipalFrom` already returns a struct with `UserID`, inline it). Check `PrincipalFrom`'s real shape and adapt — pass `""` for token principals.

- [ ] **Step 4: Wire service + routes** in `internal/api/server.go`. Add field `promote *promote.Service` to `Server`; in `New`, after the `projectKeys` block:
```go
	if kr != nil && st != nil && svc != nil {
		s.promote = promote.New(svc, st)
	}
```
Register in an authenticated group (near the config routes):
```go
	r.Group(func(r chi.Router) {
		r.Use(RequireAuth(s.auth))
		r.Get("/v1/projects/{pid}/pipeline", s.handlePipelineGet)
		r.Put("/v1/projects/{pid}/pipeline", s.handlePipelinePut)
		r.Get("/v1/configs/{cid}/locked-keys", s.handleLockedKeysList)
		r.Post("/v1/configs/{cid}/locked-keys", s.handleLockedKeyCreate)
		r.Delete("/v1/configs/{cid}/locked-keys/{key}", s.handleLockedKeyDelete)
	})
```
Add the `promote` import.

- [ ] **Step 5: Run — expect PASS.** `go test ./internal/api/ -run Promotion -v`
- [ ] **Step 6: Commit.** `git add internal/api/ && git commit -m "feat(api): promotion pipeline + locked-keys endpoints"`

---

### Task 7: API — promote preview + apply

**Files:**
- Modify: `internal/api/promotion_handlers.go`
- Modify: `internal/api/server.go` (routes)
- Test: `internal/api/promotion_apply_e2e_test.go`

**Context:** Preview reveals both configs' values (audited, `secret:read` on both). Apply needs `secret:promote` on the **target** + `secret:read` on the source (+ `config:create` if creating). `from`/`to` are config ids (query params for preview; body for apply).

- [ ] **Step 1: Failing e2e:** with a pipeline dev→staging and secrets in dev, an owner: `GET /v1/promote/preview?from={devCfg}&to={stgCfg}` → 200 with entries; `POST /v1/promote` selecting keys → 200 `{target_version}`, staging updated; a developer with read-only on the target → 403 on apply; illegal step (`from=dev,to=prod`) → 409; a selection of a locked key → 409.
- [ ] **Step 2: Run — expect FAIL.** `go test ./internal/api/ -run PromoteApply -v`
- [ ] **Step 3: Implement.** Append handlers:
```go
func (s *Server) handlePromotePreview(w http.ResponseWriter, r *http.Request) {
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")
	if from == "" || to == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "from and to config ids are required")
		return
	}
	srcRes, err := s.configResourceByID(r.Context(), from) // see note
	if err != nil { s.writeServiceError(w, err); return }
	dstRes, err := s.configResourceByID(r.Context(), to)
	if err != nil { s.writeServiceError(w, err); return }
	// Preview reveals both sides → secret:read on both, audited.
	if !s.authorize(w, r, authz.SecretRead, srcRes, "secret.reveal", "configs/"+from+"/secrets") { return }
	if err := s.can(r, authz.SecretRead, dstRes); err != nil { s.writeAuthzError(w, err); return }
	actor, _ := PrincipalFrom(r.Context())
	diff, err := s.promote.Preview(r.Context(), from, to, actorUserID(actor))
	if err != nil { s.writePromoteError(w, err); return }
	if err := s.record(r, "secret.reveal", "configs/"+to+"/secrets", "success", "", "promote-preview"); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error"); return
	}
	writeJSON(w, http.StatusOK, promoteDiffView(diff))
}

func (s *Server) handlePromoteApply(w http.ResponseWriter, r *http.Request) {
	var body struct {
		From          string `json:"from_config"`
		To            string `json:"to_config"`
		ToEnv         string `json:"to_env"`
		ToName        string `json:"to_name"`
		Create        bool   `json:"create"`
		SourceVersion int    `json:"source_version"`
		Selections    []struct {
			Key    string `json:"key"`
			Action string `json:"action"`
		} `json:"selections"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid body"); return
	}
	srcRes, err := s.configResourceByID(r.Context(), body.From)
	if err != nil { s.writeServiceError(w, err); return }
	if err := s.can(r, authz.SecretRead, srcRes); err != nil { s.writeAuthzError(w, err); return }
	// Target authz: promote on the target env. If creating, resolve the env; else the config.
	var dstRes authz.Resource
	if body.To != "" {
		if dstRes, err = s.configResourceByID(r.Context(), body.To); err != nil { s.writeServiceError(w, err); return }
	} else {
		dstRes = authz.Resource{EnvID: body.ToEnv}
		if err := s.can(r, authz.ConfigCreate, dstRes); err != nil { s.writeAuthzError(w, err); return }
	}
	if !s.authorize(w, r, authz.SecretPromote, dstRes, "secret.promote", "configs/"+body.To) { return }
	actor, _ := PrincipalFrom(r.Context())
	sels := make([]promote.Selection, 0, len(body.Selections))
	for _, sel := range body.Selections {
		sels = append(sels, promote.Selection{Key: sel.Key, Action: promote.Action(sel.Action)})
	}
	res, err := s.promote.Apply(r.Context(), promote.ApplyRequest{
		SourceConfigID: body.From, TargetConfigID: body.To, TargetEnvID: body.ToEnv, TargetName: body.ToName,
		CreateTarget: body.Create, SourceVersion: body.SourceVersion, Selections: sels, Actor: actorUserID(actor),
	})
	if err != nil { s.writePromoteError(w, err); return }
	appliedKeys := make([]string, 0, len(res.Applied))
	for _, a := range res.Applied { appliedKeys = append(appliedKeys, a.Key) }
	if err := s.record(r, "secret.promote", "configs/"+body.To, "success", "", strings.Join(appliedKeys, ",")); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error"); return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"target_version": res.TargetVersion, "applied": appliedKeys, "skipped": res.Skipped,
	})
}
```
Helpers to add in this task:
- `configResourceByID(ctx, cid)` — like `configResource` but from an explicit id (resolve scope chain). If `configResource` already factors this, reuse it.
- `promoteDiffView(diff)` — maps `promote.Diff` → JSON `{source_version, target_exists, entries:[{key,status,source_value,target_value,locked}]}`.
- `writePromoteError(w, err)` — maps `promote.ErrIllegalStep`/`ErrNoPipeline`→409 `pipeline_step_not_allowed`, `promote.ErrLockedKey`→409 `key_locked`, else `s.writeServiceError`.
- Add `strings` import.

- [ ] **Step 4: Routes** (add to the config-routes group or a new authenticated group):
```go
	r.Get("/v1/promote/preview", s.handlePromotePreview)
	r.Post("/v1/promote", s.handlePromoteApply)
```
- [ ] **Step 5: Run — expect PASS.** `go test ./internal/api/ -run 'Promote' -v`
- [ ] **Step 6: Commit.** `git add internal/api/ && git commit -m "feat(api): promote preview + apply endpoints (audited, RBAC, pipeline/lock enforced)"`

---

### Task 8: CLI

**Files:**
- Create: `cmd/janus/promotion_commands.go`
- Modify: `cmd/janus/main.go`
- Test: `cmd/janus/promotion_commands_test.go`

**Context:** `janus promote` uses the `secretFlags` binding pattern (resolves source config from the bound dir + `--to` env); `janus pipeline` + `janus secrets lock/unlock` use `--address`/`--token`. Mirror `secrets_cmd.go` + `project_commands.go`. `c.call(method, path, in, out)`.

- [ ] **Step 1: Failing test** (mirror `project_commands_test.go` httptest-stub style): `janus pipeline set <e1> <e2>` PUTs env ids; `janus secrets lock KEY` POSTs; `janus promote --to staging --dry-run` GETs the preview and prints a diff table; `janus promote --to staging --key B` POSTs an apply.
- [ ] **Step 2: Run — expect FAIL.** `go test ./cmd/janus/ -run Promotion -v`
- [ ] **Step 3: Implement `cmd/janus/promotion_commands.go`** with:
  - `newPromoteCmd()` — `secretFlags` + `--to <env>` (required), `--key K` (repeatable), `--all`, `--include-removes`, `--create-target`, `--dry-run`. Resolves the source config via `f.resolveCID()`, resolves `--to` config id via `c.resolveConfigID(project, toEnv, config)` (same config name), GETs `/v1/promote/preview?from=&to=`; `--dry-run` prints the diff table and stops; otherwise builds selections (`--all` = every add/change not locked; `--key` = those keys; removes only with `--include-removes`) and POSTs `/v1/promote`.
  - `newPipelineCmd()` — `get` (GET `/v1/projects/{pid}/pipeline`) and `set <env>...` (resolve env ids, PUT).
  - Extend `newSecretsCmd()` with `lock <KEY>` / `unlock <KEY>` subcommands POST/DELETE `/v1/configs/{cid}/locked-keys`.
  Full code follows the exact shapes in `project_commands.go`/`secrets_cmd.go`; write each subcommand's `RunE` printing a one-line human summary (`fmt.Fprintf(cmd.OutOrStdout(), ...)`).
- [ ] **Step 4: Register** `newPromoteCmd()` and `newPipelineCmd()` in `cmd/janus/main.go`'s `root.AddCommand(...)`.
- [ ] **Step 5: Run — expect PASS**, plus `go test ./cmd/janus/ -count=1`. 
- [ ] **Step 6: Commit.** `git add cmd/janus/ && git commit -m "feat(cli): janus promote / pipeline / secrets lock"`

---

### Task 9: Value-free leak test + full gate

**Files:**
- Create: `internal/promote/leak_test.go`
- No product changes unless a gate fails.

- [ ] **Step 1: Leak test.** Mirror `internal/api/runs_leak_test.go`/`internal/projectkeys/leak_test.go`: write a sentinel `CANARY="SENTINEL-PROMOTE-7b2c"` in dev, capture logs, run `Preview` + `Apply` promoting `CANARY` to staging; assert the promotion succeeds AND the sentinel value never appears in the captured logs, and that a `secret.promote` audit event (via the api leak harness, or assert the engine emits none itself) carries only key names. Also assert `promoteDiffView`/apply response JSON for a NON-selected reveal path doesn't serialize values into audit.
- [ ] **Step 2: Run — expect PASS.** `go test ./internal/promote/ -run Leak -v`
- [ ] **Step 3: Full backend gate.** `go build ./... && go vet ./... && go test -race ./internal/... ./cmd/...` (all green; testcontainers). Then `GOTOOLCHAIN=go1.26.5 govulncheck ./...` (expect 0) and `gosec -exclude-dir=internal/crypto/shamir ./...` (no new findings). Always run govulncheck under the pinned toolchain.
- [ ] **Step 4: Confirm migration reversibility** — `000016_promotion.down.sql` drops both tables (children first). Migrator tests exercise up on a fresh DB.
- [ ] **Step 5: Commit.** `git add internal/promote/leak_test.go && git commit -m "test(promote): value-free promotion leak proof"`

---

## Frontend

### Task 10: promotion endpoints + types + mocks

**Files:**
- Create: `web/src/promotion/endpoints.ts`
- Test: `web/src/promotion/endpoints.test.ts`
- Modify: `web/src/test/msw.ts` (add promotion handlers)

**Context:** Mirror `web/src/operations/endpoints.ts`: a typed module wrapping `api.get/post/put/del`. Types MUST mirror the Go JSON: preview `{source_version:number, target_exists:boolean, entries:{key,status,source_value,target_value,locked}[]}`, apply body/response as in Task 7, pipeline `{environment_ids:string[]}`, locked-keys `{keys:string[]}`.

- [ ] **Step 1: Failing test** asserting each endpoint hits the right URL/method with msw. 
- [ ] **Step 2–4:** Implement `endpoints.ts`:
```ts
import { api } from '../lib/api'

export type PromoteStatus = 'add' | 'change' | 'remove' | 'same'
export interface DiffEntry {
  key: string; status: PromoteStatus
  source_value: string; target_value: string; locked: boolean
}
export interface PromoteDiff { source_version: number; target_exists: boolean; entries: DiffEntry[] }
export interface Selection { key: string; action: 'set' | 'remove' }

export const promotion = {
  pipeline: {
    get: (pid: string) => api.get<{ environment_ids: string[] }>(`/v1/projects/${pid}/pipeline`),
    set: (pid: string, ids: string[]) => api.put(`/v1/projects/${pid}/pipeline`, { environment_ids: ids }),
  },
  locked: {
    list: (cid: string) => api.get<{ keys: string[] }>(`/v1/configs/${cid}/locked-keys`),
    lock: (cid: string, key: string) => api.post(`/v1/configs/${cid}/locked-keys`, { key }),
    unlock: (cid: string, key: string) => api.del(`/v1/configs/${cid}/locked-keys/${encodeURIComponent(key)}`),
  },
  preview: (from: string, to: string) =>
    api.get<PromoteDiff>(`/v1/promote/preview?from=${from}&to=${to}`),
  apply: (body: {
    from_config: string; to_config?: string; to_env?: string; to_name?: string
    create?: boolean; source_version: number; selections: Selection[]
  }) => api.post<{ target_version: number; applied: string[]; skipped: string[] }>(`/v1/promote`, body),
}
```
Add msw handlers returning representative JSON. Run `npm run test -- --run src/promotion/endpoints` → PASS.
- [ ] **Step 5: Commit.** `git add web/src/promotion/ web/src/test/msw.ts && git commit -m "feat(web): promotion endpoints + types + mocks"`

---

### Task 11: `PromotionDiffModal`

**Files:**
- Create: `web/src/promotion/PromotionDiffModal.tsx`
- Test: `web/src/promotion/PromotionDiffModal.test.tsx`

**Context:** Build to `docs/design/ui-promotion-mockup.html` using the `Modal` primitive + Nocturne token classes only (no raw palette). Props: `{ from: Config, to: Config, fromEnv: Environment, toEnv: Environment, onClose, onDone }`. Uses TanStack Query for the preview and a mutation for apply.

- [ ] **Step 1: Failing tests:** renders rows with status chips; env-locked rows are checkbox-disabled and pre-unchecked; `remove` rows pre-unchecked; `add`/`change` pre-checked; the footer count + primary button reflect selection; reveal toggle unmasks a value; confirm calls `promotion.apply` with the selected `{key,action}` and shows a success toast.
- [ ] **Step 2–4:** Implement the modal: `useQuery(['promote', from.id, to.id], () => promotion.preview(from.id, to.id))`; local `selected` state derived from entries (default checked for add/change unless locked; unchecked for remove/same/locked); rows = checkbox · key(mono) · status `Pill`/chip · from→to values (masked with a reveal toggle button); footer summary + `Modal` actions; `useMutation` calling `promotion.apply(...)` then `queryClient.invalidateQueries` for the target config's secrets + `onDone`. Use existing `Pill`, `Modal`, `Toast`, `cn`, `envDotClass`. Run `npm run test -- --run src/promotion/PromotionDiffModal` → PASS.
- [ ] **Step 5: Commit.** `git add web/src/promotion/ && git commit -m "feat(web): promotion diff modal (per-key selectable, locked-aware, audited reveal)"`

---

### Task 12: Pipeline board drag integration

**Files:**
- Modify: `web/src/home/ProjectBoard.tsx`
- Create: `web/src/promotion/usePipeline.ts`
- Test: `web/src/home/ProjectBoard.promote.test.tsx`

**Context:** Fetch the project pipeline; order env columns by it (fall back to existing order if unconfigured — promotion disabled then). Make `ConfigCard` draggable; on dragstart mark the legal next env droppable and dim others; on drop (or a "Promote →" button on the card) open `PromotionDiffModal` with source=dragged config, target=same-named config in the next env (or create-target when missing).

- [ ] **Step 1: Failing tests:** with a configured pipeline, dragging a dev config marks staging droppable and prod not; dropping opens the modal with the right source/target; a "Promote →" button opens it too; with no pipeline, cards are not draggable and no promote affordance shows.
- [ ] **Step 2–4:** Add `usePipeline(pid)` (query `promotion.pipeline.get`). In `ProjectBoard`, sort `envs.data` by pipeline position; compute `nextEnvId(envId)`. Add `draggable` + `onDragStart/onDragEnd` to `ConfigCard` (guard: only when a next env exists and the user isn't obviously read-only — the server still enforces), `onDragOver/onDrop` on the next `EnvColumn`, and a `Promote →` button. Track `promoting` state `{ from: Config; fromEnv; toEnv; toConfig?|create }` → render `PromotionDiffModal`. Respect `prefers-reduced-motion` for any drag affordance animation. Tokens only. Run the board tests + full `npm run test -- --run`.
- [ ] **Step 5: Commit.** `git add web/src/home/ProjectBoard.tsx web/src/promotion/ && git commit -m "feat(web): drag-to-env promotion on the project board"`

---

### Task 13: Locked-key management + pipeline config UI

**Files:**
- Modify: the secret editor (`web/src/secrets/SecretEditor.tsx` and/or its row component — open it to find the row) to add a lock/unlock affordance per key (admin only).
- Create: `web/src/promotion/PipelineSettings.tsx` (admin drag-to-reorder or simple ordered picker of envs) + route/entry.
- Test: `web/src/promotion/PipelineSettings.test.tsx` + a secret-editor lock test.

- [ ] **Step 1: Failing tests:** a locked key shows a lock icon in the editor and appears in the promotion diff as disabled; toggling lock calls `promotion.locked.lock/unlock`; `PipelineSettings` lists envs, lets an admin set the order, and calls `promotion.pipeline.set`.
- [ ] **Step 2–4:** Implement. In the editor row, add a small lock toggle button (only rendered when the user can `promotion:manage` — reuse whatever role/permission signal the UI already has, e.g. an `useCan`/role from `AuthProvider`; if none exists, render it and let the 403 surface a toast). `PipelineSettings`: fetch envs + current pipeline, present an ordered list with up/down or drag reorder, Save → `promotion.pipeline.set`. Add an entry point (project settings section or a route). Tokens only. Run web tests.
- [ ] **Step 5: Commit.** `git add web/src/ && git commit -m "feat(web): locked-key management + pipeline configuration UI"`

---

### Task 14: Web gate

**Files:** none (verification), fix-forward only.

- [ ] **Step 1:** `cd web && npm run test -- --run` (all pass), `npm run build`, `npm run smoke` (dual-theme; dark canvas `rgb(11,10,20)`), and the guard tests (`no-raw-palette`, `dark-aa`, `no-legacy-alias`). Typecheck via the build.
- [ ] **Step 2:** Fix any failures (most likely: a raw palette class slipped in, or an msw mock shape drifted from the Go JSON). Re-run until green.
- [ ] **Step 3: Commit** any fixes. `git commit -am "test(web): promotion UI green in both themes"`

---

### Task 15: Final gate + PR

- [ ] **Step 1:** Backend `go build ./... && go vet ./... && go test -race ./internal/... ./cmd/...` green; `GOTOOLCHAIN=go1.26.5 govulncheck ./...` = 0; `gosec -exclude-dir=internal/crypto/shamir ./...` clean.
- [ ] **Step 2:** Web gate (Task 14) green.
- [ ] **Step 3:** Push `env-promotion`; open a PR (base `main`) titled "Environment promotion (Phase A)" summarizing: pipeline + locked-keys tables (000016), `internal/promote` engine (diff + re-encrypt-via-set apply), `secret:promote`/`promotion:manage` RBAC, audited value-free promotion, REST + CLI + drag/diff UI. **Do NOT merge** — the user merges after review.

---

## Self-review notes

- **Spec coverage:** pipeline table+repo = T1/T2; locked keys = T1/T3; engine (Preview/Apply, re-encrypt, drift, create-target, references) = T4; RBAC = T5; pipeline/lock API = T6; promote API = T7; CLI = T8; value-free proof + gate = T9; endpoints/types = T10; diff modal = T11; drag board = T12; lock + pipeline UI = T13; web gate = T14; PR = T15. Every spec section maps to a task. ✅
- **Type consistency:** `promote.Diff/DiffEntry/Status(StatusAdd…)/Selection/Action(ActionSet/ActionRemove)/ApplyRequest/ApplyResult` defined in T4 and used verbatim in T7; the web `DiffEntry/PromoteDiff/Selection` in T10 mirror the Go JSON (`source_value`/`target_value`/`locked`, `status ∈ add|change|remove|same`, `action ∈ set|remove`). `SecretPromote`/`PromotionManage` (T5) used in T6/T7. ✅
- **Value-free:** engine returns/decrypts values only for the diff and the set path; `secret.promote` audit records key names only (T7); leak test proves it (T9). ✅
- **Verify-before-code flags:** three names to confirm against the real source when implementing — `EnvironmentRepo.Create`/`Get` signatures + `Environment.ProjectID` field (T2/T4), `ConfigRepo` create-and-return-id helper (T4 Step 3a), and `PrincipalFrom`'s shape for `actorUserID` (T6). Each task says to check the real signature first.
```
