# How-to: import and export secrets in the web UI

## Bulk import from `.env` or `.properties`

Open a config → **Import…** in the editor toolbar.

1. **Paste** the file contents, or **Choose file…** (`.env`, `.properties`,
   `.txt`). Parsing happens entirely in your browser — nothing is sent
   anywhere until you save.
2. The **preview** lists every parsed line with an action chip:
   - `+ new` — key doesn't exist in this config
   - `~ overwrite` — key exists; its value will be replaced
   - a red error for unparseable lines or invalid keys (line number shown) —
     these can't be selected
3. Untick anything you don't want, then **Stage into draft**. Nothing is
   written yet: the keys land in the editor's dirty buffer as amber rows.
4. Review, then **Save as vN** — the whole import commits as **one immutable
   config version**, so a bad import is one rollback away.

Format support in one tolerant parser:

- **dotenv** — `KEY=value`, `export KEY=value`, `"double quotes"` with
  `\n`/`\t` escapes, `'single quotes'` literal, `#` comments, trailing
  inline comments on unquoted values
- **Java properties** — `key=value`, `key: value`, `key value`, spaces
  around separators, `!` and `#` comments, backslash line continuations,
  `\uXXXX` escapes

Keys are validated with the same rule as the server (letters, digits,
`. _ -`, no slashes, ≤255). Keys that aren't env-var identifiers (e.g.
`app.timeout`, `service-account.json`) import fine and get the `file` badge —
`janus run` skips them; `janus secrets download --format files` materializes
them to disk.

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
