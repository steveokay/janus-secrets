-- Scoped audit read (RBAC at organisation scale, item 3).
--
-- Every audit endpoint authorized against the INSTANCE scope, so a team lead
-- could not review their own project's trail without being handed every event
-- in the organisation — and audit rows carry resource paths and key names, so
-- that leaked the shape of every other team's secrets. It cut the other way
-- too: teams that should be self-auditing simply could not.
--
-- Filtering by project cannot be derived from the resource text: it is
-- free-form (`configs/<cid>/secrets`, `project/<pid>/members/<uid>`,
-- `groups/<gid>`, `auth/oidc`, …) and a prefix/LIKE scheme would silently
-- mis-scope events. For an audit view that is the worst possible failure — it
-- would LOOK complete. So the scope is recorded on the event at write time.
--
-- CRITICAL: project_id is deliberately NOT part of the chain hash.
-- internal/audit/hash.go's computeHash covers a fixed field list under the
-- `janus:audit:v1` domain tag. Adding a hashed field would invalidate every
-- existing event and break GET /v1/audit/verify on upgrade. Keeping it outside
-- means verification is unaffected — and it means this column is an INDEX, NOT
-- EVIDENCE: someone with direct database access could re-point an event to hide
-- it from a SCOPED view. That actor is already outside the threat model, and
-- the instance-wide view remains complete, so tamper-evidence is unweakened.
-- Do not let a scoped view be mistaken for evidence in its own right.
--
-- NULL means "not attributable to a project" and is visible ONLY to an
-- instance-wide reader. That covers instance-level actions (login, seal, user
-- and group management) and every event written BEFORE this migration, so a
-- team's scoped history starts at the upgrade rather than being backfilled with
-- guesses. Fail-closed in both directions: instance events never leak into a
-- project view, and nothing is invented for the past.
--
-- DELIBERATELY NOT A FOREIGN KEY. An audit log is a historical record and must
-- outlive the entity it describes; constraining it to rows that still exist is
-- the wrong model in two concrete ways:
--
--   1. ON DELETE SET NULL would MUTATE an append-only table, erasing an event's
--      attribution at the moment a project is destroyed — exactly when the
--      trail matters most.
--   2. It creates a write-ordering hazard. handleProjectDestroy deletes the
--      project and THEN records project.destroy; with an FK that insert
--      references a row that no longer exists and fails, so destroying a
--      project would 500.
--
-- A dangling id is harmless here. Scoped reads filter by the project ids the
-- caller can read, and a destroyed project is readable by nobody, so its events
-- match no scoped filter — they behave exactly like NULL for scoped readers
-- while preserving the historical attribution for instance-wide ones. Project
-- ids are UUIDs and never reused, so a dangling id can never come to mean a
-- different project.
ALTER TABLE audit_events
    ADD COLUMN project_id uuid;

-- The scoped read is "this project's events, newest first", which is the same
-- ordering the unscoped list uses.
CREATE INDEX audit_events_project_seq_idx
    ON audit_events (project_id, seq DESC)
    WHERE project_id IS NOT NULL;
