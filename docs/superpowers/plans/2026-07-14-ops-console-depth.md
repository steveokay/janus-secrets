# Operations Console Depth — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the `/operations` console durable per-run history, column sorting, multi-row bulk operations, and a health overview — a backend run-history layer (PR 1) plus a frontend depth layer (PR 2).

**Architecture:** PR 1 adds `rotation_runs`/`sync_runs` tables written in the same transaction as the existing state mark (with a 100-row-per-owner cap), plus `ListRuns` repo methods and `GET …/{id}/runs` endpoints; dynamic reuses its existing leases history. PR 2 extends the shared `OpsTable` with sorting + selection, adds a bulk `SelectionBar` (client-side fan-out of existing per-id endpoints), a health strip, and a run-history `Sheet`.

**Tech Stack:** Go + `pgx` + `golang-migrate` (backend); React + TS + Vite + Tailwind (Nocturne tokens) + TanStack Query + MSW/Vitest (frontend). testcontainers for store tests.

**Spec:** `docs/superpowers/specs/2026-07-14-ops-console-depth-design.md`.

---

## Invariants (apply to EVERY task)

- **Security (unchanged):** run rows carry ONLY timestamps, `status`, the existing **sanitized** error category, an integer `config_version`, `attempt_num`, and (sync) `keys_count` — never a secret value, DSN, PAT, kubeconfig, or config content. The console's only rendered plaintext stays the ephemeral once-issued dynamic password (`IssuedCredsModal`). Bulk ops call the existing per-id endpoints (each individually audited server-side) — no new bulk endpoint.
- **Backend:** parameterized SQL only; migrations reversible (`.up`/`.down`); `make test` + `govulncheck` + `gosec` clean; run-recording shares the state-mark transaction so state and history stay consistent.
- **Frontend:** design tokens only (no hex, no `dark:`, no raw palette, no legacy aliases `text-muted`/`text-faint`/`shadow-card`); kit primitives; both themes; MSW mocks mirror the Go wire shapes (mock-drift rule). Guards `no-raw-palette`/`dark-aa`/`no-legacy-alias`, tsc, build, dual-theme smoke green.
- **No regressions:** existing engine behavior (crash-safe rotate/sync, scheduler, per-row actions, 403-tolerant fan-out) unchanged; existing tests stay green.

## File structure

```
migrations/000013_rotation_runs.up.sql / .down.sql   (PR1) rotation run table
migrations/000014_sync_runs.up.sql / .down.sql       (PR1) sync run table
internal/store/rotation_runs.go                      (PR1) RotationRun struct + ListRuns + insert/prune helper
internal/store/sync_runs.go                          (PR1) SyncRun struct + ListRuns + insert/prune helper
internal/store/rotation.go (MODIFY)                  (PR1) MarkRotated/MarkFailure → tx + run insert
internal/store/sync.go (MODIFY)                      (PR1) MarkSynced/MarkFailure → tx + run insert
internal/rotation/execute.go (MODIFY)                (PR1) capture startedAt, pass to Mark*
internal/secretsync/reconcile.go (MODIFY)            (PR1) capture startedAt, pass to Mark*
internal/api/rotation_handlers.go (MODIFY)           (PR1) handleRotationRuns
internal/api/sync_handlers.go (MODIFY)               (PR1) handleSyncRuns
internal/api/server.go (MODIFY)                      (PR1) register /runs routes

web/src/lib/useRowSelection.ts (MOVED from secrets/) (PR2) shared selection hook
web/src/operations/ops-ui.tsx (MODIFY)               (PR2) OpsTable sortable headers + checkbox column
web/src/operations/OpsSelectionBar.tsx               (PR2) bulk action bar
web/src/operations/HealthStrip.tsx                   (PR2) current-state counts above tabs
web/src/operations/RunHistorySheet.tsx               (PR2) rotation/sync run drill-in
web/src/operations/endpoints.ts (MODIFY)             (PR2) rotation.runs / sync.runs wrappers + types
web/src/operations/RotationPanel.tsx / SyncPanel.tsx / DynamicPanel.tsx (MODIFY) (PR2) sort + select + bulk + drill-in wiring
web/src/operations/OperationsPage.tsx (MODIFY)       (PR2) render HealthStrip
web/src/operations/LeasesSheet.tsx (MODIFY)          (PR2) lease bulk-revoke
```

---

# PHASE 1 — Backend run-history (ends with PR 1)

### Task 1: Migrations — `rotation_runs` + `sync_runs`

**Files:** Create `migrations/000013_rotation_runs.up.sql`, `.down.sql`, `migrations/000014_sync_runs.up.sql`, `.down.sql`.

**Context:** Confirm 000012 is the highest existing migration (`ls migrations/`); if not, bump the numbers to the next free pair. Migrations run via `make migrate`.

