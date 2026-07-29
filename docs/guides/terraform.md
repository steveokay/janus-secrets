# Terraform provider

`terraform-provider-janus` lets infrastructure teams manage Janus resources
**declaratively** — projects, environments, configs, secrets, service tokens,
and groups with their membership and role bindings — instead of clicking the
web UI or scripting `curl` against the REST API. It is a self-contained Go
module in
[`terraform-provider-janus/`](../../terraform-provider-janus/) built on the
[terraform-plugin-framework](https://github.com/hashicorp/terraform-plugin-framework),
and it talks to Janus over the `/v1` HTTP API with a scoped
`janus_svc_`/admin token.

> **Read this first — secrets live in Terraform state.** Terraform persists
> every managed attribute in its state file, including `janus_secret.value`,
> every value in the `janus_secrets.secrets` map, and the once-only
> `janus_service_token.token`. Even though the provider
> marks these `Sensitive` (so they never print in plan output), they are
> still written to state. **Use a sensitive/remote state backend** (encrypted
> S3 + lock table, Terraform Cloud, etc.) and tightly restrict who can read
> state.

## When to use it

- You already describe the rest of your infra (databases, clusters, CI) in
  Terraform and want your Janus projects/configs to live in the same code.
- You want reviewable, versioned, drift-detected management of the secret
  *containers* (and optionally the values).
- You want to mint CI service tokens as part of `terraform apply`.

For runtime secret **consumption**, prefer `janus run` (the
[CLI](injecting-secrets.md)) or the [Go SDK](go-sdk.md) — the provider is for
declarative *management*, not for injecting secrets into a running process.

## Setup

The provider is not published to the Terraform Registry yet; build and install
it into the local plugin mirror:

```sh
cd terraform-provider-janus
go build -o terraform-provider-janus

VERSION=0.1.0
OS_ARCH=linux_amd64   # or darwin_arm64, windows_amd64, ...
DEST=~/.terraform.d/plugins/registry.terraform.io/steveokay/janus/${VERSION}/${OS_ARCH}
mkdir -p "$DEST" && cp terraform-provider-janus "$DEST/"
```

Configure the provider. Both settings fall back to environment variables
(`JANUS_ADDR`, `JANUS_TOKEN`):

```hcl
terraform {
  required_providers {
    janus = {
      source  = "steveokay/janus"
      version = "0.1.0"
    }
  }
}

provider "janus" {
  endpoint = "https://janus.example.com" # or export JANUS_ADDR
  token    = var.janus_token             # or export JANUS_TOKEN (sensitive)
}
```

The token needs enough RBAC to perform the operations you declare (create
projects/envs/configs, write secrets, mint tokens) — typically an admin or
owner token for the relevant scope.

## End-to-end example

```hcl
resource "janus_project" "web" {
  slug = "acme-web"
  name = "Acme Web"
}

resource "janus_environment" "prod" {
  project_id = janus_project.web.id
  slug       = "prod"
  name       = "Production"
}

resource "janus_config" "prod_root" {
  project_id     = janus_project.web.id
  environment_id = janus_environment.prod.id
  name           = "prod"
}

resource "janus_secret" "database_url" {
  config_id = janus_config.prod_root.id
  key       = "DATABASE_URL"
  value     = var.database_url # sensitive
}

resource "janus_service_token" "ci_deploy" {
  name   = "ci-deploy"
  scope  = janus_config.prod_root.id
  access = "read"
}

output "ci_token" {
  value     = janus_service_token.ci_deploy.token
  sensitive = true
}
```

## Writing many keys as one config version — `janus_secrets`

`janus_secret` manages **one** key, and each write creates its own immutable
config version. Seeding twenty keys that way produces twenty versions, which
makes the version history and the diff view useless for the change you actually
made.

`janus_secrets` manages a **map** of keys for one config and sends every add,
change and removal in a single request to `PUT /v1/configs/{id}/secrets` — so
the whole apply lands as **one config version**, exactly like hitting "Save as
vN" in the web editor.

```hcl
resource "janus_secrets" "prod" {
  config_id = janus_config.prod_root.id
  message   = "seed prod from terraform" # becomes the config version message

  secrets = {
    DATABASE_URL = var.database_url
    STRIPE_KEY   = var.stripe_key
    FEATURE_FLAG = "on"
  }
}

output "prod_config_version" {
  value = janus_secrets.prod.config_version # the version this apply created
}
```

Delete a key by removing it from the map: the next apply **tombstones** it (a
soft delete — the value stays recoverable from an earlier config version) in the
*same* batch as any other change, so an apply that adds two keys and drops one is
still one version. Destroying the resource tombstones every key it manages, also
in one batch. Keys that are not in the map are never touched.

### Drift detection is metadata-only

The masked list endpoint is **value-free** — that is the point of it — so this
resource *cannot* compare the stored plaintext with your configuration. Instead
it records each key's server-side `value_version` (Janus bumps that counter on
every write to a key) in the non-sensitive `value_versions` attribute. On
refresh:

