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
