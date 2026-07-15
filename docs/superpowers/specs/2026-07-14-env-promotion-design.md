# Environment-to-environment secret promotion — design

**Status:** approved for spec (2026-07-14). Phase A specified in full; Phase B (review/approval workflow) sketched at the end for continuity, to be its own spec+plan later.

## Goal

Let a user propagate secrets from one environment to the next along a project's release pipeline (dev → staging → prod) through a **point-in-time, per-key selectable diff** — never a blind overwrite. Promotion is driven from the web UI (drag a config downstream, review a diff, promote), the REST API, and the CLI.

## What promotion is — and is not

- **Promotion is a point-in-time copy.** It reads the source config's stored values at a pinned version and applies the selected ones to the target config as **one new config version** (so it inherits Janus's versioning, diff, rollback, and audit). After promotion the target's values live independently — changing the source later does not change the target.
- **It is not the same as references.** Janus already has live cross-env references (`${projects.p.dev.KEY}`) that re-resolve at read time. Promotion copies the **raw stored value verbatim** — if a source value *is* a reference string, it promotes as that reference string (it is not resolved-and-baked). A self-referential env reference therefore keeps pointing at its original env after promotion; the diff shows the raw `${…}` so the operator sees this.
- **It is not inheritance.** Config inheritance (root + branch configs) is within one environment. Promotion moves values across environments within the same project.
- **It is within a single project.** Environments live inside a project, so source and target share one project KEK. Cross-*project* movement is out of scope (use references).

## Core concepts

1. **Release pipeline** — a per-project **ordered** list of environments (e.g. `[dev, staging, prod]`). Promotion is allowed only to the **immediately next** environment in the list (forward, adjacent). `dev→staging` and `staging→prod` are legal; `dev→prod` (skip) and any backflow (`prod→dev`) are rejected. A project must have a pipeline configured before promotion is enabled.
2. **Promotion** — moving selected keys from a **source config** to a **target config** (default: the same config *name* in the next environment). If the target config does not exist, promotion offers to create it.
3. **Locked keys** — an admin can mark specific keys on a config as **locked**. A locked key on the *target* config can never be overwritten or removed by a promotion (hard-blocked in the diff). This protects deliberately env-specific values (prod `DATABASE_URL`, per-env secret keys) from accidental clobbering.

## Data model (new)

Two small tables (migration `000016`). Both reach a project via `environments` (recall: `configs` has no `project_id`; a config → its `environment_id` → `environments.project_id`).

```sql
-- Ordered release pipeline per project. position is 0-based; env unique per project.
CREATE TABLE promotion_pipeline_steps (
    project_id     uuid    NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    position       integer NOT NULL,
    environment_id uuid    NOT NULL REFERENCES environments (id) ON DELETE CASCADE,
    PRIMARY KEY (project_id, position),
    UNIQUE (project_id, environment_id)
);

-- Keys protected from promotion overwrite/removal, per (target) config.
CREATE TABLE config_locked_keys (
    config_id  uuid        NOT NULL REFERENCES configs (id) ON DELETE CASCADE,
    key        text        NOT NULL,
    created_by uuid        REFERENCES users (id),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (config_id, key)
);
```

Down migrations `DROP TABLE IF EXISTS …` (children → parents, no dependency issue).

Store repos (new `internal/store/promotion.go`):
- `PipelineRepo`: `Get(projectID) ([]PipelineStep, error)`, `Set(projectID, envIDs []string) error` (replace whole ordering in one tx), `NextEnv(projectID, envID) (string, bool, error)` (the env immediately after `envID`, or `false` if none/not in pipeline).
- `LockedKeyRepo`: `List(configID) ([]string, error)`, `Lock(configID, key, actor) error`, `Unlock(configID, key) error`, `AreLocked(configID, keys []string) (map[string]bool, error)`.

## Promotion engine (`internal/promote`)

A thin service holding `*secrets.Service`, the store repos, and the authz check surface. Two operations.

### Preview (the diff)