| Observation                                        | What Terraform does                     |
| -------------------------------------------------- | --------------------------------------- |
| `value_version` moved since the last apply          | Key dropped from state → plan rewrites it |
| Key missing, or now only *inherited* from a base config | Key dropped from state → plan re-adds it |
| `value_version` unchanged                           | Assumed still in sync                    |

So it reliably tells you **that** a key was written outside Terraform, and
reasserts your configuration. It cannot tell you **what** the out-of-band value
was, and it cannot notice a value that never matched state in the first place
(e.g. immediately after `terraform import janus_secrets.prod <config_uuid>`,
where `secrets` starts empty and the first apply rewrites every key). Detecting
value drift properly would mean an audited whole-config `secret.reveal` on every
`terraform plan`; that trade-off is deliberately not taken.

Because the whole `secrets` map is `Sensitive`, plan output masks values *and*
key names. Use the value-free `value_versions` output to see which keys the
resource tracks.

### Don't manage one key with both resources

`janus_secret` and `janus_secrets` can share a config, but **not a key** — two
owners would fight on every apply. `janus_secrets` fails loudly instead of
clobbering: before adopting a key it checks the masked list, and if that key
already exists in the config it errors and writes nothing, naming the clashing
keys. (A key that merely *inherits* from a base config is fair game — writing it
is a legitimate override.) Keep the key sets disjoint, or move the key under one
resource and `terraform state rm` the other.

If the target config is **protected** (four-eyes approval), Janus answers `202
Accepted` and files the batch as a pending edit request rather than committing
it. The provider surfaces that as an error, because Terraform must never record
a write that is not live.

## Environment-scoped service tokens

Janus scopes service tokens to a **config** or a whole **environment** — never a
project and never instance-wide. `scope_kind` picks which, and `scope` is the
corresponding UUID:

```hcl
# Default: config-scoped (scope_kind = "config" is implied).
resource "janus_service_token" "ci_deploy" {
  name   = "ci-deploy"
  scope  = janus_config.prod_root.id
  access = "read"
}

# Every config in the prod environment.
resource "janus_service_token" "prod_reader" {
  name       = "prod-reader"
  scope_kind = "environment"
  scope      = janus_environment.prod.id
  access     = "read"
}
```

An invalid kind (say `project`) fails at `terraform plan` — the provider
validates the enum locally, so you don't burn an API round-trip on a `400`. The
minted token remains a **sensitive computed** attribute available exactly once;
it is persisted in state and cannot be re-read from Janus afterwards.

Upgrading from a provider build without `scope_kind`: existing tokens refresh
their kind from the server, so they are not planned for replacement.

A full runnable version (with variables, data sources, and outputs) lives in
[`terraform-provider-janus/examples/main.tf`](../../terraform-provider-janus/examples/main.tf).

## Groups as code

