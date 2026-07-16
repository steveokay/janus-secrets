# Promotion approval workflow (Phase B) — design

**Status:** approved (brainstorm 2026-07-16)
**Tracker:** `gaps.md` priority item #11; the Phase B follow-up noted in the env→env promotion work.
**Scope:** A request → review → approve/apply workflow layered over the existing Phase-A promotion, for users who lack `secret:promote` on a target environment. Backend service + store + migration + REST + CLI + web UI. Reuses the existing `promote.Apply` on approval.

## Overview

Phase A lets a user with `secret:promote` on the target environment promote a
config's secrets forward (dev→staging→prod) directly. Phase B adds the path for
users who **lack** that capability on the target: they file a **promotion
request** (a value-free, version-pinned selection of keys to promote), and a
user who **does** hold `secret:promote` on the target reviews it and, on
**approve**, the promotion is applied immediately under the approver's
authority. This realizes the common change-control pattern "developers freely
promote dev→staging; promoting into prod requires an admin/owner's approval."

The request stores only key names, actions, and a pinned source version — never
plaintext values. On approval the existing `promote.Apply` re-resolves and
re-encrypts exactly what was reviewed.

## Goals

- Let a developer without target `secret:promote` file a version-pinned,
  value-free promotion request.
- Let an approver (holder of target `secret:promote`) review and approve
  (apply immediately) or reject a request; let the requester cancel.
- Enforce four-eyes: an approver is never the requester.
- Surface it across REST, the `janus promote` CLI, and the promotion UI.
- Preserve value-safety and the hash-chained audit contract.

## Trigger model (decided)

**Capability-gap.** Approval is the path for users who lack `secret:promote`
on the target env. Users who have it still promote directly (Phase A is
**unchanged**). No policy-gate/four-eyes-on-privileged-targets, no forcing
privileged users through the gate.

## RBAC

- **`promotion:request`** — a **new** action, granted to **developer, admin,
  owner** (not viewer), scoped to the **source** environment. The requester's
  standing to file comes from being a developer in the source config; they also
  need `secret:read` on the source config (they must see which keys exist).
  Scoping to source (not target) is deliberate: a target-scoped developer would
  already hold `secret:promote` there and never need to request.
- **Approve / reject** — requires the existing **`secret:promote`** on the
  **target** environment (exactly the capability the requester lacks).
- **Self-approval forbidden** — `decided_by` must differ from `requested_by`,
  unconditionally (four-eyes even if the requester happens to hold target
  rights).
- **Cancel** — only the requester (`requested_by == caller`) may cancel their
  own `pending` request.
- **List / get** — an approver (holder of target `secret:promote`) sees
  requests targeting envs they can approve; a requester sees their own. Deny by
  default; value-free responses.

Wire `promotion:request` into `internal/authz`: add the action constant and
include it in `developerActions` (so developer/admin/owner inherit it), with a
matrix test mirroring the existing `SecretPromote` rows.

## Data model

New migration `000021_promotion_requests` adds table `promotion_requests`
(value-free by construction):

| column | type | notes |
|---|---|---|
| `id` | uuid pk | |
| `project_id` | uuid | for scoping/filtering + authz |
| `source_config_id` | uuid | |
| `source_version` | int | **pinned**; the version previewed at request time |
| `target_config_id` | uuid null | null when creating the target |
| `target_env_id` | uuid | required (target env for authz + create) |
| `target_name` | text | config name when `create_target` |
| `create_target` | bool | mirrors Phase-A `ApplyRequest.CreateTarget` |
| `selections` | jsonb | `[{ "key": "...", "action": "set"\|"remove" }]` — **key names + action only, never values** |
| `note` | text | requester justification (non-secret metadata) |
| `status` | text | `pending` \| `applied` \| `rejected` \| `cancelled` |
| `requested_by` | uuid | user id |
| `decided_by` | uuid null | approver/rejecter |
| `decision_note` | text null | approver's reason (esp. on reject) |
| `applied_target_version` | int null | set on apply |
| `created_at` | timestamptz | |
| `decided_at` | timestamptz null | |

