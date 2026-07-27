# Scoped audit read — design

**Date:** 2026-07-27
**Tracker:** `status.md` → "Open — RBAC at organisation scale", item (3).
**Status:** designed, **not implemented.** Written after items (1) and (2)
shipped, so the next session starts from decisions rather than re-deriving them.

## Problem

Every audit endpoint — `verify`, `events`, `histogram`, `export` — authorizes
against `authz.Instance()`. So a team lead cannot review their own project's
trail without being handed **every event in the organisation**, and audit rows
carry resource paths and key names, which leaks the shape of every other team's
secrets. It also cuts the other way: teams that should be self-auditing simply
cannot.

This is now the **only** place the isolation story leaks. Projects are invisible
without a binding, groups manage access at team scale, and teams create their
own projects — but the audit log is still all-or-nothing.

## Why this is not a small change

`audit_events` has no project column, and the resource string is free-form
(`configs/<cid>/secrets`, `project/<pid>/members/<uid>`, `groups/<gid>`,
`auth/oidc`, …). Filtering by project therefore cannot be done from the resource
text without a brittle prefix/`LIKE` scheme that would silently mis-scope
events — the worst outcome for an audit view, because it would look complete.

There are **144 `record()` / `recordActor()` call sites** in `internal/api`, so
threading a project id through each one by hand is a large, error-prone diff.

## Decisions

### 1. Store the project scope on the event, outside the hash

Add a nullable `project_id` to `audit_events` (migration `000048`), indexed for
the filter.

**It must NOT enter the chain hash.** `computeHash` in `internal/audit/hash.go`
covers a fixed field list under the `janus:audit:v1` domain tag; adding a field
would invalidate every existing event's hash and break `GET /v1/audit/verify` on
upgrade. Keeping it outside means:

- verification is unaffected, for existing and new events alike;
- the column is an **index, not evidence**. Someone with direct database access
  could re-point an event and hide it from a scoped view — but that actor is
  already outside the threat model (`docs/threat-model.md` §5, "Protects the
  host, container and Postgres"), and the **instance-wide view remains
  complete**, so tamper-evidence is not weakened.

Document that property explicitly; do not let a scoped view be mistaken for
tamper-evident evidence in its own right.

### 2. Capture the scope at authorization time, not at record time

An event's project scope **is** the scope its operation was authorized against.
So `s.can` / `s.authorize` stash the resolved `Resource.ProjectID` on the
request context, and `recordActor` reads it. One change in two places, and every
handler that authorized against a project-scoped resource is covered
automatically.

This is principled rather than magical, but it has one failure mode worth
guarding: a handler that authorizes against resource A and then records an event
about resource B would attribute the event to A. Audit that with a test that
walks handlers, or accept it and document it — do not leave it unexamined.

Instance-scoped operations (login, seal, user and group management) legitimately
have **no** project, and stay `NULL`.

### 3. NULL means instance-only, and that is fail-closed

An event with no project scope is visible **only** to an instance-wide reader.
Two consequences, both correct:

- Events written **before** the upgrade all have `NULL`, so they never appear in
  a scoped view. A team's history starts at the upgrade. State this in the guide
  rather than backfilling with guesses.
- Instance-level events never leak into a project view.

### 4. Scoped read is project-level only

`audit:read` is already in `adminActions`, so a project admin holds it at
project scope today — no new action is needed. An **environment**-scoped
binding confers nothing here, because only the project is recorded. Document
that; do not silently widen it.

### 5. `verify` stays instance-only

The hash chain covers all events; a subset cannot be verified. `verify`,
`checkpoint` and `prune` keep their current instance/owner gating.

### 6. Filter in the query

Compute the caller's readable project set. Instance `audit:read` → unrestricted.
Otherwise `WHERE project_id = ANY($n)`, with an empty set → `403`.

**Apply it inside the SQL, never after paging** — a post-filter would let the
keyset cursor skip rows and silently truncate a team's trail.

## Surface

- Migration `000048`: `audit_events.project_id uuid NULL` + index.
- `audit.Event` gains `ProjectID *string`; the store append writes it.
- `s.can`/`s.authorize` stash scope; `recordActor` reads it.
- `events`, `histogram`, `export` accept the scope filter; `verify` unchanged.
- Audit screen: for a non-instance reader, show the scoped view and say so —
  "showing events for the 2 projects you can read", never an unqualified
  "Audit ledger".
- Docs: `operations.md` (permission table + the not-evidence property),
  `guides/` audit section, `threat-model.md` (the index-vs-evidence distinction),
  `data-model.md`, both trackers, CHANGELOG.

## Testing

- A project admin sees their project's events and **not** another project's.
- Instance admin's view is byte-identical to today.
- Pre-upgrade (`NULL`) events never appear in a scoped view.
- Cursor pagination across a scoped filter neither skips nor repeats — seed more
  events than one page and walk every page.
- `GET /v1/audit/verify` still passes with `project_id` populated, proving the
  column is outside the hash.
- An environment-scoped admin gets `403`, not a partial view.
