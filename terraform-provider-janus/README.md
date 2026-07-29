# terraform-provider-janus

A [Terraform](https://www.terraform.io/) provider for the
[Janus](../README.md) self-hosted secrets manager. It lets infra teams manage
Janus **projects, environments, configs, secrets (one key at a time or a whole
batch), config/environment-scoped service tokens, and groups with their local
membership and scope bindings** declaratively instead of clicking the UI or
scripting `curl` against the REST API.

The provider is a **self-contained Go module** (`github.com/steveokay/janus-secrets/terraform-provider-janus`)
built on the [terraform-plugin-framework](https://github.com/hashicorp/terraform-plugin-framework).
It talks to Janus over the `/v1` HTTP API with a small in-module client and
never imports the Janus server's internal packages.

> **Warning — secrets in state.** Terraform stores every managed attribute in
> state, including `janus_secret.value`, every value in the `janus_secrets.secrets`
> map, and the once-only `janus_service_token.token`.
> **Use a sensitive/remote state backend**
> (encrypted S3 + DynamoDB lock, Terraform Cloud, etc.) and restrict access to
> state. These attributes are marked `Sensitive` so they never render in plan
> output, but they are still persisted in state.

## Installation (local build)

This provider is not (yet) published to the Terraform Registry. Build it and
install it into the local plugin mirror:

```sh
cd terraform-provider-janus
go build -o terraform-provider-janus

# Install to the CLI filesystem mirror (adjust OS_ARCH, e.g. linux_amd64):
VERSION=0.1.0
OS_ARCH=linux_amd64
DEST=~/.terraform.d/plugins/registry.terraform.io/steveokay/janus/${VERSION}/${OS_ARCH}
mkdir -p "$DEST"
cp terraform-provider-janus "$DEST/"
```

Then reference it:

```hcl
terraform {
  required_providers {
    janus = {
      source  = "steveokay/janus"
      version = "0.1.0"
    }
  }
}
```

## Provider configuration

| Attribute  | Required | Env fallback  | Description                                                     |
| ---------- | -------- | ------------- | --------------------------------------------------------------- |
| `endpoint` | yes\*    | `JANUS_ADDR`  | Janus base URL, e.g. `https://janus.example.com` (no `/v1`).    |
| `token`    | yes\*    | `JANUS_TOKEN` | A `janus_svc_`/admin service token. **Sensitive.**              |

\* Each may be supplied via the attribute **or** its environment variable. The
provider configures an `*http.Client` with a 30s timeout.

```hcl
provider "janus" {
  endpoint = "https://janus.example.com" # or export JANUS_ADDR
  token    = var.janus_token             # or export JANUS_TOKEN
}
```

## Resources

### `janus_project`

A top-level project.

| Attribute | Type   | Behavior                                          |
| --------- | ------ | ------------------------------------------------- |
| `slug`    | string | Required, immutable (forces replacement).         |
| `name`    | string | Optional/computed; updatable in place (rename).   |
| `id`      | string | Computed — project UUID.                          |

```hcl
resource "janus_project" "web" {
  slug = "acme-web"
  name = "Acme Web"
}
```

Import: `terraform import janus_project.web <project_uuid>`

### `janus_environment`

An environment within a project (dev/staging/prod).

| Attribute    | Type   | Behavior                                        |
| ------------ | ------ | ----------------------------------------------- |
| `project_id` | string | Required, forces replacement.                   |
| `slug`       | string | Required, immutable (forces replacement).       |
| `name`       | string | Optional/computed; updatable in place (rename). |
| `id`         | string | Computed — environment UUID.                    |

```hcl
resource "janus_environment" "prod" {
  project_id = janus_project.web.id
  slug       = "prod"
  name       = "Production"
}
```

Import: `terraform import janus_environment.prod <project_uuid>/<environment_uuid>`

### `janus_config`

A config within an environment; holds secrets and may inherit from a base
config in the same environment.

| Attribute        | Type   | Behavior                                     |
| ---------------- | ------ | -------------------------------------------- |
| `project_id`     | string | Required (create route), forces replacement. |
| `environment_id` | string | Required, forces replacement.                |
| `name`           | string | Required, forces replacement.                |
| `inherits_from`  | string | Optional base config UUID, forces replacement.|
| `id`             | string | Computed — config UUID.                      |

```hcl
resource "janus_config" "prod_root" {
  project_id     = janus_project.web.id
  environment_id = janus_environment.prod.id
  name           = "prod"
}
```

Import: `terraform import janus_config.prod_root <config_uuid>`
(`project_id` is only used for the create route and refreshes on the next plan.)

### `janus_secret`

A single secret key/value in a config. **Writing a value creates one immutable
config version** server-side.

| Attribute   | Type   | Behavior                                             |
| ----------- | ------ | ---------------------------------------------------- |
| `config_id` | string | Required, forces replacement.                        |
| `key`       | string | Required, forces replacement.                        |
| `value`     | string | Required, **Sensitive**; updatable in place.         |
| `id`        | string | Computed — `"<config_id>/<key>"`.                    |

```hcl
resource "janus_secret" "database_url" {
  config_id = janus_config.prod_root.id
  key       = "DATABASE_URL"
  value     = var.database_url # sensitive
}
```

Import: `terraform import janus_secret.database_url <config_uuid>/<key>`

### `janus_secrets` (batch)

A **whole map** of key/values in one config. Every add, change and removal in an
apply is sent as **one** request to `PUT /v1/configs/{id}/secrets`, so Janus
records **one config version** for the batch instead of one per key.

| Attribute        | Type        | Behavior                                                                    |
| ---------------- | ----------- | --------------------------------------------------------------------------- |
| `config_id`      | string      | Required, forces replacement.                                               |
| `secrets`        | map(string) | Required, **Sensitive** — key → value. Dropping a key tombstones it.        |
| `message`        | string      | Optional/computed (default `terraform apply`) — commit message.             |
| `value_versions` | map(number) | Computed, **not** sensitive — key → server `value_version` (drift ledger).  |
| `config_version` | number      | Computed — config version created by the last apply (`0` if nothing to do). |
| `id`             | string      | Computed — the config UUID.                                                 |

```hcl
resource "janus_secrets" "prod" {
  config_id = janus_config.prod_root.id
  message   = "seed prod from terraform"

  secrets = {
    DATABASE_URL = var.database_url
    STRIPE_KEY   = var.stripe_key
    FEATURE_FLAG = "on"
  }
}
```

Import: `terraform import janus_secrets.prod <config_uuid>` — values cannot be
recovered value-free, so `secrets` starts empty and the first apply rewrites
every key in your configuration (as one config version).

**Drift detection is metadata-only — read this.** The masked list endpoint is
value-free, so the provider *cannot* compare the stored plaintext with your
configuration. Instead it records each key's server-side `value_version` (Janus
bumps that counter on every write) and, on refresh:

- key's version moved → someone wrote it outside Terraform → the key is dropped
  from state so the next plan proposes rewriting it;
- key is missing, or now visible only through config inheritance → treated as
  deleted out of band → the next plan re-adds it;
- version unchanged → assumed to still hold the value in state.

What it therefore does **not** do: tell you what the out-of-band value is, or
notice a value that never matched state in the first place (e.g. straight after
`terraform import`). It is a "someone touched this key" signal, not a value
comparison. Reading values would mean an audited `secret.reveal` of the whole
config on every `terraform plan`, which this resource deliberately avoids.

**Do not manage the same key with `janus_secret` and `janus_secrets`.** Two
owners for one key fight on every apply. `janus_secrets` fails loudly rather
than clobbering: before adopting a key it checks the masked list, and if the key
already exists in that config (origin `own`/`overridden`) it errors and writes
nothing. Keys that merely *inherit* from a base config are fair game — writing
them is a legitimate override. Keys absent from the map are never touched, so
the two resources can coexist in the same config as long as their key sets are
disjoint.

If the config is **protected** (four-eyes / `require_approval`), Janus answers
`202 Accepted` and files the batch as a pending edit request instead of
committing it. The provider treats that as an error — Terraform must not record
a write that is not live.

### `janus_service_token`

A scoped `janus_svc_...` service token. The raw token is returned **once** at
creation as a **sensitive computed** attribute.

| Attribute    | Type   | Behavior                                                                       |
| ------------ | ------ | ------------------------------------------------------------------------------ |
| `name`       | string | Required, forces replacement (tokens are re-minted).                           |
| `scope_kind` | string | Optional/computed, default `config`; `config` or `environment`. Forces replace.|
| `scope`      | string | Required — a config UUID or environment UUID; forces replacement.              |
| `access`     | string | Required — `read` or `readwrite`; forces replacement.                          |
| `token`      | string | Computed, **Sensitive** — the raw token, available once.                       |
| `id`         | string | Computed — token ID (metadata handle, not the secret).                         |

```hcl
# Config-scoped (default): one config, read-only.
resource "janus_service_token" "ci_deploy" {
  name   = "ci-deploy"
  scope  = janus_config.prod_root.id
  access = "read"
}

# Environment-scoped: every config in the environment.
resource "janus_service_token" "prod_reader" {
  name       = "prod-reader"
  scope_kind = "environment"
  scope      = janus_environment.prod.id
  access     = "read"
}

output "ci_token" {
  value     = janus_service_token.ci_deploy.token
  sensitive = true
}
```

`scope_kind` accepts **only** `config` and `environment` — Janus deliberately
has no project-wide or instance-wide service token. An invalid kind is rejected
at `terraform plan` by an attribute validator, before any API call.

Upgrading from a provider version without `scope_kind`: existing tokens refresh
their kind from the server on the next `terraform refresh`/plan, so they are not
planned for replacement.

> The raw token is only shown at mint and cannot be retrieved again. After
> `terraform import janus_service_token.x <token_id>`, `token` is empty — a
> subsequent apply does not re-mint (all attributes force replacement only on
> change). Re-minting requires `terraform taint` / replacement.

### `janus_group`

A **group** — a subject a role binding can target instead of a user, so a whole
team is granted access once. Managing the catalog needs instance-scoped
`group:manage`; *binding* a group is a different authority (see
`janus_group_binding`).

| Attribute             | Type   | Behavior                                                                    |
| --------------------- | ------ | --------------------------------------------------------------------------- |
| `name`                | string | Required; unique across BOTH kinds. No rename route exists → forces replacement. |
| `kind`                | string | Required — `local` or `oidc`. Validated at `plan`. Forces replacement.      |
| `claim_value`         | string | Required for `oidc`, forbidden for `local` (checked at `plan`). Forces replacement. |
| `description`         | string | Optional; no update route → forces replacement.                             |
| `can_create_projects` | bool   | Optional/computed, default `false`. **The only in-place update.**            |
| `id`                  | string | Computed — group UUID.                                                      |

```hcl
resource "janus_group" "payments" {
  name = "Team Payments"
  kind = "local"
}

resource "janus_group" "payments_idp" {
  name        = "Team Payments (Entra)"
  kind        = "oidc"
  claim_value = "8f14e45f-ceea-467a-9d0e-7f4b2a1c9c33"
}
```

Import: `terraform import janus_group.payments <group_uuid>`

**No member count and no member data source, on purpose.** An `oidc` group's
member list is a snapshot refreshed at each sign-in, so it covers only users
Janus has seen sign in. Any count here would read as the group's membership and
would not be it. To see who reaches a scope, use the Members screen, which
resolves it server-side under `member:read`.

Deleting a group cascades its membership and every binding it conferred, so the
access is gone on the next request — Janus resolves permissions per request and
never freezes them into a session.

### `janus_group_member`

Membership of a **local** group.

| Attribute  | Type   | Behavior                                |
| ---------- | ------ | --------------------------------------- |
| `group_id` | string | Required, forces replacement.           |
| `user_id`  | string | Required, forces replacement.           |
| `id`       | string | Computed — `"<group_id>/<user_id>"`.    |

```hcl
resource "janus_group_member" "alice" {
  group_id = janus_group.payments.id
  user_id  = var.alice_user_id
}
```

Import: `terraform import janus_group_member.alice <group_uuid>/<user_uuid>`

**Only valid for `kind = "local"`.** An `oidc` group's membership comes from the
identity provider and is refreshed at each sign-in; the denormalised
`group_kind` column plus a composite foreign key make a hand-added row
*unrepresentable*, which is what keeps "access granted through an IdP group is
fully described by the IdP" true for an access review.

The provider refuses it at `terraform plan` whenever `group_id` is already
concrete (an existing group, an imported one, a literal UUID) by looking the
kind up during plan. When the group is created in the *same* apply its id is
unknown at plan time, so the check runs as a pre-flight in `Create` — same
explanation, no write, just at apply instead. A lookup that fails for any other
reason (unreachable server, a token that cannot read the catalog) is silent by
design: `plan` must not break over it.

### `janus_group_binding`

Bind a group at a scope with a role — the actual grant.

| Attribute        | Type   | Behavior                                                                 |
| ---------------- | ------ | ------------------------------------------------------------------------ |
| `group_id`       | string | Required, forces replacement.                                            |
| `role`           | string | Required — `viewer`, `developer` or `admin`. **Never `owner`.** Updatable in place. |
| `project_id`     | string | Optional; set for a project- or environment-scoped binding. Forces replacement. |
| `environment_id` | string | Optional; requires `project_id`. Forces replacement.                     |
| `scope_level`    | string | Computed — `instance` / `project` / `environment`.                       |
| `id`             | string | Computed — see import syntax below.                                      |

| `project_id` | `environment_id` | Scope |
| --- | --- | --- |
| unset | unset | instance-wide |
| set | unset | the whole project (every environment and config in it) |
| set | set | that one environment |

```hcl
resource "janus_group_binding" "payments_on_web" {
  group_id   = janus_group.payments.id
  project_id = janus_project.web.id
  role       = "developer"
}
```

Import: `terraform import janus_group_binding.x project/<project_uuid>/<group_uuid>`
(also `instance/<group_uuid>` and
`environment/<project_uuid>/<environment_uuid>/<group_uuid>` — the id and the
import syntax are the same string).

**`owner` is refused at `terraform plan`**, with its own diagnostic rather than
a generic "invalid value": owner rotates the master key, prunes the audit chain
and hard-destroys secret history, so it must be a direct binding on a person.
Deriving it from group membership would hand that tier to whoever administers
the identity provider — whose membership list Janus cannot authoritatively
enumerate — and would break the never-lock-out guard, which relies on every
instance owner being a direct binding. Both the API and a database `CHECK`
constraint refuse it too.

**This is a different authority from `janus_group`.** The catalog is
instance-scoped `group:manage`; binding is `member:manage` **at that scope**,
capped by your own bound role (measured against your durable role, never a
break-glass elevation). A project admin can therefore grant a group access to
their project but cannot create a group or add themselves to one. If one root
module does both, the token running Terraform needs both — a `403` on one
resource and not the other is that split showing, not a provider bug.

Bindings **union** with direct user bindings; there are no deny rules and no
precedence. A project-scoped grant covers that project's production environment
too — make production four-eyes (`janus env protect prod`) rather than looking
for a narrower binding.

## Data sources

### `janus_secret`

Read a single secret value. **Audited** server-side (`secret.reveal`). The
returned `value` is sensitive and lands in state.

| Attribute   | Type   | Behavior                          |
| ----------- | ------ | --------------------------------- |
| `config_id` | string | Required.                         |
| `key`       | string | Required.                         |
| `value`     | string | Computed, **Sensitive**.          |
| `id`        | string | Computed — `"<config_id>/<key>"`. |

```hcl
data "janus_secret" "db" {
  config_id = janus_config.prod_root.id
  key       = "DATABASE_URL"
}
```

### `janus_config`

Read config metadata (no secret values).

| Attribute        | Type   | Behavior             |
| ---------------- | ------ | -------------------- |
| `id`             | string | Required.            |
| `environment_id` | string | Computed.            |
| `name`           | string | Computed.            |
| `inherits_from`  | string | Computed (nullable). |

## Error handling & drift

The provider maps the Janus error envelope (`{"error":{code,message}}`) to
Terraform diagnostics (the envelope is value-free by design, so no secret leaks
into diagnostics). A `404` on a resource `Read` removes it from state so
Terraform plans a re-create (drift detection).

## Testing

Unit tests are **hermetic** — they run against an in-process `httptest.Server`
faking Janus and drive the API client and the resource CRUD/mapping logic
directly. They do **not** require a live Janus instance or the `terraform`
binary.

```sh
cd terraform-provider-janus
go test ./...
```

Any `resource.Test` acceptance tests (there are none by default) must be gated
behind the `TF_ACC` environment variable.

## Deferred / follow-ups

- **Value-level drift for `janus_secrets`** — would require an audited
  whole-config reveal on every refresh; the metadata (`value_version`) ledger is
  the deliberate trade-off. See the drift note above.
- **Token TTL / IP allowlist** — the mint API accepts `ttl_seconds` and
  `ip_allowlist`; the resource does not expose them yet.
- **Group rename** — Janus exposes no update route for a group's `name`,
  `description` or `claim_value`, so changing any of them forces replacement,
  which drops and re-creates every binding the group held.
- **Registry publication** + generated docs (`tfplugindocs`).

See [`examples/main.tf`](./examples/main.tf) for an end-to-end example, and the
[Terraform guide](../docs/guides/terraform.md) for a walkthrough.
