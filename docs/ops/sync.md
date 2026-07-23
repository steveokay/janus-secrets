# Sync integrations

A sync target replicates one config's **resolved** secrets (references
expanded, inheritance merged — the same view `?reveal=true` returns) to an
external store on an interval, or on demand. Four providers ship today:

- **`github`** — writes GitHub Actions secrets (repo- or
  environment-scoped) via the GitHub REST API.
- **`k8s`** — writes a Kubernetes `Secret` object via the Kubernetes API
  server, using server-side apply. For an end-to-end integration guide
  (cluster RBAC, consuming the Secret, refreshing running pods), see
  [kubernetes.md](../guides/kubernetes.md).
- **`gitlab`** — writes GitLab CI/CD variables to a project via the
  GitLab REST API (`PRIVATE-TOKEN` auth).
- **`aws_ssm`** — writes AWS SSM Parameter Store `SecureString`
  parameters under a path prefix (SSM encrypts them with the account KMS
  key).

Sync is **one-way**: Janus → external store. It never reads back from the
destination except to fetch the GitHub public key needed to encrypt a
value for upload. Nothing you edit directly in the destination flows
back into Janus, and the next reconcile can overwrite it (see prune
semantics below).

## Full-mirror model & prune

Each target tracks a **managed-keys manifest** — the set of keys Janus
itself wrote on the last successful sync. A sync target is a *full
mirror* of a config by default (`--prune` defaults to `true`): any
previously-managed key that is no longer present in the config's resolved
secrets is deleted from the destination on the next reconcile. Keys that
exist in the destination but were **never** written by Janus (an operator
added them by hand, outside the manifest) are left alone for `github` —
prune only ever removes keys Janus itself is tracked as owning.

For `k8s`, prune is enforced differently: applies use **server-side apply**
with `fieldManager=janus`, so Kubernetes itself reconciles field ownership
and removes fields Janus no longer submits. With `--prune=false` on a k8s
target, Janus falls back to a merge-patch instead of an apply — a
merge-patch can only add/update keys, so a key removed from the config
**lingers** in the destination `Secret` until you prune manually. A
merge-patch also cannot *create* a Secret: a fresh `--prune=false` k8s
target whose destination `Secret` does not yet exist will fail (and back
off) until the `Secret` is created out-of-band. Leave prune on (the
default) for Janus to create-or-update the `Secret` itself.

Turn prune off (`--prune=false`) if the destination has a mix of
Janus-managed and independently-managed keys and you want a strict "add
and update, never delete" behavior — with the k8s caveat above.

## Providers & credential setup (least privilege)

### GitHub Actions

Create a **fine-grained personal access token** scoped to only the target
repository, with the **"Secrets" repository permission set to
read/write** and nothing else. If you're targeting a GitHub *environment*
(`--environment`) rather than the whole repo, prefer an
environment-scoped protection rule over a broader repo-wide PAT where
your GitHub plan supports it.

Janus never uploads a secret value in the clear: it first fetches the
repo's (or environment's) public key from the GitHub secrets API, then
encrypts each value client-side as a **libsodium sealed box**
(`golang.org/x/crypto/nacl/box`) before PUTting it — this is the same
encryption GitHub's own docs and `gh` CLI use. The PAT itself is never
logged and never appears in an API response (see Masking below).

- **Addr:** `owner`/`repo`, plus optional `environment`.
- **Creds:** `pat`.

### Kubernetes

Create a **ServiceAccount** scoped to a single namespace with an RBAC
`Role`/`RoleBinding` granting only `get`, `patch`, and `create` on
`secrets` in that namespace — not cluster-wide, and no other verbs or
resources. Mint a token for that ServiceAccount (a projected/bound token,
or a legacy long-lived Secret-backed token depending on your cluster
version) and supply it as `--k8s-token`.

Janus also requires the cluster's **CA certificate** (`--ca-cert`) so it
can verify the API server's TLS certificate — Janus does not trust the
system root store for the Kubernetes API endpoint, and does **not**
support skipping verification. An empty CA certificate is rejected at
target-creation time (`400`), before any credentials are persisted.

Applies use **server-side apply** (`fieldManager=janus`) against
`PATCH /api/v1/namespaces/{ns}/secrets/{name}`, so re-applying is safe to
retry and Kubernetes itself tracks which fields Janus owns.