`Preview(ctx, sourceConfigID, targetConfigID) (Diff, error)`:
1. AuthZ: `secret:read` on both source and target (resolved to their envs).
2. Validate the step: source env → target env must be the legal next pipeline step (else `ErrIllegalStep`).
3. Read both configs' latest live state (`SecretRepo.GetLatest`), **decrypting both sides' values** (the chosen "values shown" model — this reveal is audited, see Audit). Compare **raw stored values**.
4. For each key in `union(source, target)` produce a `DiffEntry{ Key, Status, SourceValue, TargetValue, Locked }` where `Status ∈ {add, change, remove, same}`:
   - `add` — in source, absent in target
   - `change` — in both, raw values differ
   - `same` — in both, equal
   - `remove` — target-only (absent in source)
   - `Locked` — target key is in `config_locked_keys`
5. Return `Diff{ SourceVersion, Entries, TargetExists }`. `SourceVersion` pins the source config version so Apply is consistent with what the operator reviewed.

Default check state (UI hint, not enforced server-side): `add`/`change` checked, `remove` unchecked, `same` no action, **`Locked` unchecked and disabled**.

### Apply

`Apply(ctx, req ApplyRequest) (ApplyResult, error)` where `ApplyRequest{ SourceConfigID, TargetConfigID (or TargetEnvID+Name to create), SourceVersion, Selections []Selection, IdempotencyKey }` and `Selection{ Key, Action ∈ {set, remove} }`:
1. AuthZ: `secret:promote` on the **target** env + `secret:read` on the source. If the target must be created: also `config:create` on the target env.
2. Validate the pipeline step (same as Preview).
3. If target missing and creation requested → create the config (same name) first.
4. Reject the request if **any selected key is locked** on the target (`ErrLockedKey`, names the key) — defense in depth beyond the UI disabling.
5. Read source values at `SourceVersion`; for each `set` selection decrypt the source value (source config AAD, same project KEK) and stage a `secrets.SecretChange{Key, Value}`; for each `remove` selection stage a delete. Ignore keys whose selection no longer matches reality (drift): if a `set` key vanished from the source at that version, skip it and report it in `ApplyResult.Skipped`.
6. Apply the staged changes via `secrets.Service.SetSecrets(targetConfigID, changes, message="promote from <srcEnv> v<n>", actor)` → **one new target config version**. Plaintext values are ephemeral and zeroized by the existing set path.
7. Write the `secret.promote` audit event (below).
8. Return `ApplyResult{ TargetVersion, Applied []KeyAction, Skipped []string }`.

Idempotency: `Apply` honors an `Idempotency-Key` (destructive-ish mutation) so a retried promotion does not double-apply.

## Cryptography & security

- Source and target are in the same project → the **same project KEK** wraps both. Promotion decrypts each selected source value and re-encrypts it under the target config (fresh DEK, target AAD) via the existing set path. No cross-project KEK concerns; no ciphertext copy (the DEK AAD binds config-id + value-version, which differ in the target, so values must be re-encrypted).
- Requires the server **unsealed** (needs the project KEK); sealed → 503 like every secret op.
- **Value-free audit.** The `secret.promote` event records key **names** and actions, source env + version, target env + resulting version — never values. The preview's value reveal emits a `secret.reveal` event (key names + counts, no values in the log). Enforced by a leak test.
- Raw stored values are copied verbatim (references preserved). No plaintext in logs/errors.

## AuthZ (new actions)

Add to `internal/authz/actions.go`:
- `SecretPromote  Action = "secret:promote"` — **developer+**, scoped to the **target** environment. Executing a promotion needs `secret:promote` on target **and** `secret:read` on source (and `config:create` on target when creating the config).
- `PromotionManage Action = "promotion:manage"` — **admin+**, project-scoped. Gates managing the pipeline ordering and locking/unlocking keys.