- [ ] **Step 1: Write `000013_rotation_runs.up.sql`:**

```sql
CREATE TABLE rotation_runs (
  id              BIGSERIAL PRIMARY KEY,
  policy_id       UUID NOT NULL REFERENCES rotation_policies(id) ON DELETE CASCADE,
  started_at      TIMESTAMPTZ NOT NULL,
  ended_at        TIMESTAMPTZ NOT NULL,
  status          TEXT NOT NULL CHECK (status IN ('success','failure')),
  error           TEXT,
  config_version  INTEGER,
  attempt_num     INTEGER NOT NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_rotation_runs_policy ON rotation_runs (policy_id, id DESC);
```

- [ ] **Step 2: Write `000013_rotation_runs.down.sql`:** `DROP TABLE IF EXISTS rotation_runs;`

- [ ] **Step 3: Write `000014_sync_runs.up.sql`:**

```sql
CREATE TABLE sync_runs (
  id              BIGSERIAL PRIMARY KEY,
  target_id       UUID NOT NULL REFERENCES sync_targets(id) ON DELETE CASCADE,
  started_at      TIMESTAMPTZ NOT NULL,
  ended_at        TIMESTAMPTZ NOT NULL,
  status          TEXT NOT NULL CHECK (status IN ('success','failure')),
  error           TEXT,
  config_version  INTEGER,
  keys_count      INTEGER NOT NULL DEFAULT 0,
  attempt_num     INTEGER NOT NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_sync_runs_target ON sync_runs (target_id, id DESC);
```

- [ ] **Step 4: Write `000014_sync_runs.down.sql`:** `DROP TABLE IF EXISTS sync_runs;`

