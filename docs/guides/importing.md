# How-to: import secrets from Doppler, Vault, or AWS Secrets Manager

`janus import <source>` reads secrets from an external system and writes them
into a target Janus **project → environment → config** as **one config
version**. It is **CLI-first and client-side**: the CLI fetches from the source
and pushes through the authenticated Janus API. Janus itself never stores the
external credentials and gains no new server endpoints.

Three sources are supported:

| Subcommand         | Source                     | Reads                                        |
| ------------------ | -------------------------- | -------------------------------------------- |
| `janus import doppler` | Doppler REST API       | one Doppler config's secrets                 |
| `janus import vault`   | Vault KV v2            | one KV path's key/value map                  |
| `janus import aws-sm`  | AWS Secrets Manager    | every secret under a name prefix             |

## Safety model

- **Dry-run by default.** Every command runs as a dry-run unless you pass
  `--confirm`. A dry-run prints the **key names + count** that *would* be
  imported and the target — never a value — and writes nothing.
- **Values are never printed.** Neither a dry-run nor a real import ever writes
  a secret value to stdout or a log line. Only key names and counts appear.
- **Source credentials are never stored or logged.** They come from flags or
  environment variables and are used only for the one-shot fetch.
- **One config version.** All imported keys are written as a single batched
  save (`Save as vN`), so a bad import is one rollback away.

## Target flags (all sources)

```
--project P    target Janus project slug   (required)
--env E        target Janus environment slug (required)
--config C     target Janus config name     (required)
--create       create the project/env/config if missing
--confirm      actually write (default is --dry-run)
--message M    config-version message for the imported save
--address URL  Janus server address (default: stored/env)
--token T      Janus service token (default: stored/env)
```

You authenticate to **Janus** with your stored session (`janus login`) or a
service token (`--token` / `JANUS_TOKEN`) exactly like every other command.

---

## Doppler

**Credentials:** a Doppler **service token** with read access to the config.
Pass it with `--doppler-token` or the `DOPPLER_TOKEN` environment variable.

**Mapping:** a Doppler project/config → the target Janus project/env/config.
Each Doppler secret name becomes a Janus key; the computed value is imported.

```sh
# Dry-run: see what would land, no writes
DOPPLER_TOKEN=dp.st.xxxxx janus import doppler \
  --doppler-project acme --doppler-config prod \
  --project acme --env prod --config main

# Real import (creating the target tree if needed)
DOPPLER_TOKEN=dp.st.xxxxx janus import doppler \
  --doppler-project acme --doppler-config prod \
  --project acme --env prod --config main \
  --create --confirm --message "seed from Doppler prod"
```

`--doppler-api` overrides the API base URL (defaults to
`https://api.doppler.com`) for self-hosted or testing setups.

---

## Vault (KV v2)

**Credentials:** a Vault **address** and **token**. Pass them with
`--vault-addr` / `--vault-token`, or the standard `VAULT_ADDR` / `VAULT_TOKEN`
environment variables.

**Mapping:** the CLI reads `GET {addr}/v1/{mount}/data/{path}` (KV v2). Each key
in the secret's `data` map becomes a Janus key. String leaves import verbatim;
non-string leaves (numbers, booleans, nested objects) are JSON-encoded so no
data is lost.

```sh
export VAULT_ADDR=https://vault.example:8200
export VAULT_TOKEN=hvs.xxxxx

# Dry-run
janus import vault --vault-mount secret --vault-path myapp/prod \
  --project acme --env prod --config main

# Real import
janus import vault --vault-mount secret --vault-path myapp/prod \
  --project acme --env prod --config main --confirm
```

`--vault-mount` defaults to `secret`.

---

## AWS Secrets Manager

**Credentials:** **static** AWS credentials and a region. Pass them with
`--aws-access-key-id` / `--aws-secret-access-key` (and optional
`--aws-session-token`) / `--aws-region`, or the standard `AWS_ACCESS_KEY_ID`,
`AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`, `AWS_REGION` environment
variables. The importer uses static credentials only — it never silently
borrows the host's instance-profile identity.

**Mapping:** every Secrets Manager secret whose name starts with `--aws-prefix`
is fetched. For each:

- a secret whose value is a **JSON object** fans out to **one Janus key per
  field** (e.g. `{"USERNAME":"…","PASSWORD":"…"}` → `USERNAME`, `PASSWORD`);
- any other value becomes a **single Janus key** named after the secret's
  trailing path segment (the prefix is stripped).

Binary secrets are skipped (only string secrets are importable).

```sh
export AWS_REGION=us-east-1
export AWS_ACCESS_KEY_ID=AKIAxxxxx
export AWS_SECRET_ACCESS_KEY=xxxxx

# Dry-run
janus import aws-sm --aws-prefix prod/myapp/ \
  --project acme --env prod --config main

# Real import
janus import aws-sm --aws-prefix prod/myapp/ \
  --project acme --env prod --config main --confirm
```

---

## After importing

The import lands as a single config version. Review it in the web UI (config
version diff) or with `janus secrets list` / `janus secrets diff`, and roll back
to the previous version if anything is wrong. For the web-UI `.env`/paste
importer, see [import-export.md](import-export.md).
