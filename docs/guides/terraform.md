# Terraform provider

`terraform-provider-janus` lets infrastructure teams manage Janus resources
**declaratively** — projects, environments, configs, secrets, and service
tokens — instead of clicking the web UI or scripting `curl` against the REST
API. It is a self-contained Go module in
[`terraform-provider-janus/`](../../terraform-provider-janus/) built on the
[terraform-plugin-framework](https://github.com/hashicorp/terraform-plugin-framework),
and it talks to Janus over the `/v1` HTTP API with a scoped
`janus_svc_`/admin token.

> **Read this first — secrets live in Terraform state.** Terraform persists
> every managed attribute in its state file, including `janus_secret.value`
> and the once-only `janus_service_token.token`. Even though the provider
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

A full runnable version (with variables, data sources, and outputs) lives in
[`terraform-provider-janus/examples/main.tf`](../../terraform-provider-janus/examples/main.tf).

## Resources & data sources at a glance

| Kind        | Name                     | Notes                                                        |
| ----------- | ------------------------ | ------------------------------------------------------------ |
| resource    | `janus_project`          | `name` renamable in place; `slug` immutable.                 |
| resource    | `janus_environment`      | `name` renamable; `slug`/`project_id` immutable.             |
| resource    | `janus_config`           | Optional `inherits_from`; all attributes immutable.          |
| resource    | `janus_secret`           | `value` **Sensitive**; a write creates one config version.   |
| resource    | `janus_service_token`    | `token` **Sensitive computed**, shown once at create.        |
| data source | `janus_secret`           | Reads a value (**Sensitive**, audited `secret.reveal`).      |
| data source | `janus_config`           | Reads config metadata (no values).                           |

Full schema tables, import syntax, and per-attribute behavior are in the
provider's [README](../../terraform-provider-janus/README.md).

## How it maps to Janus concepts

- A `janus_secret` write goes through `PUT /v1/configs/{cid}/secrets/{key}`,
  which creates a new **immutable config version** — so each apply that
  changes a value bumps the version, exactly as the [data model](../data-model.md)
  describes.
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

- Service tokens are **config-scoped** today (`kind=config`);
  environment-scoped tokens are a planned follow-up.
- Each `janus_secret` is its own config version; a future batch resource could
  group multiple keys into one version.
- Registry publication and generated docs (`tfplugindocs`) are pending.

See also: [Service tokens](service-tokens.md), [Managing secrets](managing-secrets.md),
[Go SDK](go-sdk.md).