- **Addr:** `namespace`/`secret_name`.
- **Creds:** `api_url`/`token`/`ca_cert`. `api_url` is the API server base
  URL (scheme + host + port, no path — Janus appends
  `/api/v1/namespaces/…`); its host must be a SAN on the server cert since
  TLS is verified against `ca_cert`. For which endpoint to use (outside vs.
  inside the cluster, and the Docker Desktop container case), see
  [kubernetes.md § What goes in the `--api-url` field](../guides/kubernetes.md#what-goes-in-the---api-url-field).

### GitLab CI/CD

Create a **project access token** (or a personal access token scoped to
the target project) with the **`api`** scope, and supply it as
`--gitlab-token`. Janus sends it as the `PRIVATE-TOKEN` header on every
call; it is never logged and never echoed back by the API.

Each config key becomes a GitLab CI/CD variable. Janus upserts variables
via the CI/CD variables API — it tries to create (`POST .../variables`)
and, if the variable already exists, updates it (`PUT .../variables/:key`).
Prune deletes previously-managed keys no longer present in the config
(`DELETE .../variables/:key`); a `404` on delete is treated as
already-gone (idempotent).

TLS is verified against the system root store (default `net/http`
transport), which is correct for `gitlab.com` and any GitLab instance
with a publicly-trusted certificate.

> **Masked/protected caveat.** Janus writes every variable with
> `masked=false` and `protected=false`. GitLab **rejects** `masked=true`
> for values that don't satisfy its
> [mask regex](https://docs.gitlab.com/ee/ci/variables/#mask-a-cicd-variable)
> (minimum length, restricted character set), which would turn ordinary
> secrets into spurious sync failures. Configuring masking/protection is
> a documented follow-up; for now, set those flags manually in GitLab if
> a given value qualifies, or scope the runner appropriately.

- **Addr:** `gitlab_url` (optional, defaults to `https://gitlab.com`),
  `project` (numeric id **or** URL-encoded `group%2Fproject` path),
  optional `environment_scope`.
- **Creds:** `token` (the `PRIVATE-TOKEN` value).

### AWS SSM Parameter Store

Create an IAM principal (user or role) with permission to
`ssm:PutParameter` and `ssm:DeleteParameters` under the target path
prefix (and `kms:Encrypt`/`kms:GenerateDataKey` on the KMS key SSM uses
for `SecureString`). Supply its static credentials as
`--aws-access-key-id` / `--aws-secret-access-key` (and, for temporary
credentials, `--aws-session-token`).

Janus writes each config key as a `SecureString` parameter at
`<path_prefix>/<KEY>` with `Overwrite=true` — SSM encrypts the value with
the account KMS key at rest. Janus builds the SDK client from **only** the
static credentials you supply; it never falls back to the host's ambient
environment or instance-profile credentials, so a sync target's identity
is always explicit. Prune deletes previously-managed parameters under the
prefix no longer present in the config (`DeleteParameters`, batched at the
API's 10-name limit). AWS error messages are **sanitized** to a value-free
category before they reach `last_error` or the audit log — an ARN, account
id, or value never leaks.

- **Addr:** `region`, `path_prefix` (e.g. `/janus/app/prod`).
- **Creds:** `access_key_id`, `secret_access_key`, optional
  `session_token`.

## GitHub key-name constraint

GitHub Actions secret names must match `^[A-Za-z_][A-Za-z0-9_]*$`, be
**100 characters or fewer**, and must **not** start with the reserved
`GITHUB_` prefix (checked case-insensitively). A Janus secret key that
doesn't conform is **skipped**, not fatal: the reconcile still applies
every other key and reports the skipped key (with a value-free reason) in
the sync result. Rename the offending key in Janus (or accept it will
never sync to GitHub) rather than expecting Janus to coerce it.

The Kubernetes provider has no equivalent name restriction — Kubernetes
`Secret` data keys are far more permissive.

## Change detection

Before doing any external-API work, Janus computes a keyed fingerprint
(an HMAC under the project's key material, so it never leaves a
reversible trace) over the config's fully resolved key/value set. If the
fingerprint matches the one recorded on the target's last successful
sync, the reconcile is a no-op — no GitHub/Kubernetes calls are made, and
`next_sync_at` simply advances to the next interval.

`janus sync sync <id>` (or `POST /v1/sync/targets/{id}/sync`) forces an
immediate reconcile **regardless of the fingerprint** — useful right
after editing the underlying config, or to confirm a target that was
previously `failed` is healthy again.

## Cross-project references refused (security)

A config synced to an external store is resolved with a
**project-scoped authorizer**: a reference to another config in the
*same* project resolves normally, but a reference to a config in a
**different** project is refused, and the whole reconcile fails (the
target's failure/backoff path — see below). This is deliberate: without
this restriction, a project admin could set up a sync target on a config
that references a secret from a project they don't otherwise have
export rights on, and exfiltrate it to an external store they control.

Keep every reference inside a synced config scoped to its own project.
If you need to share a value across projects, sync it independently from
its owning project's config rather than re-exporting it via a reference.

## Sealed behavior

While the server is sealed, the scheduler's per-tick pass (`RunDue`) is a
complete no-op — it does not even claim due targets. A manual
`janus sync sync <id>` while sealed returns the `sealed` error directly.
Sealed windows are **not** treated as a failure: `failure_count` and
`last_error` are untouched, and no `sync.reconcile` audit event is
written for a sealed skip. Once the server is unsealed, any target whose
`next_sync_at` has already passed reconciles on the next tick.

## Failure handling & backoff

A failed reconcile (provider apply error, forbidden cross-project
reference, invalid config) is retried with exponential backoff: base
delay **1 minute**, doubling on each consecutive failure, capped at
**1 hour** — identical shape to rotation. `last_error` stores a
value-free failure category (`apply failed`, `invalid config`,
`forbidden reference`, `sync error`) — never a credential, token, or
secret value.

After **5 consecutive failures**, the target's status flips to `failed`
and the scheduler stops retrying it automatically. Reactivate it either
way:

```sh
janus sync sync <id>                        # manual sync: clears `failed`, retries immediately
janus sync update <id> --status active      # reset status without forcing an immediate attempt
```

## Scheduler

The sync scheduler runs in-process alongside the server — no separate
worker/cron process to deploy, and the same shape as the rotation
scheduler. One environment variable controls it:

| Variable | Default | Meaning |
|---|---|---|
| `JANUS_SYNC_TICK` | `60s` | Go duration between scheduler passes. Set `0` to disable the scheduler on this instance (targets still exist and can be synced manually via `janus sync sync`, but nothing syncs automatically) |

The scheduler stops on graceful shutdown (SIGTERM) along with the rest of
the server; there is nothing extra to drain.

## CLI usage

```sh
# GitHub Actions: mirror a config's secrets into a repo, hourly.
janus sync create --config $CONFIG --provider github \
  --owner acme --repo widgets --interval-seconds 3600 \
  --pat github_pat_...

# GitHub Actions, environment-scoped.
janus sync create --config $CONFIG --provider github \
  --owner acme --repo widgets --environment production \
  --interval-seconds 3600 --pat github_pat_...

# Kubernetes: mirror a config into a namespaced Secret, every 5 minutes.
janus sync create --config $CONFIG --provider k8s \
  --api-url https://k8s.internal:6443 --k8s-token $SA_TOKEN \
  --ca-cert "$(cat cluster-ca.pem)" \
  --namespace widgets-prod --secret-name app-config \
  --interval-seconds 300

# GitLab: mirror a config into a project's CI/CD variables, hourly.
janus sync create --config $CONFIG --provider gitlab \
  --project 42 --gitlab-token $GITLAB_TOKEN \
  --interval-seconds 3600
#   (self-managed instance / URL-encoded path / env scope)
janus sync create --config $CONFIG --provider gitlab \
  --gitlab-url https://gitlab.example.com \
  --project mygroup%2Fmyproject --environment-scope production \
  --gitlab-token $GITLAB_TOKEN --interval-seconds 3600

# AWS SSM: mirror a config into SecureString parameters under a prefix.
janus sync create --config $CONFIG --provider aws_ssm \
  --aws-region us-east-1 --path-prefix /janus/atlas/prod \
  --aws-access-key-id $AWS_ACCESS_KEY_ID \
  --aws-secret-access-key $AWS_SECRET_ACCESS_KEY \
  --interval-seconds 900

# List, inspect, update, sync now, delete.
janus sync list --project $PROJECT
janus sync get <id>
janus sync update <id> --interval-seconds 900
janus sync update <id> --status active
janus sync sync <id>
janus sync delete <id>
```

`--prune` defaults to `true` on `create`; pass `--prune=false` to disable
full-mirror deletes (see prune semantics above). `update` accepts the
same `--interval-seconds`/`--prune`/`--status active|paused` flags, plus
provider-specific `--owner`/`--repo`/`--environment`/`--pat` or
`--api-url`/`--ca-cert`/`--k8s-token`/`--namespace`/`--secret-name` to
rotate credentials or repoint the destination without recreating the
target.

## Run history

Every reconcile attempt — scheduled or manual, success or failure — also
appends a row to `sync_runs` (started/ended time, status, value-free error
category, resulting config version, keys-written count, attempt number) in
the same transaction as the attempt itself. `GET /v1/sync/targets/{id}/runs`
(cursor-paginated, `sync:manage`) lists a target's history newest-first; the
web UI surfaces it as a per-target run-history panel. History is capped at
the 100 most recent runs per target — older rows are pruned automatically
on insert. There is no CLI subcommand for run history; use the API or UI.

## RBAC & audit

Creating, listing, updating, deleting, or manually syncing a target
requires the `sync:manage` permission, granted to the project **admin**
and **owner** roles (same tier as `rotation:manage`). Every reconcile —
scheduled or manual, success or failure — writes a `sync.reconcile` audit
event under a `system` actor (never the triggering user, since scheduled
reconciles have none); the event records the target's config path and
provider, never the resolved secret values or credentials. Every
create/update/delete/sync API call is separately audited under the
calling user.

`GET`/`list` responses mask sync target configuration exactly like
rotation policies mask theirs: credentials (PAT, Kubernetes token, CA
certificate) are **never** echoed back by the API, in logs, or in
`last_error` — only non-secret destination coordinates (`owner`/`repo`/
`environment`/`namespace`/`secret_name`) and status fields are returned.

## Backup & restore

`sync_targets` rows are included in `janus backup` / `janus restore` like
any other table: the envelope-encrypted provider credentials (PAT,
Kubernetes token, CA certificate) travel wrapped, alongside the
non-secret destination address and managed-keys manifest, as part of the
key-preserving instance dump. A restored instance — once unsealed with
the original unseal material — keeps its sync targets and resumes
reconciling them on the next scheduler tick. See
[backup-restore.md](backup-restore.md) for the general backup/restore
procedure.
