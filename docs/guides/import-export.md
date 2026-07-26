# How-to: import and export secrets in the web UI

## Bulk import — the wizard

Open a config → **Import…** in the editor toolbar. The wizard has three steps.

**1 · Source.** Pick where the secrets are coming from: `.env / .properties`,
**Doppler**, **Vault (KV v2)**, or **AWS Secrets Manager**. For the three
external systems Janus shows the **exact export command to run**, with a copy
button — e.g. `doppler secrets download --no-file --format json`.

The importer is **paste-based on purpose**: Janus never asks for your Doppler /
Vault / AWS credentials, adds no server endpoint for them, and makes no outbound
call. You export with the tool you already trust; the browser only parses the
result. The reasoning is written up in
[importing.md § Why paste](importing.md#why-paste-and-not-connect-to-doppler).

**2 · Paste the output** — or **Choose file…** (`.env`, `.properties`, `.txt`,
`.json`). Parsing happens entirely in your browser; nothing is sent anywhere
until you save. The wizard **auto-detects** the shape and says what it found
(`Detected · Doppler`). If detection is wrong, clicking a source **forces** that
format (`Forced · Vault (KV v2)`); *Auto-detect* hands the decision back.

**3 · Review.** Every entry previews with an action chip:

- `+ new` — key doesn't exist in this config
- `~ overwrite` — key exists; its value will be replaced
- a red error with the location and reason — these can't be selected
- a `json` badge — a non-string JSON value, kept as its JSON text (see below)
- a `dup` badge — a repeat of an earlier key; the first selected one wins
- a `file` badge — a valid key that isn't an env-var identifier

Use **Select all** / **Select none** or untick individual rows, then **Stage
into draft**. Nothing is written yet: the keys land in the editor's dirty buffer
as amber rows. Review, then **Save as vN** — the whole import commits as **one
immutable config version**, so a bad import is one rollback away.

### Formats

**dotenv / Java properties**, in one tolerant parser:

- **dotenv** — `KEY=value`, `export KEY=value`, `"double quotes"` with
  `\n`/`\t` escapes, `'single quotes'` literal, `#` comments, trailing
  inline comments on unquoted values
- **Java properties** — `key=value`, `key: value`, `key value`, spaces
  around separators, `!` and `#` comments, backslash line continuations,
  `\uXXXX` escapes

**Doppler** — the flat JSON object `doppler secrets download --no-file --format
json` emits: one Janus key per field.

**Vault KV v2** — the full `vault kv get -format=json` envelope
(`{"data":{"data":{…},"metadata":{…}}}`), a bare `{"data":{…}}`, or an
already-unwrapped flat object. `metadata` is never imported as a secret.

**AWS Secrets Manager** — `get-secret-value` output, and
`batch-get-secret-value` output (`SecretValues[]`). A `SecretString` that is
itself a JSON **object** fans out to one key per field; any other string becomes
a single key named after the secret's **trailing path segment**
(`prod/myapp/api-token` → `api-token`). Binary secrets are rejected with a
reason. If AWS reported errors for part of a batch, the wizard says so.

### Non-string values and rejected keys

**Non-string JSON leaves** — numbers, booleans, nulls, nested objects, arrays —
are **kept as their JSON text** (`8080`, `true`, `{"a":1}`), badged `json` in the
preview, and counted in the footer. This is the same rule `janus import` applies
server-side, so a web import and a CLI import land identical values. Nothing is
silently coerced.

Keys are validated with the same rule as the server (letters, digits,
`. _ -`, no slashes, ≤255). A key that fails is **rejected individually** with
the reason and its location shown, and never carries its value forward — the
rest of the import is unaffected. Keys that are valid but aren't env-var
identifiers (e.g. `app.timeout`, `service-account.json`) import fine and get the
`file` badge — `janus run` skips them; `janus secrets download --format files`
materializes them to disk.

## Export — Download .env

Open a config → **Download .env**.

- A confirmation modal states the cost up front: **every value is revealed
  (one audited `secret.reveal` per key) and written as plaintext** to a file
  on your machine. Cancel is a no-op.
- The file is a well-formed dotenv: keys sorted, a header comment naming
  project/env/config and version, values quoted and escaped whenever they
  contain newlines, quotes, spaces, `#`, or `$`.
- Filename-style keys are **skipped with a comment** pointing at
  `janus secrets download --format files`, mirroring the CLI.

Treat the downloaded file like the secrets it contains: it's outside Janus's
protection the moment it hits disk. The CLI equivalent (with the same
guardrail philosophy) is `janus secrets download --format env --plain` — see
[Injecting secrets](injecting-secrets.md).