Indexes: `(project_id, status)` and `(target_env_id, status)` for the approver
queue; `(requested_by, status)` for "my requests".

Add `store.PromotionRequestRepo` with: `Create`, `Get`, `List(filter)`, a
transactional **`Decide(ctx, id, to, decidedBy, note)`** for the pure status
transitions (`reject`/`cancel`) that compare-and-sets `pending → to` (returning
a not-`pending` error the handler maps to `409`), and an approve helper that
runs inside a tx which **row-locks** the `pending` request (`SELECT … FOR
UPDATE`), lets the caller perform the promotion, and then marks
`applied`+`applied_target_version` — so a racing approve blocks and the promotion
applies at most once.

## Service + state machine

Extend `internal/promote` with request lifecycle logic reusing the existing
`Service.Apply`. State machine: `pending → applied | rejected | cancelled`
(terminal). Transitions:

- **Create** — validate the requester has `promotion:request` (source env) +
  `secret:read` (source config); validate the step is the pipeline's next hop
  (reuse `validateStep`); snapshot the current source version as `source_version`
  and persist the selection + note as `pending`.
- **Approve** — authorize `secret:promote` on the target env; assert
  `caller != requested_by`. Acquire a **row lock** on the request
  (`SELECT … FOR UPDATE`) and confirm it is still `pending` — this is the
  concurrency guard (a racing approver blocks, then sees non-`pending` → `409`).
  Run the existing `promote.Apply` with an `ApplyRequest` built from the stored
  request (`SourceVersion` = pinned, `Selections`, target coords, `Actor` =
  approver). **Only on Apply success** set `status=applied`, `decided_by`,
  `decided_at`, and `applied_target_version` (from `ApplyResult`) and commit; on
  Apply error, leave the row `pending` (no status change) and return the error.
  This ordering avoids an "applied" row whose promotion didn't happen. Source is
  deterministic (config versions are immutable); target-side drift (a now-locked
  key, a deleted/soft-deleted target) surfaces via Apply's existing `Skipped`
  list or a clean error.
- **Reject** — authorize `secret:promote` (target); `caller != requested_by`;
  `Decide(pending→rejected)` with `decision_note`.
- **Cancel** — `caller == requested_by`; `Decide(pending→cancelled)`.

## API (REST)

Mounted in the existing `if s.promote != nil { … RequireAuth … }` group in
`server.go`; each handler does its own `s.authorize(...)` with a value-free
audit action.

- `POST /v1/promote/requests` — body `{ source_config_id, target_config_id?,
  target_env_id, target_name?, create_target, selections:[{key,action}], note }`.
  → `201 { id, status:"pending", ... }`.
- `GET /v1/promote/requests` — query `?project=&status=&mine=true`. Value-free
  metadata list (cursor pagination consistent with other lists).
- `GET /v1/promote/requests/{id}` — detail incl. the value-free diff (key names
  + change type via `Preview` on the pinned version) and the note.
- `POST /v1/promote/requests/{id}/approve` — → `200 ApplyResult`
  (`target_version`, `applied[]`, `skipped[]`).
- `POST /v1/promote/requests/{id}/reject` — body `{ note? }`.
- `POST /v1/promote/requests/{id}/cancel`.

Idempotency-Key honored on the mutating verbs via the existing middleware.

## CLI

Extend the existing `janus promote` group:

```sh
janus promote request --to <env> [--key K --key K … | --all] [--note "…"]   # POST …/requests
janus promote requests [--status pending] [--mine]                          # GET  …/requests
janus promote approve <id>                                                   # POST …/{id}/approve
janus promote reject  <id> [--note "…"]                                      # POST …/{id}/reject
janus promote cancel  <id>                                                   # POST …/{id}/cancel
```

`request` resolves source from the binding (`.janus.yaml`/flags) like the
existing promote verbs; approve prints the resulting `ApplyResult` summary
(counts only, value-free). Destructive-ish verbs (`reject`/`cancel`) TTY-confirm
unless `--yes`.

