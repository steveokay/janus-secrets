terraform {
  required_providers {
    janus = {
      source = "steveokay/janus"
    }
  }
}

# Endpoint and token may also come from JANUS_ADDR / JANUS_TOKEN env vars.
provider "janus" {
  endpoint = "https://janus.example.com"
  token    = var.janus_token
}

variable "janus_token" {
  description = "A janus_svc_/admin service token used by Terraform."
  type        = string
  sensitive   = true
}

# 1. Project
resource "janus_project" "web" {
  slug = "acme-web"
  name = "Acme Web"
}

# 2. Environment
resource "janus_environment" "prod" {
  project_id = janus_project.web.id
  slug       = "prod"
  name       = "Production"
}

# 3. Config
resource "janus_config" "prod_root" {
  project_id     = janus_project.web.id
  environment_id = janus_environment.prod.id
  name           = "prod"
}

# 4. Secret (value is sensitive; a write creates one config version)
resource "janus_secret" "database_url" {
  config_id = janus_config.prod_root.id
  key       = "DATABASE_URL"
  value     = var.database_url
}

variable "database_url" {
  description = "Example secret value supplied out-of-band (obviously-fake fixture below)."
  type        = string
  sensitive   = true
  default     = "postgres://placeholder-user@db.internal:5432/acme"
}

# 5. A BATCH of secrets: the whole map is committed as ONE config version.
#    Removing a key from the map tombstones it on the next apply. Do not manage
#    a key here AND with a janus_secret resource — the provider errors rather
#    than clobbering a key it does not already own.
resource "janus_secrets" "prod_bootstrap" {
  config_id = janus_config.prod_root.id
  message   = "seed prod from terraform"

  secrets = {
    STRIPE_KEY   = var.stripe_key
    FEATURE_FLAG = "on"
  }
}

variable "stripe_key" {
  description = "Example secret value supplied out-of-band (obviously-fake fixture below)."
  type        = string
  sensitive   = true
  default     = "sk_test_placeholder_not_a_real_key"
}

# 6. Service token scoped to the config, read-only.
#    The minted token is sensitive and lands in Terraform state — use a
#    sensitive/remote state backend.
resource "janus_service_token" "ci_deploy" {
  name   = "ci-deploy"
  scope  = janus_config.prod_root.id
  access = "read"
}

# 7. Service token scoped to the whole ENVIRONMENT (every config in it).
#    scope_kind accepts only "config" or "environment" — Janus has no
#    project-wide or instance-wide service tokens.
resource "janus_service_token" "prod_reader" {
  name       = "prod-reader"
  scope_kind = "environment"
  scope      = janus_environment.prod.id
  access     = "read"
}

# 8. GROUPS — one binding for a whole team.
#
#    Three resources because Janus splits the authority two ways, on purpose:
#      * the CATALOG (janus_group, janus_group_member) is instance `group:manage`
#      * the BINDING (janus_group_binding) is `member:manage` AT THAT SCOPE,
#        capped by your own bound role.
#    A project admin can apply the binding below but not the group above it.

# A `local` group: membership is the explicit list you manage here.
resource "janus_group" "payments" {
  name        = "Team Payments"
  kind        = "local"
  description = "Payments squad"
}

# Local membership. This resource is REFUSED for an `oidc` group: that
# membership is a snapshot refreshed from the IdP at each sign-in, and the
# schema makes a hand-added row unrepresentable. The provider says so at
# `terraform plan` whenever it can see the group's kind.
resource "janus_group_member" "payments_lead" {
  group_id = janus_group.payments.id
  user_id  = var.payments_lead_user_id
}

variable "payments_lead_user_id" {
  description = "A Janus user UUID (obviously-fake fixture below)."
  type        = string
  default     = "00000000-0000-0000-0000-0000000000aa"
}

# An IdP-fed group: Janus never owns the membership, so there is no
# janus_group_member for it — just the claim value your provider emits.
resource "janus_group" "payments_idp" {
  name        = "Team Payments (Entra)"
  kind        = "oidc"
  claim_value = "8f14e45f-ceea-467a-9d0e-7f4b2a1c9c33"
}

# The grant. `role` accepts viewer/developer/admin only — a group can never be
# owner, and `owner` here fails at `terraform plan`, not mid-apply.
#
# This is a PROJECT-scoped binding, so it covers every environment in the
# project including production. There are no deny rules; protect prod with
# four-eyes (`janus env protect prod`) instead of looking for a narrower grant.
resource "janus_group_binding" "payments_on_web" {
  group_id   = janus_group.payments.id
  project_id = janus_project.web.id
  role       = "developer"
}

# Narrower: the IdP group gets viewer on the prod environment only.
# An environment binding also needs project_id — the route is nested under it,
# and the provider checks that at plan time.
resource "janus_group_binding" "idp_on_prod" {
  group_id       = janus_group.payments_idp.id
  project_id     = janus_project.web.id
  environment_id = janus_environment.prod.id
  role           = "viewer"
}

# Data source: read a single secret value (audited server-side).
data "janus_secret" "db" {
  config_id = janus_config.prod_root.id
  key       = "DATABASE_URL"

  depends_on = [janus_secret.database_url]
}

# Data source: read config metadata.
data "janus_config" "prod" {
  id = janus_config.prod_root.id
}

output "minted_ci_token" {
  value     = janus_service_token.ci_deploy.token
  sensitive = true
}

output "minted_env_token" {
  value     = janus_service_token.prod_reader.token
  sensitive = true
}

# Value-free: the config version the batch created, and the per-key drift ledger.
output "prod_bootstrap_config_version" {
  value = janus_secrets.prod_bootstrap.config_version
}

output "prod_bootstrap_value_versions" {
  value = janus_secrets.prod_bootstrap.value_versions
}

output "prod_config_name" {
  value = data.janus_config.prod.name
}

# Value-free: how wide each group grant is, straight from the plan.
output "payments_binding_scope" {
  value = janus_group_binding.payments_on_web.scope_level
}
