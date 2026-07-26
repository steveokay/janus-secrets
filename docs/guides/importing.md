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

## From the web UI

The web UI has an **import wizard** covering the same three sources, in the
secret editor: open a config → **Import…**.

It works differently from the CLI on purpose. **It is paste-based: you export
from the source system yourself, then paste the result.** The wizard walks you
through it:

1. **Source** — pick `.env / .properties`, Doppler, Vault, or AWS Secrets
   Manager. Janus shows you the **exact command to run** against that system,
   with a copy button. That command is the credential-handling step, and it
   happens on your machine, in a tool you already trust.
2. **Paste the output** (or choose a file). The wizard **auto-detects** which of
   the four shapes you pasted and shows what it recognised — `Detected ·
   Doppler`. Picking a source by hand **forces** that format instead
   (`Forced · Vault (KV v2)`); *Auto-detect* hands the decision back.
3. **Review** — every key previews as `+ new` / `~ overwrite`, or is rejected
   with a reason. Untick anything you don't want, then **Stage into draft**.

Nothing is written until you press **Save as vN** — the whole import commits as
**one immutable config version**, exactly like the CLI's batched save, so a bad
import is one rollback away.

### Why paste, and not "connect to Doppler"

A "connect your Doppler account" button would be less typing. It would also
mean Janus **receives your Doppler, Vault, and AWS credentials** — because a
browser cannot call those APIs directly (CORS, and it would put third-party
credentials in the page), so the request would have to be proxied through the
Janus server.

That would trade away the property stated at the top of this page: *Janus itself
never stores the external credentials and gains no new server endpoints.* A
self-hosted secrets manager that also accumulates credentials to **other**
people's secret stores is a strictly bigger target than one that doesn't. We
keep the property. The wizard adds **no endpoint**, holds **no external
credential**, and makes **no outbound call** — every parser is a pure function
running in your browser tab.

If you want the fetch automated — in CI, or over a whole tree of configs — that
is what `janus import` above is for. It runs on your machine, with your
credentials, and never hands them to the server either.

### What the wizard accepts

| Source | Shape |
| ------ | ----- |
| `.env` / `.properties` | `KEY=value`, `export KEY=value`, `key: value`, comments, quotes, continuations |
| Doppler | a flat JSON object — `{"KEY":"value", …}` |
| Vault KV v2 | the full `{"data":{"data":{…},"metadata":{…}}}` envelope, a bare `{"data":{…}}`, or an already-unwrapped flat object |
| AWS Secrets Manager | `get-secret-value` output (a `SecretString` that is itself JSON fans out to one key per field; any other string becomes one key named after the secret's trailing path segment) and `batch-get-secret-value` output (`SecretValues[]`) |

**Non-string JSON values** (numbers, booleans, nulls, nested objects, arrays)
are **kept as their JSON text** and flagged in the preview with a `json` badge
and a count in the footer — the same rule the CLI applies, so a web import and a
`janus import` land identical values. They are never silently coerced.

**Rejected per key, with the reason shown:** keys that fail the server's key
rule (letters, digits, `. _ -`, no slashes, ≤255), binary secrets
(`SecretBinary`), and Secrets Manager entries that are a plain string with no
`Name` to derive a key from. Rejected keys can't be selected and never carry
their value forward.

## After importing

The import lands as a single config version. Review it in the web UI (config
version diff) or with `janus secrets list` / `janus secrets diff`, and roll back
to the previous version if anything is wrong. For the web-UI export side
(`Download .env`), see [import-export.md](import-export.md).

---

**Trademarks.** Doppler, HashiCorp Vault, and Amazon Web Services (AWS) are
trademarks of their respective owners. Janus is an independent project and is
not affiliated with, endorsed by, or sponsored by any of them; these names are
used only to identify the source systems this command imports from.