## Web UI

Within the existing promotion surface (Nocturne tokens, no new style):

- A **Requests** view: a queue of `pending` requests targeting environments the
  viewer can approve, plus a "My requests" section (the viewer's own filed
  requests with status).
- A **review** panel: the value-free diff (added/changed/removed key names) +
  the requester's note, with **Approve** / **Reject** actions (reject captures a
  note). Approve shows the `ApplyResult` (counts, skipped keys).
- A **"Request approval instead"** path: when a user without target
  `secret:promote` opens the promote flow (or hits 403 on direct apply), offer
  filing a request with the same key-selection UI they'd use to promote.
- Discovery: a pending-count badge on the promotion nav entry (polled via the
  list endpoint) — no push notifications.

## Value-safety & audit

- Requests persist only key names + actions + a source-version pin. `note` /
  `decision_note` are non-secret justification metadata (documented as
  not-for-values; treated as non-secret).
- Approve-time `Apply` re-resolves and re-encrypts under the approver — values
  are never stored in the request, its list/get responses, or audit.
- Each transition writes a hash-chained, value-free audit event:
  `promotion.request.create`, `promotion.request.approve`,
  `promotion.request.reject`, `promotion.request.cancel` (actor, request id,
  source/target paths, key names, result). Approve additionally produces the
  existing promote-apply audit trail.
- A dedicated leak test asserts no secret value appears in request storage, any
  request API response, or audit output.

## Error handling

- Unknown/again-decided request → `409` (status not `pending`), surfaced from
  the `Decide` compare-and-set; never a double-apply.
- Missing capability → `403` (deny by default), value-free message.
- Approve when the target config was deleted, the pinned source version is gone,
  or the step is no longer valid → the `Apply` error is returned and the
  transition is rolled back; the request remains `pending` so it can be
  re-approved after the target is fixed, or cancelled. (No separate `failed`
  status — keeps the state machine minimal.)
- Self-approval attempt → `403` with a clear "cannot approve your own request".

## Files

**New:**
- `migrations/000021_promotion_requests.{up,down}.sql`
- `internal/store/promotion_request_repo.go` (+ test)
- `internal/promote/requests.go` — request lifecycle service (+ tests)
- `internal/api/promotion_request_handlers.go` (+ e2e + leak tests)
- `cmd/janus/promote_request_commands.go` (+ test) — or extend the existing
  promote command file if small.
- `web/src/…/promotion/Requests*.tsx` (+ tests) — queue + review + request-instead.

**Modified:**
- `internal/authz/actions.go` (add `PromotionRequest`; include in
  `developerActions`) + matrix test.
- `internal/api/server.go` (mount the six request routes in the promote group).
- `cmd/janus/promote_commands.go` (register the new subcommands).
- `web/src/…/promotion/*` (nav badge, "request instead" entry).

## Testing

- authz matrix: `promotion:request` = false for viewer, true for developer/
  admin/owner (mirrors `SecretPromote`).
- lifecycle: create→approve applies (asserts target version bumped, correct
  keys); create→reject; create→cancel; each writes the right audit event.
- four-eyes: approver == requester → `403`; requester cancel allowed, approver
  cancel not.
- concurrency: two approves race → exactly one applies, the other `409`.
- drift: pinned source version applied even after source advances; a
  target-locked key is skipped; deleted target → clean error, request stays
  `pending`.
- value-safety: leak test over request store + all request endpoints + audit.
- migration up/down; store repo tests against real Postgres (testcontainers).
- CLI tests (httptest harness) for the five subcommands.
- web tests for the queue/review/request-instead flows (both themes via smoke).

## Non-goals (YAGNI)

- No push notifications (email/webhook) — discovery via the UI queue + badge.
- No policy-gate / four-eyes-on-prod / forcing privileged users through the gate
  (the rejected trigger option).
- No multi-approver quorum — a single approval suffices.
- No approve-then-scheduled-apply — approve applies immediately.
- No request TTL/expiry and no editing a request (cancel + refile).
- No change to Phase-A direct promotion behavior.
