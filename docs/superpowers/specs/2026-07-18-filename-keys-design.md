# Filename-style secret keys + materialize-to-files — design

_Date: 2026-07-18. Relaxes the secret-key charset to allow filename-style names (e.g. `vigil-cloud.secrets.backup.txt`) and adds a download-to-files mode so a secret can round-trip to a file on disk. Motivated by a real failure: adding a multiline secret keyed by a filename was rejected._

## Problem

Secret keys are restricted to environment-variable identifiers (`^[A-Za-z_][A-Za-z0-9_]*$`, `internal/secrets/validation.go`), because the flagship `janus run` injects them as env vars. A user storing a backup file's contents keyed by its filename (`vigil-cloud.secrets.backup.txt`) gets a validation error at save — the key has `.` and `-`, both disallowed. The multiline **value** is fine; only the **key** is rejected. The user's goal: store file contents under the filename and later **download it back to a file by that name**.

## Verified constraints (why this isn't just widening a regex)

- **`janus run`** builds the child env from resolved secrets (`cmd/janus/run.go` → `buildChildEnv`); env var names can't contain `.`/`-`/`/`.
- **Secret references** split on dots: `${projects.p.e.c.KEY}` and local `${KEY}` (`internal/resolve/parse.go`). `parseRefBody` splits the body by `.` into exactly 1 (local) or 5 (absolute) tokens, each a `validSegmentToken` (letters/digits/`_`/`-`). A key containing `.` therefore **cannot be referenced** — the parser errors clearly (not silent). Note a pre-existing inconsistency: `validSegmentToken` already allows `-`, but write-validation didn't.
- **Downloads**: `janus secrets download --format env|json|yaml` (`cmd/janus/secrets_download.go`) with `--output` requiring `--plain`. A dotted key makes an invalid `.env`.
- **Storage**: `secret_values.key` / `config_version_entries.key` are `text` — no schema constraint, **no migration** needed.

## Decisions (from brainstorming)

1. **Charset**: flat filenames `^[A-Za-z0-9._-]+$`; no slashes/subdirectories; reject `.`/`..`. (User: download-to-files, flat.)
2. **`janus run`**: inject only env-var-safe keys; **skip** the rest with a stderr warning. (User: skip-with-warning.)
3. **Download-to-files** round-trip is the goal: a new `--format files` mode. (User: download it back to a file by that name.)
4. **References**: dotted keys are simply not referenceable — accepted; the parser already errors on them.

## Approach

Relax `validateKey` to the flat-filename charset; make the *old* identifier rule a reusable `isEnvVarName` predicate that `janus run` and the `.env` export use to decide inject-vs-skip; add a `--format files` download mode that writes each secret to `<dir>/<key>`. No migration; the new key rule is a strict superset for existing identifier keys.

Rejected: a separate `filename` metadata field + materialize-by-filename (cleaner run/ref safety, but adds a field + migration and doesn't match "the key is the filename"); hyphens-only (doesn't allow dots).

## Section A — Key validation (core)

`internal/secrets/validation.go`:
- `keyRe` becomes `^[A-Za-z0-9._-]+$`. `validateKey(k)` additionally rejects: `k == "." || k == ".."` (dot/dotdot), any `/` or `\` (belt-and-suspenders even though the charset excludes them — future-proofs against a widened charset), and `len(k) > 255`. On failure the error names the allowed set ("letters, digits, and . _ - ; not '.'/'..'; no slashes; ≤255").
- `isEnvVarName(k) = ^[A-Za-z_][A-Za-z0-9_]*$` (the OLD rule) is the predicate for "injectable as an env var." It's needed **client-side** in `cmd/janus` (the CLI is an HTTP client — `run`/export resolve secrets over the API, not via `internal/secrets`), so it lives as a tiny helper in `cmd/janus` (e.g. `cmd/janus/envname.go`). The server-side `validateKey` and the client-side `isEnvVarName` are independent trivial regexes; no shared import.
- Existing identifier keys stay valid. No migration.

## Section B — `janus run` + `.env` export skip non-injectable keys

- `cmd/janus/run.go` `buildChildEnv`: iterate resolved secrets; for each key, if `isEnvVarName(key)` inject `KEY=VALUE`, else collect it into a skipped list. After building, if the skipped list is non-empty, print ONE line to **stderr**: `janus: skipped N secret(s) not usable as env vars: name1, name2, …`. Identifier keys behave exactly as today; ordering/precedence with the parent env unchanged.
- `janus secrets download --format env` (dotenv): skip non-`isEnvVarName` keys, emitting a `# skipped: <name>` comment line so the output stays a valid `.env`. `json`/`yaml` formats include ALL keys (plain maps; a dotted key is a valid JSON/YAML map key).

