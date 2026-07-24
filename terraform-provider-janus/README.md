# terraform-provider-janus

A [Terraform](https://www.terraform.io/) provider for the
[Janus](../README.md) self-hosted secrets manager. It lets infra teams manage
Janus **projects, environments, configs, secrets, and service tokens**
declaratively instead of clicking the UI or scripting `curl` against the REST
API.

The provider is a **self-contained Go module** (`github.com/steveokay/janus-secrets/terraform-provider-janus`)
built on the [terraform-plugin-framework](https://github.com/hashicorp/terraform-plugin-framework).
It talks to Janus over the `/v1` HTTP API with a small in-module client and
never imports the Janus server's internal packages.

> **Warning — secrets in state.** Terraform stores every managed attribute in
> state, including `janus_secret.value` and the once-only
> `janus_service_token.token`. **Use a sensitive/remote state backend**
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

### `janus_service_token`

A scoped `janus_svc_...` service token. The raw token is returned **once** at
creation as a **sensitive computed** attribute.

| Attribute | Type   | Behavior                                                     |
| --------- | ------ | ------------------------------------------------------------ |
| `name`    | string | Required, forces replacement (tokens are re-minted).         |
| `scope`   | string | Required — a config UUID; forces replacement.                |
| `access`  | string | Required — `read` or `readwrite`; forces replacement.        |
| `token`   | string | Computed, **Sensitive** — the raw token, available once.     |
| `id`      | string | Computed — token ID (metadata handle, not the secret).       |

```hcl
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

> The raw token is only shown at mint and cannot be retrieved again. After
> `terraform import janus_service_token.x <token_id>`, `token` is empty — a
> subsequent apply does not re-mint (all attributes force replacement only on
> change). Re-minting requires `terraform taint` / replacement.

`scope` is a **config** UUID; environment-scoped tokens are a documented
follow-up (see below).

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

- **Environment-scoped service tokens** — `janus_service_token.scope` currently
  targets a config UUID (`kind=config`). An `scope_kind` attribute (or a
  separate resource) for `kind=environment` tokens is a straightforward
  follow-up.
- **Batch secret writes** — each `janus_secret` write is its own config version;
  a future `janus_config_secrets` map resource could batch multiple keys into a
  single version.
- **Registry publication** + generated docs (`tfplugindocs`).

See [`examples/main.tf`](./examples/main.tf) for an end-to-end example, and the
[Terraform guide](../docs/guides/terraform.md) for a walkthrough.