A [group](groups.md) is a subject a role binding can target instead of a user,
so a whole team is granted access once. Three resources cover it, and they are
three resources rather than one because Janus splits the authority three ways —
see [Two tokens, or one big one](#two-tokens-or-one-big-one) below.

```hcl
# The catalog: which groups exist. Instance `group:manage`.
resource "janus_group" "payments" {
  name        = "Team Payments"
  kind        = "local"
  description = "Payments squad"
}

# Local membership. Also instance `group:manage`.
resource "janus_group_member" "alice" {
  group_id = janus_group.payments.id
  user_id  = var.alice_user_id
}

# The grant. `member:manage` AT THE SCOPE, capped by your own bound role.
resource "janus_group_binding" "payments_on_web" {
  group_id   = janus_group.payments.id
  project_id = janus_project.web.id
  role       = "developer"
}
```

An IdP-fed group instead names the claim value your provider emits, and has no
`janus_group_member` at all:

```hcl
resource "janus_group" "payments_idp" {
  name        = "Team Payments"
  kind        = "oidc"
  claim_value = "8f14e45f-ceea-467a-9d0e-7f4b2a1c9c33" # Entra object GUID
}
```

### The two rules the provider enforces at `plan`

Both of these are refused by Janus (one by a `CHECK` constraint, one by the
schema itself), and both fail at `terraform plan` rather than mid-apply:

**A group can never be `owner`.** `role` accepts `viewer`, `developer` and
`admin` only, and `owner` gets its own diagnostic explaining why rather than a
generic "invalid value": owner rotates the master key, prunes the audit chain
and destroys secret history, so it must be a direct binding on a person. That
also keeps the never-lock-out guard honest — every instance owner is a direct
binding, so an IdP outage cannot leave the instance without one.

**`janus_group_member` only makes sense for a `local` group.** An `oidc` group's
membership is a snapshot refreshed from the identity provider at each sign-in;
the denormalised `group_kind` column plus a composite foreign key make a
hand-added member of one *unrepresentable*, not merely rejected. The provider
looks the group's kind up during `plan` whenever `group_id` is already concrete
(an existing group, an imported one, a literal UUID) and fails there. When the
group is created in the *same* apply its id is still unknown at plan time, so
the check runs as a pre-flight in `Create` instead — you still get a
provider-authored explanation and **no write**, just at apply rather than plan.
A lookup that fails for any other reason (server unreachable, token cannot read
the catalog) is silent by design: `plan` must not break over it.

The kind/claim pairing is plan-time too — `kind = "oidc"` with no `claim_value`,
or `kind = "local"` *with* one, never reaches the API.

### Two tokens, or one big one

The split is deliberate and the provider does not paper over it:

| Resource | Authority needed |
| --- | --- |
| `janus_group`, `janus_group_member` | instance-scoped `group:manage` (admin+) |
| `janus_group_binding` | `member:manage` **at that scope**, capped by your bound role |

So a project admin can bind a group to their own project — the same authority
they already have over users — but cannot create a group or add themselves to
one, and therefore cannot reach a project they don't already administer. Run
Terraform with a token holding both if your root module does both; a
`403 forbidden` on one resource and not the other is this split showing, not a
provider bug. The cap is measured against your *durable* role, never a
break-glass elevation, so an emergency elevation cannot be turned into a lasting
group binding.

### What the provider does not expose

No member count and no member data source. An `oidc` group's member list only
covers users who have signed in — Janus has never seen a token for anyone else —
so any count would render as the group's membership and would not be it. If you
need to see who reaches a scope, the **Members** screen resolves it server-side
(it needs only `member:read` there, which a project admin has).

Bindings **union** with direct user bindings; there are no deny rules. A
project-scoped grant therefore covers that project's production environment too.
The answer is to make production four-eyes (`janus env protect prod`), not to
look for a narrower binding — see [groups](groups.md#groups-and-production).

Import syntax:

```sh
terraform import janus_group.payments <group_uuid>
terraform import janus_group_member.alice <group_uuid>/<user_uuid>
terraform import janus_group_binding.payments_on_web project/<project_uuid>/<group_uuid>
```

The binding id doubles as its import syntax: `instance/<group>`,
`project/<project>/<group>`, or `environment/<project>/<environment>/<group>`.

## Resources & data sources at a glance

| Kind        | Name                     | Notes                                                        |
| ----------- | ------------------------ | ------------------------------------------------------------ |
| resource    | `janus_project`          | `name` renamable in place; `slug` immutable.                 |
| resource    | `janus_environment`      | `name` renamable; `slug`/`project_id` immutable.             |
| resource    | `janus_config`           | Optional `inherits_from`; all attributes immutable.          |
| resource    | `janus_secret`           | `value` **Sensitive**; a write creates one config version.   |
| resource    | `janus_secrets`          | Map of keys, **Sensitive**; the whole batch is one config version. |
| resource    | `janus_service_token`    | `token` **Sensitive computed**, shown once at create; `config`- or `environment`-scoped. |
| resource    | `janus_group`            | The catalog entry: `local` or `oidc`; only `can_create_projects` updates in place. |
| resource    | `janus_group_member`     | Membership of a **local** group; refused at plan for an `oidc` one. |
| resource    | `janus_group_binding`    | The grant: group × scope × role. Never `owner`. **Different authority** from the two above. |
| data source | `janus_secret`           | Reads a value (**Sensitive**, audited `secret.reveal`).      |
| data source | `janus_config`           | Reads config metadata (no values).                           |

Full schema tables, import syntax, and per-attribute behavior are in the
provider's [README](../../terraform-provider-janus/README.md).

## How it maps to Janus concepts

- A `janus_secret` write goes through `PUT /v1/configs/{cid}/secrets/{key}`,
  which creates a new **immutable config version** — so each apply that
  changes a value bumps the version, exactly as the [data model](../data-model.md)
  describes. A `janus_secrets` apply goes through the batch route
  `PUT /v1/configs/{cid}/secrets` instead, so N keys share **one** version.
- Reading a secret (the `janus_secret` data source, or a resource `Read`) hits
  the audited reveal endpoint, so it is recorded as a `secret.reveal`
  [audit event](../architecture.md).
- Deleting a `janus_project` / `janus_environment` / `janus_config` performs a
  **soft delete** (recoverable from [Trash](trash-and-recovery.md)); it is not
  a hard destroy.
- A `404` on any resource read removes it from state so Terraform plans a
  recreate — drift detection for out-of-band deletions.

## Testing & development

The provider's unit tests are **hermetic**: they run against an in-process
fake Janus (`httptest.Server`) and drive the API client and resource
CRUD/mapping directly, with no live server and no `terraform` binary required.

```sh
cd terraform-provider-janus
go test ./...
```

## Limitations / roadmap

- `janus_secrets` detects drift from **metadata only** (`value_version`), never
  from stored plaintext — see the drift section above.
- The mint API accepts `ttl_seconds` and `ip_allowlist`; `janus_service_token`
  does not expose them yet.
- A group has **no rename**: Janus exposes no update route for `name`,
  `description` or `claim_value`, so changing any of them forces replacement —
  which drops every binding the group held and re-creates them in the same
  apply. Rename deliberately, not casually.
- `janus_group_member` for a group created in the **same apply** is checked in
  `Create` rather than at `plan` (the group id is unknown until then). It still
  fails before any write.
- Registry publication and generated docs (`tfplugindocs`) are pending.

See also: [Service tokens](service-tokens.md), [Managing secrets](managing-secrets.md),
[Go SDK](go-sdk.md).
