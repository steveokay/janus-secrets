# Managing secrets

This guide covers how to organize and manage secrets in Janus: the
project → environment → config → secret hierarchy, how each level is
created, and the day-to-day CLI workflow for reading and writing
values. It also explains the two-level versioning model, soft delete
versus hard destroy, and the read-time reference and inheritance
features.

For the underlying schema and versioning internals see
[../data-model.md](../data-model.md); for inheritance and reference
resolution see [../references.md](../references.md); for the full CLI
reference see [../cli.md](../cli.md); for the web UI see
[../web.md](../web.md).

## The hierarchy

Janus organizes secrets in four Doppler-style levels:

```
Project            e.g. "acme-web"          — owns a wrapped project KEK
  └─ Environment   e.g. "prod" / "staging"  — user-definable
       └─ Config   e.g. "prod" (root) or "prod-ci" (branch)
            └─ Secrets   KEY = value         — versioned key/value pairs
```

Projects, environments, and configs are addressed by a human
`slug`/`name` that is unique within its parent, and each also carries a
UUID primary key. Environments are entirely user-definable — the common
`dev` / `staging` / `prod` split is a convention, not a fixed set. A
config is the leaf container that actually holds secrets; a single
environment can have a root config plus branch configs that inherit from
it. See [../data-model.md](../data-model.md) for the full model.

## Creating projects, environments, and configs

You can create projects, environments, and configs three ways: the
**CLI** (`janus project/env/config create`), the **web UI**, or the
**REST API** directly. Once a config exists, `janus setup` *binds* a
working directory to it (writing `.janus.yaml`) and `janus secrets …`
reads and writes the values inside it.