Matrix impact (cumulative): `developerActions += SecretPromote`; `adminActions += PromotionManage`. Deny by default; a developer with no target-env grant cannot promote (this is the signal Phase B's request flow builds on).

## API (`internal/api`, all under `/v1`)

- `GET  /v1/projects/{pid}/pipeline` — the ordered env list. (`project:read`)
- `PUT  /v1/projects/{pid}/pipeline` — replace ordering `{ "environment_ids": [...] }`. (`promotion:manage`)
- `GET  /v1/configs/{cid}/locked-keys` — list locked keys. (`config:read`)
- `POST /v1/configs/{cid}/locked-keys` — `{ "key": "..." }` lock. (`promotion:manage`)
- `DELETE /v1/configs/{cid}/locked-keys/{key}` — unlock. (`promotion:manage`)
- `GET  /v1/promote/preview?from={cfg}&to={cfg}` — the diff. (`secret:read` on both) — audited reveal.
- `POST /v1/promote` — apply. Body `{ from_config, to_config | (to_env,to_name,create:true), source_version, selections:[{key,action}] }`; `Idempotency-Key` header. (`secret:promote` on target + `secret:read` on source)

Errors: illegal pipeline step → `409 pipeline_step_not_allowed`; locked key selected → `409 key_locked`; missing target without `create` → `404` with guidance; sealed → `503`; denials → `403`.

## Web UI (`web/`, Nocturne — matches the approved mockup)

Mockup: `docs/design/ui-promotion-mockup.html` (to be committed from the brainstorm artifact). All tokens/components from the Nocturne system; renders in both themes.

- **Pipeline board** — env columns rendered in pipeline order, env-coded (dev=info, staging=warn, prod=danger). A config card is **draggable**; on drag, the legal next env highlights as a drop target and non-adjacent/backward envs dim with a lock affordance. Dropping (or a **Promote →** button) opens the diff modal. Illegal targets are not droppable.
- **Diff modal** — per-key rows: checkbox · key (mono) · status chip (`add`/`change`/`remove`/`same`) · `from source` → `target now` values. Secret values masked with a reveal toggle (values were fetched in the audited preview; the toggle is display-only). Locked keys show a lock icon, pre-unchecked and disabled. `remove` rows unchecked by default. Live footer summary + a primary button that reflects the selection count; confirm → toast + the target board card bumps its version. A "will be created" banner when the target config is missing.
- **Locked-key management** — in the secret editor, an admin can lock/unlock a key (lock icon in the row); locked keys are surfaced in the diff.
- **Pipeline configuration** — an admin screen (or project settings section) to set the environment order.

## CLI (`cmd/janus`)

- `janus promote --from <env> --to <env> [--config <name>] [--key K ...|--all] [--include-removes] [--create-target] [--dry-run]` — `--dry-run` prints the diff (like the preview); without it, applies the selected keys. `--key` repeatable; `--all` selects all add/change (never locked, never removes unless `--include-removes`).
- `janus pipeline get|set <env> <env> …` — read/replace the project pipeline (admin).
- `janus secrets lock <KEY>` / `janus secrets unlock <KEY>` — manage locked keys on the bound config (admin).

## Testing

- **Diff correctness** — add/change/remove/same classification; raw-value comparison (a reference vs a literal); locked flag surfaced.
- **Apply** — produces a new target version; rollback of that version works; `set` and `remove` selections honored; drift (source key vanished) skipped and reported.
- **Crypto round-trip** — a promoted value decrypts correctly under the target config (same project KEK, re-encrypted, references preserved raw).
- **RBAC** — promote needs `secret:promote` on target + `secret:read` on source (each missing → 403); creating target needs `config:create`; pipeline/lock management needs `promotion:manage`.
- **Pipeline enforcement** — adjacent-forward only; skip (`dev→prod`) and backflow rejected; unconfigured pipeline → promotion disabled.
- **Locked keys** — a locked target key cannot be applied even if selected (server rejects with `ErrLockedKey`); never auto-removed.
- **Audit is value-free** — leak test drives a promotion with a sentinel value and asserts no value appears in any audit column/log; `secret.promote` records names + versions only.
- **Idempotency** — a retried apply with the same key does not double-apply.
- Store tests via testcontainers; TDD throughout.

## Phase B — review/approval workflow (separate spec later)

On top of Phase A: a user **without** `secret:promote` on the target can submit a **promotion request** (a pinned diff snapshot: source, target, source version, selected keys). An approver **with** `secret:promote` reviews the request and approves (which executes the promotion exactly as Apply) or rejects with a reason. New table `promotion_requests` (+ status: pending/approved/rejected/applied), a new `promotion:request` action (viewer/developer), an **inbox** UI for approvers, and notifications. Requests capture the diff at submission time and re-validate (locked keys, pipeline, drift) at approval. Out of scope for Phase A.

## Non-goals

- Cross-*project* promotion (envs are within a project; use references).
- Scheduled/automatic promotion.
- Bidirectional or non-adjacent promotion (pipeline is forward + adjacent).
- Merging/3-way conflict resolution beyond per-key selection.