- [ ] **Step 5: Apply + verify.** `make migrate` (or the project's migrate command); confirm no error. Then `make migrate` down-one-and-up if the toolchain supports it, else trust the reversible pair. Commit.

```bash
git add migrations/000013_rotation_runs.up.sql migrations/000013_rotation_runs.down.sql migrations/000014_sync_runs.up.sql migrations/000014_sync_runs.down.sql
git commit -m "feat(store): rotation_runs + sync_runs migrations (per-run history)"
```

---

### Task 2: `RotationRun` struct + `ListRuns` + insert/prune helper

**Files:** Create `internal/store/rotation_runs.go`. Test: `internal/store/rotation_runs_test.go`.

**Context:** Mirror the existing repo idioms in `internal/store/rotation.go` (`RotationRepo`, `r.s.pool`, `mapError`, keyset scanning). `RunHistoryCap = 100`. Runs are read newest-first, keyset by `id` descending.

- [ ] **Step 1: Write the failing test** (`rotation_runs_test.go`) — uses the same testcontainers harness as `rotation_test.go` (copy its setup). Assert: after inserting 3 runs for a policy, `ListRuns` returns them newest-first; inserting beyond 100 prunes the oldest; keyset cursor pages.

```go
func TestRotationRunsInsertListPrune(t *testing.T) {
    st := newTestStore(t) // same helper rotation_test.go uses
    ctx := context.Background()
    pid := seedPolicy(t, st) // helper that inserts a rotation_policies row; adapt to existing seed
    // insert 105 runs
    for i := 0; i < 105; i++ {
        if err := st.Rotation().InsertRun(ctx, RotationRunInput{
            PolicyID: pid, StartedAt: time.Now(), EndedAt: time.Now(),
            Status: "success", ConfigVersion: ptrInt(i + 1), AttemptNum: 0,
        }); err != nil { t.Fatal(err) }
    }
    runs, err := st.Rotation().ListRuns(ctx, pid, 0, 50)
    if err != nil { t.Fatal(err) }
    if len(runs) != 50 { t.Fatalf("want 50 got %d", len(runs)) }
    if runs[0].ConfigVersion == nil || *runs[0].ConfigVersion != 105 {
        t.Fatalf("newest first expected v105, got %v", runs[0].ConfigVersion)
    }
    // total capped at 100
    var total int
    st.pool.QueryRow(ctx, `SELECT count(*) FROM rotation_runs WHERE policy_id=$1`, pid).Scan(&total)
    if total != 100 { t.Fatalf("cap 100, got %d", total) }
}
```

(Adapt `newTestStore`/`seedPolicy`/`ptrInt` to the helpers already present in the store test package; read `internal/store/rotation_test.go` first and reuse its exact harness names.)

- [ ] **Step 2: Run — expect FAIL** (undefined). `go test ./internal/store/ -run TestRotationRuns`

- [ ] **Step 3: Implement `rotation_runs.go`:**

```go
package store

import (
    "context"
    "time"
)

const RunHistoryCap = 100

// RotationRun is one recorded rotation attempt (value-free: no secret material).
type RotationRun struct {
    ID            int64
    PolicyID      string
    StartedAt     time.Time
    EndedAt       time.Time
    Status        string // success | failure
    Error         *string
    ConfigVersion *int
    AttemptNum    int
    CreatedAt     time.Time
}

// RotationRunInput is the value recorded for one attempt.
type RotationRunInput struct {
    PolicyID      string
    StartedAt     time.Time
    EndedAt       time.Time
    Status        string
    Error         *string
    ConfigVersion *int
    AttemptNum    int
}

// InsertRun records a run and prunes to the newest RunHistoryCap for the policy.
// Standalone (own tx) — the mark-path variant runs inside the state tx (see
// insertRunTx). Used directly by tests.
func (r *RotationRepo) InsertRun(ctx context.Context, in RotationRunInput) error {
    tx, err := r.s.pool.Begin(ctx)
    if err != nil {
        return mapError(err)
    }
    defer tx.Rollback(ctx)
    if err := insertRotationRunTx(ctx, tx, in); err != nil {
        return err
    }
    return mapError(tx.Commit(ctx))
}

func (r *RotationRepo) ListRuns(ctx context.Context, policyID string, cursor int64, limit int) ([]RotationRun, error) {
    if limit <= 0 || limit > 100 {
        limit = 50
    }
    rows, err := r.s.pool.Query(ctx,
        `SELECT id, policy_id, started_at, ended_at, status, error, config_version, attempt_num, created_at
           FROM rotation_runs
          WHERE policy_id = $1::uuid AND ($2 = 0 OR id < $2)
          ORDER BY id DESC LIMIT $3`, policyID, cursor, limit)
    if err != nil {
        return nil, mapError(err)
    }
    defer rows.Close()
    out := make([]RotationRun, 0, limit)
    for rows.Next() {
        var x RotationRun
        if err := rows.Scan(&x.ID, &x.PolicyID, &x.StartedAt, &x.EndedAt, &x.Status,
            &x.Error, &x.ConfigVersion, &x.AttemptNum, &x.CreatedAt); err != nil {
            return nil, err
        }
        out = append(out, x)
    }
    return out, mapError(rows.Err())
}
```

Add the tx helpers (used by both `InsertRun` and the mark-path in Task 3). `pgx.Tx` satisfies the `Query/Exec` calls:

```go
// insertRotationRunTx inserts one run then prunes to RunHistoryCap for the
// policy, all on the caller's transaction. dbtx is *pgxpool.Pool or pgx.Tx.
func insertRotationRunTx(ctx context.Context, dbtx interface {
    Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, in RotationRunInput) error {
    if _, err := dbtx.Exec(ctx,
        `INSERT INTO rotation_runs (policy_id, started_at, ended_at, status, error, config_version, attempt_num)
         VALUES ($1::uuid, $2, $3, $4, $5, $6, $7)`,
        in.PolicyID, in.StartedAt, in.EndedAt, in.Status, in.Error, in.ConfigVersion, in.AttemptNum); err != nil {
        return mapError(err)
    }
    _, err := dbtx.Exec(ctx,
        `DELETE FROM rotation_runs WHERE policy_id=$1::uuid AND id NOT IN (
           SELECT id FROM rotation_runs WHERE policy_id=$1::uuid ORDER BY id DESC LIMIT $2)`,
        in.PolicyID, RunHistoryCap)
    return mapError(err)
}
```

Add the `pgconn` import (`github.com/jackc/pgx/v5/pgconn`). If the store already defines a `dbExecer` interface for tx/pool, reuse it instead of the inline anonymous interface.

- [ ] **Step 4: Run — expect PASS.** `go test ./internal/store/ -run TestRotationRuns`
- [ ] **Step 5: Commit.** `git add internal/store/rotation_runs.go internal/store/rotation_runs_test.go && git commit -m "feat(store): RotationRun ListRuns + capped insert helper"`

---

### Task 3: Record rotation runs in the mark-path (same tx)

**Files:** Modify `internal/store/rotation.go` (`MarkRotated`, `MarkFailure`), `internal/rotation/execute.go` (call sites).

**Context:** `MarkRotated(ctx, id, configVersion int, next time.Time)` and `MarkFailure(ctx, id, sanitizedErr string, next time.Time, threshold int)` currently run a single UPDATE via `r.s.execAffectingOne`. Convert each to an explicit transaction that runs the SAME UPDATE, then `insertRotationRunTx`, then commit — so the policy row and its run row move together. Add a `startedAt time.Time` and `attemptNum int` param to both; the executor passes them.

- [ ] **Step 1: Modify `MarkRotated`:**

```go
func (r *RotationRepo) MarkRotated(ctx context.Context, id string, configVersion int, next, startedAt time.Time, attemptNum int) error {
    tx, err := r.s.pool.Begin(ctx)
    if err != nil {
        return mapError(err)
    }
    defer tx.Rollback(ctx)
    ct, err := tx.Exec(ctx,
        `UPDATE rotation_policies SET
           pending_ct=NULL, pending_nonce=NULL, pending_wrapped_dek=NULL, pending_state=NULL,
           failure_count=0, status='active', last_error=NULL,
           last_rotated_at=now(), last_config_version=$2, next_rotation_at=$3, updated_at=now()
         WHERE id=$1::uuid`, id, configVersion, next)
    if err != nil {
        return mapError(err)
    }
    if ct.RowsAffected() != 1 {
        return ErrNotFound
    }
    cv := configVersion
    return finishTx(ctx, tx, insertRotationRunTx(ctx, tx, RotationRunInput{
        PolicyID: id, StartedAt: startedAt, EndedAt: time.Now(),
        Status: "success", ConfigVersion: &cv, AttemptNum: attemptNum,
    }))
}
```

Use whatever "affected==1 else ErrNotFound" sentinel `execAffectingOne` used (read it — likely `ErrNotFound`). If there is no `finishTx` helper, inline: `if err := insertRotationRunTx(...); err != nil { return err }; return mapError(tx.Commit(ctx))`.

- [ ] **Step 2: Modify `MarkFailure`:**

```go
func (r *RotationRepo) MarkFailure(ctx context.Context, id, sanitizedErr string, next time.Time, threshold int, startedAt time.Time, attemptNum int) error {
    tx, err := r.s.pool.Begin(ctx)
    if err != nil {
        return mapError(err)
    }
    defer tx.Rollback(ctx)
    ct, err := tx.Exec(ctx,
        `UPDATE rotation_policies SET
           failure_count = failure_count + 1, last_error = $2, next_rotation_at = $3,
           status = CASE WHEN failure_count + 1 >= $4 THEN 'failed' ELSE status END,
           updated_at = now()
         WHERE id=$1::uuid`, id, sanitizedErr, next, threshold)
    if err != nil {
        return mapError(err)
    }
    if ct.RowsAffected() != 1 {
        return ErrNotFound
    }
    e := sanitizedErr
    if err := insertRotationRunTx(ctx, tx, RotationRunInput{
        PolicyID: id, StartedAt: startedAt, EndedAt: time.Now(),
        Status: "failure", Error: &e, ConfigVersion: nil, AttemptNum: attemptNum,
    }); err != nil {
        return err
    }
    return mapError(tx.Commit(ctx))
}
```

- [ ] **Step 3: Update call sites in `execute.go`.** In `attempt()`, capture `startedAt := s.now()` at the very top (before `s.rotate`). On success: `s.repo.MarkRotated(ctx, p.ID, cv.Version, next, startedAt, p.FailureCount)` — but `cv.Version`/`next` are inside `rotate()`. Simplest: thread `startedAt` into `rotate()` OR compute the run in `attempt()`. To keep the change small, pass `startedAt` and `attemptNum` down: change `rotate(ctx, p)` to `rotate(ctx, p, startedAt)` and inside it call `MarkRotated(ctx, p.ID, cv.Version, next, startedAt, p.FailureCount)`. For failure in `attempt()`: `s.repo.MarkFailure(ctx, p.ID, sanitize(err), next, failureThreshold, startedAt, p.FailureCount+1)`. (`p.FailureCount` is the prior count: success ran after `p.FailureCount` prior failures; a failure is attempt number `p.FailureCount+1`.)

- [ ] **Step 4: Update existing rotation store tests** that call `MarkRotated`/`MarkFailure` with the old signature — add `time.Now()` and an attempt int. Run `go test ./internal/store/ ./internal/rotation/` — all green (existing crash-recovery + mark tests still pass; a run row now also exists, which older tests ignore).

- [ ] **Step 5: Add a store test** asserting a success mark writes a `success` run (config_version set, error null) and a failure mark writes a `failure` run (error = category, config_version null). Commit.

```bash
git add internal/store/rotation.go internal/store/rotation_runs_test.go internal/rotation/execute.go
git commit -m "feat(rotation): record each run in rotation_runs within the mark tx"
```

---

### Task 4: Sync runs — store + mark-path (mirror Task 2+3 for sync)

**Files:** Create `internal/store/sync_runs.go` (+ test); Modify `internal/store/sync.go` (`MarkSynced`, `MarkFailure`), `internal/secretsync/reconcile.go`.

**Context:** Read `internal/store/sync.go` and `internal/secretsync/reconcile.go` first. `MarkSynced` records success (has the synced config version + managed keys); `MarkFailure` records failure. Mirror Task 2/3 exactly with sync columns, adding `keys_count` (len of managed keys that run) to the success run and `attempt_num`.

- [ ] **Step 1:** Create `sync_runs.go` with `SyncRun`/`SyncRunInput` (add `KeysCount int`), `InsertRun`, `ListRuns(ctx, targetID, cursor, limit)`, and `insertSyncRunTx` — byte-for-byte analogous to `rotation_runs.go` but table `sync_runs`, column `target_id`, extra `keys_count` in the INSERT. `RunHistoryCap` is shared (already defined in `rotation_runs.go`; do not redeclare).
- [ ] **Step 2:** Failing store test `TestSyncRunsInsertListPrune` (mirror Task 2's, target-scoped, 100 cap).
- [ ] **Step 3:** Convert `MarkSynced`/`MarkFailure` to tx + `insertSyncRunTx` (mirror Task 3), adding `startedAt time.Time` + `attemptNum int` params; success run sets `ConfigVersion` + `KeysCount = len(managedKeys)`, failure sets `Error`.
- [ ] **Step 4:** Update `reconcile.go` call sites to capture `startedAt` at the top of the attempt and pass `startedAt`/attempt num (prior failure count on success, +1 on failure). Update existing sync store tests for the new signatures.
- [ ] **Step 5:** Run `go test ./internal/store/ ./internal/secretsync/` green. Commit `feat(sync): record each run in sync_runs within the mark tx`.

---

### Task 5: Endpoints — `GET …/runs` for rotation + sync

**Files:** Modify `internal/api/rotation_handlers.go`, `internal/api/sync_handlers.go`, `internal/api/server.go`. Tests: `internal/api/rotation_e2e_test.go` (or the existing rotation handler test file) + sync equivalent.

**Context:** Read an existing rotation handler (e.g. `handleRotationGet`) for the authZ pattern (`s.authorize(...)` with the policy's scope) and how it loads the policy + resolves project/config scope. Read `handleAuditEvents` (`internal/api/audit_handlers.go:41`) for the `{items, next_cursor}` + `limit`/`cursor` query parsing shape to mirror.

- [ ] **Step 1: Add `handleRotationRuns`** to `rotation_handlers.go`:

```go
type rotationRunsResponse struct {
    Runs       []rotationRunDTO `json:"runs"`
    NextCursor *int64           `json:"next_cursor"`
}
type rotationRunDTO struct {
    ID            int64   `json:"id"`
    StartedAt     string  `json:"started_at"`
    EndedAt       string  `json:"ended_at"`
    Status        string  `json:"status"`
    Error         *string `json:"error,omitempty"`
    ConfigVersion *int    `json:"config_version,omitempty"`
    AttemptNum    int     `json:"attempt_num"`
}

func (s *Server) handleRotationRuns(w http.ResponseWriter, r *http.Request) {
    id := chi.URLParam(r, "id")
    pol, err := s.rotation.Get(r.Context(), id) // however the existing GET loads it
    if err != nil { /* map 404/500 exactly like handleRotationGet */ }
    // Same authZ the GET uses (scope from pol.ProjectID/ConfigID):
    if !s.authorize(w, r, authz.RotationRead, /* scope */, "rotation.runs", "rotation/"+id) {
        return
    }
    limit, cursor, ok := parseRunsPaging(w, r) // helper below
    if !ok { return }
    runs, err := s.store.Rotation().ListRuns(r.Context(), id, cursor, limit)
    if err != nil { writeError(w, http.StatusInternalServerError, CodeInternal, "internal error"); return }
    out := make([]rotationRunDTO, 0, len(runs))
    for _, x := range runs {
        out = append(out, rotationRunDTO{
            ID: x.ID, StartedAt: x.StartedAt.Format(time.RFC3339), EndedAt: x.EndedAt.Format(time.RFC3339),
            Status: x.Status, Error: x.Error, ConfigVersion: x.ConfigVersion, AttemptNum: x.AttemptNum,
        })
    }
    var next *int64
    if len(runs) == limit && limit > 0 { last := runs[len(runs)-1].ID; next = &last }
    writeJSON(w, http.StatusOK, rotationRunsResponse{Runs: out, NextCursor: next})
}
```

Match the EXACT authZ call (permission constant + scope object) that `handleRotationGet` uses — copy it so a caller who can GET the policy can GET its runs, and one who can't gets the same 403.

- [ ] **Step 2: Add a shared `parseRunsPaging`** (in `rotation_handlers.go` or a shared api helper): reads `limit` (default 50, clamp 1–100) and `cursor` (int64 ≥ 0, default 0), writing a 400 on bad input and returning `ok=false`. Mirror the audit-events parsing.

- [ ] **Step 3: Add `handleSyncRuns`** to `sync_handlers.go` — identical shape with `keys_count` added to the DTO, `s.store.Sync().ListRuns`, and sync's authZ/scope.

- [ ] **Step 4: Register routes** in `server.go` next to the existing engine routes:

```go
r.Get("/v1/rotation/policies/{id}/runs", s.handleRotationRuns)
r.Get("/v1/sync/targets/{id}/runs", s.handleSyncRuns)
```

- [ ] **Step 5: Handler tests** (mirror an existing rotation handler test): seed a policy + a couple runs, assert `GET …/runs` returns them newest-first with `next_cursor`, `limit` clamps, and an unauthorized token gets 403. Run `go test ./internal/api/ -run 'Runs'` green.

- [ ] **Step 6: Commit.** `git add internal/api/ && git commit -m "feat(api): GET /v1/{rotation,sync}/.../runs run-history endpoints"`

---

### Task 6: PR 1 verification + leak test + open PR

**Files:** extend the leak test; no product changes unless a gate fails.

- [ ] **Step 1: Extend the secret-leak test** (find the existing grep/log leak test, e.g. `internal/…/leak_test.go`): run a rotation and a sync with a known secret value, then assert that value does NOT appear in any `rotation_runs.error`/`sync_runs.error` row (query the tables) — runs must be value-free.
- [ ] **Step 2: Full backend gate.** `make test` (all Go + testcontainers), `govulncheck ./...`, `gosec ./...` (or the project's `make` targets) — all green. Fix any finding.
- [ ] **Step 3:** Confirm migrations reversible: apply then roll back 000014/000013 in a scratch DB if the toolchain supports it; else re-read the `.down.sql`.
- [ ] **Step 4: Commit** any test/fix, then **push + open PR 1** (base `main`): title "Ops run-history backend — rotation_runs/sync_runs + endpoints", body summarizing the tables, same-tx recording, 100-cap, endpoints, value-free guarantee. **Do NOT merge** — the user merges after review. After PR 1 merges, Phase 2 begins from the updated `main`.

---

# PHASE 2 — Frontend depth (ends with PR 2)

> Start Phase 2 from a branch off `main` AFTER PR 1 is merged (the endpoints must exist). If executing before merge, mock the endpoints in MSW per the DTO shapes in Task 5.

### Task 7: Lift `useRowSelection` to a shared module

**Files:** Move `web/src/secrets/useRowSelection.ts` → `web/src/lib/useRowSelection.ts` and `web/src/secrets/useRowSelection.test.tsx` → `web/src/lib/useRowSelection.test.tsx`; update the editor import in `web/src/secrets/SecretEditor.tsx`.

- [ ] **Step 1:** `git mv` both files to `web/src/lib/`. Update the import in `SecretEditor.tsx` from `./useRowSelection` to `../lib/useRowSelection`.
- [ ] **Step 2:** `cd web && npm test -- --run useRowSelection SecretEditor && npm run typecheck` — all green (behavior identical; only the path changed).
- [ ] **Step 3:** Commit. `git commit -m "refactor(web): lift useRowSelection to shared lib for reuse"`

---

### Task 8: `OpsTable` — sortable headers

**Files:** Modify `web/src/operations/ops-ui.tsx`. Test: `web/src/operations/ops-ui.test.tsx`.

**Context:** `OpsTable` currently takes `columns: string[]` and renders row `children`. Add optional sorting: a column can be `{ label, key }` sortable or a plain string. Keep backward compatibility by accepting `columns: Array<string | OpsColumn>`. Sorting itself is done by the PANEL over its row data; `OpsTable` only renders sortable header buttons + carets and calls `onSort`.

- [ ] **Step 1: Write failing test** (`ops-ui.test.tsx`): render `OpsTable` with a sortable column and assert clicking its header calls `onSort('status')` and the active caret shows.

```tsx
test('OpsTable sortable header calls onSort', async () => {
  const onSort = vi.fn()
  render(
    <table><OpsTableHeader columns={[{ label: 'Status', key: 'status' }]} sort={null} onSort={onSort} /></table>,
  )
  await userEvent.click(screen.getByRole('button', { name: /sort by status/i }))
  expect(onSort).toHaveBeenCalledWith('status')
})
```

(If splitting a `OpsTableHeader` export is awkward, test through the full `OpsTable` with the loading/empty flags off and a dummy row child.)

- [ ] **Step 2: Implement.** Add:

```tsx
export type OpsColumn = { label: string; key: string }
export type OpsSort = { key: string; dir: 'asc' | 'desc' } | null
```

Extend `OpsTable` props with `sort?: OpsSort` and `onSort?: (key: string) => void`; render each column: if it's an `OpsColumn` and `onSort` is provided, a header `<button aria-label={`sort by ${label.toLowerCase()}`}>` with a `ChevronUp`/`ChevronDown` (lucide) when active; else the current plain `<th>`. Token classes only (mirror the editor `HeaderCell`: `text-ink-faint`, active `text-brand-text`).

- [ ] **Step 3: Run — PASS.** `cd web && npm test -- --run ops-ui`
- [ ] **Step 4: Commit.** `git commit -m "feat(web/ops): OpsTable sortable column headers"`

---

### Task 9: Rotation & Sync panels — sort + selection + bulk bar + run drill-in

**Files:** Create `web/src/operations/OpsSelectionBar.tsx`, `web/src/operations/RunHistorySheet.tsx`; Modify `web/src/operations/endpoints.ts`, `RotationPanel.tsx`, `SyncPanel.tsx`. Tests alongside.

**Context:** These two panels share the same shape (project-scoped rows with status/pause/resume/run-now/delete). Read `RotationPanel.tsx` for the current row rendering + per-id action calls (`opsEndpoints.rotation.*`). Use the lifted `useRowSelection`.

- [ ] **Step 1: endpoints** — add run wrappers + types to `endpoints.ts`:

```ts
export interface RunView { id: number; started_at: string; ended_at: string; status: 'success' | 'failure'; error?: string; config_version?: number; attempt_num: number; keys_count?: number }
export interface RunsPage { runs: RunView[]; next_cursor: number | null }
// under opsEndpoints.rotation:
runs: (policyId: string, cursor?: number) =>
  api.get<RunsPage>(`/v1/rotation/policies/${policyId}/runs${cursor ? `?cursor=${cursor}` : ''}`),
// under opsEndpoints.sync:
runs: (targetId: string, cursor?: number) =>
  api.get<RunsPage>(`/v1/sync/targets/${targetId}/runs${cursor ? `?cursor=${cursor}` : ''}`),
```

- [ ] **Step 2: `OpsSelectionBar.tsx`** — generic bar (mirror the editor's `SelectionBar`): `{ count, actions: Array<{label, onClick, tone?}>, onClear }`. Renders `N selected` + kit `Button`s. Destructive actions pass `tone="danger"` and the caller wraps them in a `ConfirmDialog`.

- [ ] **Step 3: `RunHistorySheet.tsx`** — props `{ open, onOpenChange, title, load }` where `load = (cursor?: number) => Promise<RunsPage>`. Renders a table: When (`RelTime`), Status (`StatusPill`), Duration (`ended_at−started_at` formatted), Cfg (`v{config_version}` or `—`), Attempt; a "Load more" button when `next_cursor`. On a 403 from `load`, show an access hint (mirror `OpsTable`'s forbidden state) instead of erroring.

- [ ] **Step 4: Wire `RotationPanel`** — add: `sort` state + `cycleSort` (reuse the editor's cycle logic), sort the rows client-side (default: failing first — `status==='failed'` rank, then next_run); `useRowSelection` + checkbox column (pass selection props into the rows/OpsTable); render `OpsSelectionBar` when `count>0` with actions Pause (`PATCH status:'paused'`), Resume (`PATCH status:'active'`), Rotate now (`POST rotate`), Delete (ConfirmDialog → `DELETE`) — each a `Promise.allSettled` fan-out over selected ids, then `qc.invalidateQueries(['ops','rotation'])` and a summary toast `Paused 3 · 1 failed`; a per-row "Runs" icon button opening `RunHistorySheet` with `load={(c) => opsEndpoints.rotation.runs(id, c)}`. Prune selection to visible; clear after an action.

- [ ] **Step 5: Wire `SyncPanel`** identically (Sync now instead of Rotate now; `opsEndpoints.sync.runs`).

- [ ] **Step 6: Tests** — selection + bulk (assert N per-id calls via MSW + summary toast + ConfirmDialog on delete), sort reorders, RunHistorySheet renders mocked runs + 403 hint. `cd web && npm test -- --run RotationPanel SyncPanel && npm run typecheck`.

- [ ] **Step 7: Commit.** `git commit -m "feat(web/ops): rotation+sync sort, bulk ops, run-history drill-in"`

---

### Task 10: Dynamic panel — sort + bulk delete-role + lease bulk-revoke

**Files:** Modify `web/src/operations/DynamicPanel.tsx`, `web/src/operations/LeasesSheet.tsx`. Tests alongside.

- [ ] **Step 1: `DynamicPanel`** — add sort (role name / project / TTL) + `useRowSelection` + checkbox column + `OpsSelectionBar` with a single action Delete role (ConfirmDialog → `DELETE` fan-out over selected role ids), invalidate `['ops','dynamic']`, summary toast. Roles are stateless — no pause/resume/run.
- [ ] **Step 2: `LeasesSheet`** — add `useRowSelection` over the leases + a bulk Revoke action (ConfirmDialog → `POST …/revoke` fan-out over selected lease ids), invalidate the leases query, summary toast. Keep the existing per-lease renew/revoke.
- [ ] **Step 3: Tests** — dynamic bulk delete-role (N calls + confirm), lease bulk revoke. `cd web && npm test -- --run DynamicPanel LeasesSheet && npm run typecheck`.
- [ ] **Step 4: Commit.** `git commit -m "feat(web/ops): dynamic role bulk-delete + lease bulk-revoke"`

---

### Task 11: Health strip

**Files:** Create `web/src/operations/HealthStrip.tsx`; Modify `web/src/operations/OperationsPage.tsx`. Test alongside.

**Context:** The strip calls the same `useRotation(filter)`/`useSync(filter)`/`useDynamicRoles(filter)` aggregators the tabs use (TanStack Query dedupes, so no extra network). It derives counts from `rows`. Dynamic segment shows role count only (no per-role lease fan-out — see spec).

- [ ] **Step 1: Failing test** (`HealthStrip.test.tsx`): given mocked aggregates with 2 failing rotation policies, assert the strip renders "2 failing" with the danger token and, on click, switches to the rotation tab with a status sort (assert the `onSelect`/URL effect).
- [ ] **Step 2: Implement `HealthStrip`** — three segments (Rotation / Sync / Dynamic). Rotation/Sync: count `rows` by `status` (`active`/`failed`/`paused`) → `N active · N failing · N paused`, failing in `text-danger` with an `AlertTriangle` when `>0`. Dynamic: `N roles`. Each segment is a button that sets the tab (`setParams tab=`) and, for a failing count, the panel's sort to status. Skeleton while loading; hide a segment's error silently (403-tolerant like the tabs). Token-only.
- [ ] **Step 3: Render** `<HealthStrip filter={filter} onGo={(tab, sort) => …}/>` above the tablist in `OperationsPage`, threading the tab/sort setters. Keep it compact (one row, wraps on narrow).
- [ ] **Step 4:** `cd web && npm test -- --run HealthStrip && npm run typecheck`. Commit `feat(web/ops): health overview strip (current-state counts)`.

---

### Task 12: PR 2 verification + gaps.md + open PR

- [ ] **Step 1: Full gate.** `cd web && npm test -- --run` (all pass), `npm run typecheck`, `npm run build`, `npm test -- --run no-raw-palette dark-aa no-legacy-alias`.
- [ ] **Step 2: Dual-theme smoke.** `npm run preview -- --port 4180 --host 127.0.0.1 &` then `SMOKE_URL=http://127.0.0.1:4180 npm run smoke` (light + dark, dark bg `rgb(11,10,20)`). Stop preview.
- [ ] **Step 3: SECURITY re-check.** `git diff main...HEAD -- web/src | grep -iE "decrypt|datakey|plaintext|reveal"` → only the pre-existing dynamic once-issued-password path (unchanged). Confirm run-history rows render only status/time/category/version (value-free); bulk ops call per-id endpoints only.
- [ ] **Step 4: Update `gaps.md` §2.2** — mark done: column sorting, bulk pause/resume/delete, run-history timeline (+ note dynamic reuses leases), health overview. Leave open: per-role lease-health aggregate (deferred), dynamic role-update-in-UI if still open. Commit.
- [ ] **Step 5: Push + open PR 2** (base `main`), body summarizing sorting/bulk/health/run-history + the preserved security posture + that it consumes PR 1's endpoints. **Do NOT merge** — user merges after final holistic review.

---

## Self-review notes
- **Spec coverage:** run-history tables+recording+cap = Tasks 1–4; endpoints = Task 5; leak/value-free = Tasks 4/6; sorting = Task 8+9/10; bulk ops (rotation/sync pause/resume/run/delete, dynamic delete-role, lease revoke) = Tasks 9/10; health strip = Task 11; run-history drill-in (dynamic reuses LeasesSheet) = Task 9/10; useRowSelection lift = Task 7.
- **Type consistency:** `RotationRunInput`/`RotationRun` (Task 2) reused in Task 3; `RunView`/`RunsPage` (Task 9) match the Go `rotationRunDTO`/`rotationRunsResponse` (Task 5) wire shapes; `OpsColumn`/`OpsSort` (Task 8) consumed by panels (Task 9/10); shared `RunHistoryCap` defined once (Task 2), reused by sync (Task 4).
- **Phase gate:** Phase 2 starts only after PR 1 merges (endpoints exist); if pipelined, MSW mocks stand in per the Task 5 DTOs.
- **Scope discipline:** dynamic reuses leases (no new table/endpoint); health strip does NOT fan out leases; bulk reuses audited per-id endpoints (no bulk endpoint); run rows value-free.
- **Signature-change ripple:** `MarkRotated`/`MarkFailure`/`MarkSynced`/`MarkFailure(sync)` gain `startedAt`+`attemptNum` — Tasks 3/4 explicitly update ALL call sites AND existing tests that use the old signatures (a compile-break otherwise).