> Note: `janus project` does double duty — it manages both **project
> CRUD** (`create`/`list`/`delete`/`restore`, below) and the **project
> KEK lifecycle** (`rotate-kek`/`rewrap`/`kek-status`). See
> [The `janus project` command](#the-janus-project-command) for the KEK
> verbs.

### Via the CLI

The CLI is self-sufficient for bootstrap. Every command takes the usual
`--address`/`--token` (or `JANUS_ADDR`/`JANUS_TOKEN`); `env`/`config`
resolve their parent from `--project`/`--env` flags, the `JANUS_*`
environment, or `.janus.yaml` (same precedence as `janus secrets`).

```sh
# Create a project, an environment under it, and a config under that.
janus project create --slug acme-web --name "Acme Web"
janus env create --project acme-web --slug prod --name "Production"
janus config create --project acme-web --env prod --name prod
#   optionally inherit from a base config in the same environment:
#   janus config create --project acme-web --env prod --name prod --inherits-from base

# List / soft-delete / restore (delete confirms on a TTY unless --yes).
janus project list
janus env list --project acme-web
janus config list --project acme-web --env prod
janus config delete prod --project acme-web --env prod --yes
janus config restore prod --project acme-web --env prod
```

Mint a scoped service token for CI (shown once — the raw token prints to
**stdout** so it is capturable; the summary goes to stderr):

```sh
TOKEN=$(janus token mint --name ci-deploy --project acme-web --env prod \
  --config prod --access rw --ttl 24h)
janus token list                 # metadata only; the raw token is never re-shown
janus token revoke <id> --yes
```

Omit `--config` on `token mint` for an environment-scoped token. Add
`--json` to any `list`/`mint` command for machine-readable output.

### Via the web UI

The UI provides create flows for projects, environments, and configs
(for example the "New project" action and the per-environment config
board). This is the easiest path for humans. See [../web.md](../web.md).

### Via the REST API

The create endpoints all live under `/v1/` and require an authenticated,
authorized caller (`Authorization: Bearer <token>`, or a UI session
cookie). Substitute a real `$ADDR` and `$TOKEN` below.

Create a project — `POST /v1/projects` with a `slug` and `name`:

```sh
curl -sS -X POST "$ADDR/v1/projects" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"slug": "acme-web", "name": "Acme Web"}'
```

```json
{
  "id": "…-uuid-…",
  "slug": "acme-web",
  "name": "Acme Web",
  "created_at": "2026-07-16T…Z"
}
```

Create an environment under that project —
`POST /v1/projects/{pid}/environments` with a `slug` and `name`:

```sh
curl -sS -X POST "$ADDR/v1/projects/$PID/environments" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"slug": "prod", "name": "Production"}'
```

Create a config under that environment —
`POST /v1/projects/{pid}/environments/{eid}/configs` with a `name` and an
optional `inherits_from` (the UUID of a base config *in the same
environment*, for the root/branch inheritance model):

```sh
curl -sS -X POST "$ADDR/v1/projects/$PID/environments/$EID/configs" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"name": "prod", "inherits_from": null}'
```

The `id` returned by the config-create call is the config UUID (`cid`)
that all secret operations are keyed by. The CLI resolves this `cid` for
you from the project/env/config slugs, so you rarely handle it directly.

## Setting, getting, listing, and deleting secrets

Once a config exists, bind your working directory to it, then use
`janus secrets …`. All of these commands accept the address, credential,
and binding flags (`--address`, `--token`, `--project`, `--env`,
`--config`); see [../cli.md](../cli.md) for the resolution precedence.

### Bind the directory first

`janus setup` validates the project/env/config against the server and,
only on success, writes a committable `.janus.yaml` in the current
directory:

```sh
janus setup --project acme-web --env prod --config prod
```

```yaml
# .janus.yaml  (human slugs only — no values — safe to commit)
project: acme-web
environment: prod
config: prod
```

The file holds slugs only, never values, so teammates who clone the repo
inherit the binding. `.janus.yaml` is read from the current working
directory only (no parent-directory walk).

### Set

`janus secrets set` batches one or more changes into a **single new
config version** (see [Versioning](#versioning--rollback)):

```sh
janus secrets set DATABASE_URL=postgres://…                 # inline pair
janus secrets set A=1 B=2 C=3 --message "seed prod config"  # batched → one version
janus secrets set TOKEN <<<'s3cr3t'                         # value from stdin
janus secrets set TOKEN                                      # TTY: echo-off prompt
janus secrets set API_KEY=abc --type password                # tag the type (display hint only)
```

Value sources, in order: inline `KEY=VALUE` / positional `KEY VALUE`,
then piped stdin, then an echo-off TTY prompt for a bare `KEY`. Inline
values are visible in the process list and shell history — prefer stdin
or the prompt for real secrets. `--message` sets the config version's
message. `--type string|password|json|ssh_key|certificate|note` tags
every key=value pair in the call with a display/handling hint used by the
web UI (masking, generator, validation) — it is **not** a storage or
crypto distinction, and it applies to all pairs in the call; omit it to
default to `string`. The `Saved N secret(s) as vN` confirmation goes to
stderr.

#### Key naming

Secret keys accept a flat filename-style charset (`[A-Za-z0-9._-]`, e.g.
`config.json` or `id_rsa.pub`), not just valid env-var names — this
supports secrets that are really whole files. A key that isn't a valid
environment-variable name (contains `.` or `-`, or starts with a digit)
is **skipped with a warning** by `janus run` and by `secrets download
--format env`; it can still be written to disk via `--format files`
(below) or read individually with `secrets get`. Such keys also cannot
be used in `${...}` references and are skipped (not synced) by the
GitHub Actions sync integration — see
[github-actions.md](./github-actions.md#key-name-constraints).

### Get

`janus secrets get KEY` reveals one value (an **audited**
`secret.reveal`) and prints the value only to stdout, so command
substitution is exact:

```sh
DB_URL=$(janus secrets get DATABASE_URL)     # value only → capture
janus secrets get API_KEY --version 3        # a historical value version
janus secrets get DB_URL --raw               # stored value verbatim, unresolved
```

By default the value is **resolved** — config inheritance and `${…}`
references are applied. `--raw` returns the config's own stored value
verbatim (unresolved `${…}`). `--version N` fetches a historical value
(always raw — a past version is a stored artifact).

### List

`janus secrets list` shows masked metadata only — key names, `ORIGIN`,
per-key value version, and update time. **No values are shown and the
read is not audited:**

```sh
janus secrets list           # KEY  VERSION  ORIGIN  UPDATED table
janus secrets list --json    # machine-readable
```

The `ORIGIN` column reflects inheritance: `own`, `inherited`, or
`overridden` (see [References & inheritance](#references--inheritance)).

### Delete

`janus secrets delete` tombstones one or more keys, committing a new
config version. It confirms on a TTY unless `--yes`:

```sh
janus secrets delete OLD_KEY                 # confirms on a TTY
janus secrets delete OLD_A OLD_B --yes       # skip the confirmation
```

This is a **secret delete** (a tombstone in a new version), not an entity
destroy — the key's history remains and it can be re-set later. See
[Soft delete vs hard destroy](#soft-delete-vs-hard-destroy) for the
distinction from destroying a whole project/environment/config.

### Lock / unlock (promotion protection)

`janus secrets lock KEY` marks a key promotion-protected on the bound
config; `janus secrets unlock KEY` clears it. A locked key is silently
skipped by `janus promote` / `promote request` even when selected via
`--all` or `--key` — useful for an environment-specific value (e.g. a
prod-only credential) that a promotion from a lower environment should
never overwrite:

```sh
janus secrets lock DATABASE_URL     # protect this key on the bound config
janus secrets unlock DATABASE_URL   # clear the protection
```

See [Promotion & approvals](#promotion--approvals) below.

## Versioning & rollback

Janus versions every change at **two levels** so operators can answer
both "what did this whole config look like at release time?" and "when
did *this one key* change?".

- **Config version.** Each *save* creates one immutable config version
  (`v1`, `v2`, …). A save may batch edits to many keys — the config
  version is the unit of **diff** and **rollback**. This is what the CLI
  commits on each `secrets set`/`delete`, and what the web editor commits
  when you click "Save as vN".
- **Secret value version.** Each key additionally has its own
  append-only value history, for per-key trace.

Reads default to the latest config version.

The relevant read/rollback endpoints (the web UI surfaces these as a
version list with diff and a rollback action):

| Endpoint | Purpose |
|---|---|
| `GET /v1/configs/{cid}/versions` | list config versions (v1…vN) |
| `GET /v1/configs/{cid}/versions/diff` | diff two config versions (value-free) |
| `POST /v1/configs/{cid}/rollback` | roll back to a version (creates a *new* version) |
| `GET /v1/configs/{cid}/secrets/{key}/history` | per-key value history |

Rollback does not rewrite history: rolling back to `vN` creates a *new*
config version whose manifest copies `vN`'s entries (reusing the same
encrypted value rows — nothing is re-encrypted). See
[../data-model.md](../data-model.md) for the manifest-of-pointers design.

## Soft delete vs hard destroy

Two distinct notions of "delete" exist, and they operate at different
levels:

- **Secret delete** (a key inside a config) = a tombstone entry in a new
  config version. The key disappears from the resolved state but its
  history remains; "undelete" is just a later save that sets the key
  again. This is what `janus secrets delete` does.
- **Entity soft-delete** (a whole project / environment / config) = a
  nullable `deleted_at` timestamp. Soft-deleted entities are hidden from
  reads and lists but can be **restored**. A **hard destroy** is a
  separate, explicit operation that actually removes rows (and cascades
  to the subtree).

Entity delete/restore endpoints:

| Endpoint | Purpose |
|---|---|
| `DELETE /v1/projects/{pid}` | soft-delete a project |
| `POST /v1/projects/{pid}/restore` | restore a soft-deleted project |
| `DELETE /v1/projects/{pid}/environments/{eid}` | soft-delete an environment |
| `POST /v1/projects/{pid}/environments/{eid}/restore` | restore an environment |
| `DELETE /v1/configs/{cid}` | soft-delete a config |
| `POST /v1/configs/{cid}/restore` | restore a config |
| `GET /v1/trash` | list soft-deleted entities awaiting restore/destroy |

The web UI exposes trash/restore for soft-deleted entities. Destroying a
config that is still an inheritance base for a branch config is refused
(`409`), so an inheritance relationship is never silently broken. See
[../data-model.md](../data-model.md) for cascade and destroy semantics.

## References & inheritance

Two read-time composition features let you avoid duplicating values.
They are resolved when a config is read — nothing is copied at write
time. See [../references.md](../references.md) for the full model.

### Inheritance (root + branch configs)

A config may set `inherits_from` (at config-create time) to a base
config **in the same environment**, forming a root config plus branch
configs. A branch's effective values are its base's values overlaid with
its own — **child wins** per key. A branch may have no secrets of its
own and exist purely to override a few keys.

Inheritance is **transparent**: reading a branch does not require a
separate grant on its base. In `janus secrets list`, the `ORIGIN` column
tells you where each key comes from:

| origin | meaning |
|---|---|
| `own` | defined only in this config |
| `inherited` | defined only in a base config |
| `overridden` | defined here and in a base (this config's value wins) |

### References

A secret value may embed references, resolved (transitively) at read
time:

- **Absolute** — `${projects.<project>.<env>.<config>.KEY}` — pulls the
  target secret's fully-resolved value. All four coordinates are
  explicit (project slug, env slug, config name, key).
- **Local** — `${KEY}` — another key in the *same* config's merged
  (post-inheritance) key set.
- References interleave with literal text and with each other:
  `postgres://${DB_USER}:${DB_PASS}@${projects.infra.prod.db.HOST}/app`.
- **Escape** — `$$` emits a literal `$`, so `$${KEY}` is the literal text
  `${KEY}`.

Set a value containing a reference exactly as you would any other value;
the `${…}` is stored verbatim and resolved on read:

```sh
janus secrets set DB_URL='postgres://${DB_USER}:${DB_PASS}@db.internal/app'
janus secrets set SHARED_KEY='${projects.infra.prod.db.HOST}'
```

References resolve on `get`, `download`, and `run` by default; pass
`--raw` to see the stored `${…}` verbatim.

**Safety properties:** references are **caller-authorized** — every
config a reference dereferences requires the caller to independently hold
`secret:read` on that target, and a forbidden reference fails closed
(`403`, atomically). Resolution is cycle-checked (a revisited
`(config, key)` frame → `409`) and depth-capped (32). Any unresolvable
reference fails the whole read and returns no values. Error messages
carry key names and target paths only — never a secret value.

## The `janus project` command

The `janus project` group does two jobs. Its **CRUD** verbs
(`create`/`list`/`delete`/`restore`, shown [above](#via-the-cli)) manage
projects as containers. Its owner-only **project KEK (key-encryption-key)
lifecycle** verbs manage key rotation:

```sh
janus project rotate-kek <project-id>   # mint a new project KEK version
janus project rewrap <project-id>       # re-wrap DEKs onto the current KEK version
janus project kek-status <project-id>   # show current version + versions still holding DEKs
```

The KEK verbs map to `POST /v1/projects/{pid}/kek/rotate`,
`POST /v1/projects/{pid}/kek/rewrap`, and `GET /v1/projects/{pid}/kek`.

## Promotion & approvals

Promotion copies selected secrets forward along the pipeline
(`dev → staging → prod`). It re-encrypts the values into the target
config — it never moves ciphertext verbatim — and records a value-free
audit event naming only the keys involved.

`janus promote` operates on the **bound config** (from `.janus.yaml` or
`--project`/`--env`/`--config`, same resolution as `janus secrets`) as
the *source*, and `--to <env-slug>` names the *target environment* — the
target config is resolved by matching the bound config's name in that
environment. Bind your directory to the source config with `janus setup`
(or pass the flags explicitly) before promoting.

### Direct promotion

If you hold `secret:promote` on the **target** environment you can promote
directly. Choose which keys to carry with `--key` (repeatable) or
`--all` (every added/changed key), preview with `--dry-run`, and apply:

```sh
janus promote --to staging --key DATABASE_URL --key API_KEY
janus promote --to staging --all                    # every added/changed key
janus promote --to staging --all --dry-run           # print the diff, apply nothing
janus promote --to staging --all --create-target     # create the target config if missing
janus promote --to staging --all --include-removes   # also propagate deletions
```

This maps to `POST /v1/promote`. Only the next allowed pipeline step is
accepted, and keys locked in the target (`janus secrets lock`, see
[above](#lock--unlock-promotion-protection)) are silently skipped. The
web UI exposes the same flow as a drag-and-diff modal.

### Approval workflow (for users without `secret:promote`)

Developers who lack `secret:promote` on the target don't get to promote
directly — but if they hold `promotion:request` on the **source**
environment (granted to `developer` and above) they can *file a request*
for someone who can. Requesting requires explicit `--key` selection (no
`--all` shortcut):

```sh
# File a request (pins the current source version; value-free)
janus promote request --to staging --key DATABASE_URL --note "rotate prod db creds"

# See what's waiting (--project takes a project id, not a slug)
janus promote requests --project <project-id> --status pending

# A holder of secret:promote on the target reviews and decides:
janus promote approve <request-id>          # applies the promotion immediately
janus promote reject  <request-id> --note "hold for release window"

# The requester can withdraw their own pending request:
janus promote cancel  <request-id>
```

The request stores **only** the target, the pinned source version, and the
key names/actions — never any secret value. Approval is **four-eyes**: the
approver must be someone other than the requester. On approval the
promotion is applied in the same step and the request is marked `applied`
with the resulting target version; if the apply fails the request stays
`pending` so it can be retried or cancelled.

These map to `POST /v1/promote/requests`, `GET /v1/promote/requests`
(filter with `?project=`, `?status=`, `?mine=true`),
`GET /v1/promote/requests/{id}` (returns a value-free diff when the target
already exists), and `POST /v1/promote/requests/{id}/{approve,reject,cancel}`.
The web UI surfaces pending items on an **Approvals** page, with a nav badge
counting requests you're able to approve. Every state change writes a
`promotion.request.{create,approve,reject,cancel}` audit event.
