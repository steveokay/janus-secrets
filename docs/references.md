# Config inheritance & secret references

Two read-time composition features over the project → environment → config →
secret hierarchy. **Inheritance** lets a config draw its values from a base
config in the same environment; **references** let a secret value embed the
value of another secret, resolved when the config is read. They compose:
inheritance is applied first to build a config's effective key set, then
references are expanded over the resulting values.

Both are pure read-time resolution — nothing is denormalized or copied at write
time. The engine lives in `internal/resolve`, a pure package that composes over
a raw-value reader (`internal/secrets`) and an authorization port
(`internal/api`); the API layer owns authz and audit.

## Inheritance

A config may set `inherits_from` (at config-create time) to another config **in
the same environment** — a root config plus branch configs, like Doppler. A
branch's effective values are its base's values overlaid with its own, **child
wins** per key. Chains are followed to the deepest ancestor.

- A branch config may have **no secrets of its own** — it can exist purely to
  inherit and override; reading it returns the base's values.
- Reading a branch does **not** require a separate grant on its base (see
  [Authorization](#authorization)).
- **Cycle** in the `inherits_from` chain → `409` (`ErrInheritanceCycle`). A
  **missing or deleted base** → `409` (`ErrBrokenInheritance`).

In the masked list, each key carries an `origin`:

| origin | meaning |
|---|---|
| `own` | defined only in this config |
| `inherited` | defined only in a base config |
| `overridden` | defined here and also in a base (this config's value wins) |

## References

A secret value may embed references, resolved (transitively) at read time:

- **Absolute** — `${projects.<project>.<env>.<config>.KEY}` — the target secret's
  fully-resolved value. All four coordinates are explicit (project slug, env
  slug, config name, key); nothing is inferred.
- **Local** — `${KEY}` — another key in the *same* config's merged (post-
  inheritance) key set.
- References interleave with literal text and with each other:
  `postgres://${DB_USER}:${DB_PASS}@${projects.infra.prod.db.HOST}/app`.
- **Escape** — `$$` emits a literal `$`, so `$${KEY}` is the literal text
  `${KEY}` (no resolution).

Resolution is recursive — an absolute reference resolves the target config's own
inheritance and references first. A value that is *exactly* one reference takes
the target's exact bytes (so a binary/opaque secret passes through unchanged); a
reference embedded in surrounding text splices the target's bytes into the
string.

### Termination & failure

Resolution is **atomic**: any unresolvable reference fails the whole read and
returns no values (better a loud failure than a half-resolved config injected
into a process).

| Condition | HTTP | Sentinel |
|---|---|---|
| reference resolution revisits a `(config,key)` frame | 409 | `ErrReferenceCycle` |
| target project/env/config/key does not exist | 422 | `ErrUnresolvedReference` |
| caller lacks `secret:read` on a referenced target | 403 | `ErrForbiddenReference` |
| resolution depth cap (32) exceeded | 422 | `ErrReferenceDepth` |
| malformed `${...}` token | 400 | `ErrBadReferenceSyntax` |

Error messages carry key names and target **paths** only — never a secret value.

## Read surface: resolved vs raw

Reveal reads **resolve by default**; `?raw=true` returns stored values verbatim.

| Request | Returns |
|---|---|
| `GET /v1/configs/{cid}/secrets` (no `reveal`) | masked list: own + inherited keys, `origin` per key, values masked; **not audited** |
| `GET /v1/configs/{cid}/secrets?reveal=true` | merged + dereferenced values (default); audited |
| `GET /v1/configs/{cid}/secrets?reveal=true&raw=true` | the config's **own** stored values, verbatim `${...}`, unmerged — the editable truth; audited |
| `GET /v1/configs/{cid}/secrets/{key}` | one key, resolved (default); audited |
| `GET /v1/configs/{cid}/secrets/{key}?raw=true` | one key, verbatim; audited |
| `GET /v1/configs/{cid}/secrets/{key}?version=N` | a historical value (always raw — a past version is a stored artifact) |

The resolved single-key response omits `value_version` (a resolved value can
compose multiple versions across configs); the raw and historical paths keep it.

**CLI:** `janus run`, `janus secrets download`, and `janus secrets get` resolve
by default (they consume values); pass `--raw` for verbatim. `janus secrets list`
shows the `ORIGIN` column.

## Authorization

The two mechanisms are treated asymmetrically, by design:

- **References — strict (caller-authorized).** Every config a reference
  dereferences (transitively) requires the caller to independently hold
  `secret:read` on that target. A forbidden reference fails closed (`403`,
  atomic). Every successful dereference is therefore an authorized read — there
  is no transitive privilege escalation. To consume a shared config via a
  reference, a token must be granted read on it explicitly.
- **Inheritance — transparent.** Reading a branch config does **not** require a
  separate grant on its base. `inherits_from` is set by an admin at config-create
  time and the base is always in the same environment, so a branch's inherited
  values are structural content of the branch, not a caller-initiated cross-
  boundary read. Consequence (intended): a service token scoped to *only* a
  branch config can read values inherited from its base.

The rationale: references are caller-authored (anyone with write can add a
`${projects.other...}`), so they must be checked; inheritance structure is admin-
controlled and same-environment, so it is trusted.

## Audit

A resolved reveal writes one primary `secret.reveal` on the config being read,
plus one `secret.reveal` per **distinct** config dereferenced via a reference
(`detail = "via reference from configs/<cid>"`, resource = the target path,
deduped per reveal). Inheritance ancestors are **not** separately audited — they
are part of the primary reveal. A reveal refused by a **forbidden reference**
writes a fail-closed `denied` `secret.reveal` on the config being read
(`detail = "forbidden reference"`), so a denied secret-access attempt is never
unaudited (surfaced by `/v1/audit/export?result=denied`). Recording is
fail-closed; no secret value ever enters an audit row.

## Non-goals

- Cross-environment / cross-project inheritance (inheritance is same-environment;
  cross-boundary sharing is what references are for).
- Reference defaults / fallback syntax (`${KEY:-default}`).
- Config-authorized (Doppler-like) transitive reads — rejected in favor of strict
  per-target authorization.
- Caching of resolved configs across requests (each reveal resolves fresh).