## Section C — Download-to-files (the round-trip)

`cmd/janus/secrets_download.go`:
- New `--format files`. With it, `--output <dir>` is the **directory** (not a file) and `--plain` is REQUIRED (writing plaintext to disk). Each resolved secret is written to `<dir>/<key>` with the value as the file contents (verbatim, multiline preserved). The directory is created if absent (`0700`); files written `0600` (mirroring the existing download's restrictive perms).
- **Traversal guard** at write time (defense in depth, independent of `validateKey`): for each key, re-check it contains no `/`, `\`, is not `.`/`..`, and that `filepath.Join(dir, key)` cleaned still has `dir` as prefix; refuse (error, abort) on anything suspicious rather than writing outside `<dir>`.
- Without `--format files`, behavior is unchanged. Reuses the existing auth + resolved-secrets fetch.

## Section D — Editor inline key validation

`web/src/secrets/` (the Add-key row / `SecretEditor`):
- Add inline validation to the Add-key input matching the new backend rule (`^[A-Za-z0-9._-]+$`, not `.`/`..`): show a clear message ("Key: letters, digits, and . _ - ; no spaces or slashes") and disable Add on an invalid key, so it fails fast instead of at Save. Reuse/adjust the existing key regex in `web/src/secrets/rowState.ts` (currently the strict identifier `KEY_RE`, used for .env import) — introduce a shared `isValidKey`/`isEnvVarKey` pair so import and add agree.
- A subtle, non-blocking hint marks keys that aren't env-injectable (e.g. a small "not an env var" tag or muted note) so the user knows `janus run` will skip them. Multiline value + `note`/`certificate` types already provide the big box.
- Token classes only; dual-theme.

## Data flow

Write: editor/CLI/API key → `validateKey` (relaxed) → stored `text`. Run: resolved secrets → `buildChildEnv` injects `isEnvVarName` keys, skips + warns on the rest. Download `--format files`: resolved secrets → one file per key under `<dir>`, traversal-guarded. Download `--format env`: skips non-identifier keys with a comment.

## Error handling

- Invalid key (bad charset / `.`/`..` / too long) → `ErrValidation` → 400 (API) / CLI error, message names the allowed set.
- `--format files` without `--plain` → CLI error (existing plaintext guard). Traversal-suspicious key at materialize → abort with a clear error (should be unreachable given `validateKey`, but enforced).
- Referencing a dotted key (`${…dotted…}`) → existing `ErrBadReferenceSyntax` (unchanged; clear).

## Testing

- **Backend**: `validateKey` table — accepts `vigil-cloud.secrets.backup.txt`, `.secrets`, `A_B`, rejects ``/`.`/`..`/`a/b`/`a\b`/`a b`/overlong. `isEnvVarName` accepts identifiers, rejects dotted/hyphenated. Leak tests unaffected (keys aren't secret).
- **CLI**: `run` injects identifier keys, skips + warns on a dotted key (assert the warning + that the env lacks the dotted key). `download --format files --output <dir> --plain` writes `<dir>/<key>` with the value; refuses without `--plain`; a crafted traversal key never writes outside `<dir>`. `download --format env` skips non-identifier keys with a comment; `json` includes them.
- **Web**: Add-key inline validation accepts filename keys, rejects `..`/spaces/slashes, disables Add on invalid; the "not env-injectable" hint appears for dotted keys. Token/dual-theme guards.
- Gates: `go test ./... -race`, gosec/govulncheck, web tests/typecheck/smoke/palette. No migration.

## Non-goals

- Subdirectories / slashes in keys (flat filenames only).
- Binary/file **upload** or size caps beyond existing limits (this stores text values; the "files as secrets" upload model stays dropped).
- Making dotted keys referenceable (they're not; parser errors).
- Sanitizing keys into env-var names for `run` (skip-with-warning instead).
- A separate `filename` metadata field (the key *is* the filename).

## Rollout

Validation + CLI + editor; **no migration**. After merge: rebuild dev containers (`docker compose up -d --build`) + `dev-unseal.sh`. Update gaps.md / status if relevant.
