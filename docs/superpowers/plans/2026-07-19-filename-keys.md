# Filename-style Secret Keys Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Allow filename-style secret keys (`[A-Za-z0-9._-]+`, e.g. `vigil-cloud.secrets.backup.txt`), make `janus run` + `.env` export skip non-env-var-safe keys with a warning, and add a `download --format files` mode that materializes each secret to `<dir>/<key>`.

**Architecture:** Relax the server-side `validateKey` to a flat-filename charset (strict superset of the old identifier rule). A new client-side `isEnvVarName` predicate (the old rule) gates env-var injection in `janus run` and `.env` export — non-injectable keys are skipped with a warning. A new `files` download format writes one traversal-guarded file per secret. No migration.

**Tech Stack:** Go (cobra CLI, net/http client), React+TS, Vitest.

---

## Reference facts (verified against the code)

- Server validation: `internal/secrets/validation.go` — `keyRe = ^[A-Za-z_][A-Za-z0-9_]*$`, `validateKey(k)` returns `ErrValidation` on mismatch. Callers: `secrets.go:40,111,166`, `versions.go:35` (unchanged by this work).
- CLI `run`: `cmd/janus/run.go:42` calls `buildChildEnv(os.Environ(), resp.Secrets, preserveEnv) []string` (`cmd/janus/runenv.go:11`), which merges parent env + secrets (`strings.Cut`, `sort.Strings`). `buildChildEnv` is called ONLY here.
- CLI download: `cmd/janus/secrets_download.go` — validates the format via `formatSecrets(format, map{})` early, `--plain` guard when `output != ""`, GETs `/v1/configs/{cid}/secrets?reveal=true`, then `formatSecrets` + `writeSecretFile(output, data)` (atomic O_EXCL 0600). `formatSecrets(format, m)` (`cmd/janus/formats.go:73`) switches env→`formatEnv(m)`, json→`formatJSON`, yaml→`formatYAML`, default→error.
- Web editor: `web/src/secrets/SecretEditor.tsx:438` renders `<AddKeyRow onAdd={(k,v)=>setBuffer((b)=>addKey(b,k,v))} />`. `web/src/secrets/rowState.ts:39` has `KEY_RE = /^[A-Za-z_][A-Za-z0-9_]*$/` (used by `parseDotenv` import).
- Toolchain: Docker/testcontainers; go.mod pins `toolchain go1.26.5`. Web `npm test` is WATCH → `npm test -- --run`; tsconfig ES2020; token-only + guards.

---

## Task 1: Relax server-side `validateKey`

**Files:**
- Modify: `internal/secrets/validation.go`
- Test: `internal/secrets/validation_test.go` (create if absent, else append)

- [ ] **Step 1: Write the failing test**

```go
package secrets

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateKey_FilenameCharset(t *testing.T) {
	ok := []string{"API_KEY", "vigil-cloud.secrets.backup.txt", ".secrets", "a", "A_B-1.2"}
	for _, k := range ok {
		if err := validateKey(k); err != nil {
			t.Errorf("validateKey(%q) = %v, want nil", k, err)
		}
	}
	bad := []string{"", ".", "..", "a/b", "a\\b", "a b", "a$b", strings.Repeat("x", 256)}
	for _, k := range bad {
		if err := validateKey(k); !errors.Is(err, ErrValidation) {
			t.Errorf("validateKey(%q) = %v, want ErrValidation", k, err)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/secrets/ -run TestValidateKey_FilenameCharset -v`
Expected: FAIL — the hyphen/dot keys are rejected by the current identifier `keyRe`.

- [ ] **Step 3: Implement**

In `internal/secrets/validation.go`, replace the key regex + `validateKey` (keep `strings` imported):
```go
// keyRe allows filename-style secret keys: letters, digits, and . _ - . Keys are
// NOT restricted to env-var identifiers because a secret may be a file (keyed by
// its filename) that is materialized to disk rather than injected via `janus run`.
// Env-var injection is gated separately at run time (see cmd/janus isEnvVarName).
var keyRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// validateKey rejects keys that are not filename-safe. The key name is not secret
// (audit records key names, never values), so it may appear in the error. Rejects
// "."/".." and path separators so a key can never traverse when materialized to a
// file, and caps length at 255 (filesystem limit).
func validateKey(k string) error {
	if k == "" || len(k) > 255 || k == "." || k == ".." ||
		strings.ContainsAny(k, `/\`) || !keyRe.MatchString(k) {
		return fmt.Errorf("%w: key %q must be letters, digits, and . _ - (not '.'/'..' or path separators, ≤255)", ErrValidation, k)
	}
	return nil
}
```
(`strings.ContainsAny(k, "/\\")` is redundant with `keyRe` today, but is belt-and-suspenders against a future charset widening and documents the traversal invariant.)

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/secrets/ -run TestValidateKey_FilenameCharset -v` → PASS. Then `go test ./internal/secrets/` → full package green (existing SetSecrets/reveal tests still pass — identifier keys remain valid).

- [ ] **Step 5: Commit**

```bash
git add internal/secrets/validation.go internal/secrets/validation_test.go
git commit -m "feat(secrets): allow filename-style secret keys (flat charset, no traversal)"
```

---

## Task 2: `janus run` skips non-env-var keys (+ `isEnvVarName` helper)

**Files:**
- Create: `cmd/janus/envname.go` + `cmd/janus/envname_test.go`
- Modify: `cmd/janus/runenv.go` (`buildChildEnv` skips + returns skipped names), `cmd/janus/run.go` (print warning)
- Test: `cmd/janus/runenv_test.go` (create/append)

- [ ] **Step 1: Write failing tests**

`cmd/janus/envname_test.go`:
```go
package main

import "testing"

func TestIsEnvVarName(t *testing.T) {
	for _, k := range []string{"API_KEY", "_x", "A1"} {
		if !isEnvVarName(k) { t.Errorf("%q should be an env var name", k) }
	}
	for _, k := range []string{"", "1A", "a-b", "a.b", "vigil-cloud.secrets.backup.txt", "a/b"} {
		if isEnvVarName(k) { t.Errorf("%q should NOT be an env var name", k) }
	}
}
```
`cmd/janus/runenv_test.go` (append or create; mirror the existing buildChildEnv test if one exists):
```go
func TestBuildChildEnv_SkipsNonEnvVarKeys(t *testing.T) {
	env, skipped := buildChildEnv(nil, map[string]string{
		"API_KEY": "v1",
		"vigil-cloud.secrets.backup.txt": "file-contents",
	}, false)
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "API_KEY=v1") {
		t.Errorf("API_KEY should be injected; env=%v", env)
	}
	if strings.Contains(joined, "vigil-cloud") {
		t.Errorf("non-env-var key must NOT be injected; env=%v", env)
	}
	if len(skipped) != 1 || skipped[0] != "vigil-cloud.secrets.backup.txt" {
		t.Errorf("skipped = %v, want [vigil-cloud.secrets.backup.txt]", skipped)
	}
}
```
(Add `import "strings"` / `"testing"` as needed.)

- [ ] **Step 2: Run to verify fail**

Run: `go test ./cmd/janus/ -run 'IsEnvVarName|BuildChildEnv' -v`
Expected: FAIL — `isEnvVarName` undefined; `buildChildEnv` returns one value not two.

- [ ] **Step 3: Implement**

`cmd/janus/envname.go`:
```go
package main

import "regexp"

// envVarRe is the POSIX-ish env-var identifier rule. Only keys matching it can be
// injected by `janus run`; filename-style keys (dots/dashes) are skipped.
var envVarRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func isEnvVarName(k string) bool { return envVarRe.MatchString(k) }
```

`cmd/janus/runenv.go` — change `buildChildEnv` to skip non-env-var secret keys and return the skipped names:
```go
func buildChildEnv(parent []string, secrets map[string]string, preserveEnv bool) (env []string, skipped []string) {
	merged := make(map[string]string, len(parent)+len(secrets))
	for _, e := range parent {
		if k, v, ok := strings.Cut(e, "="); ok {
			merged[k] = v
		}
	}
	for k, v := range secrets {
		if !isEnvVarName(k) {
			skipped = append(skipped, k)
			continue
		}
		if preserveEnv {
			if _, exists := merged[k]; exists {
				continue
			}
		}
		merged[k] = v
	}
	out := make([]string, 0, len(merged))
	for k, v := range merged {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	sort.Strings(skipped)
	return out, skipped
}
```

`cmd/janus/run.go` — update the call site (line ~42):
```go
env, skipped := buildChildEnv(os.Environ(), resp.Secrets, preserveEnv)
if len(skipped) > 0 {
	fmt.Fprintf(cmd.ErrOrStderr(), "janus: skipped %d secret(s) not usable as env vars: %s\n",
		len(skipped), strings.Join(skipped, ", "))
}
return execChild(cmdArgs[0], cmdArgs[1:], env)
```
(Add `"strings"` to `run.go` imports.)

- [ ] **Step 4: Run to verify pass**

Run: `go test ./cmd/janus/ -run 'IsEnvVarName|BuildChildEnv' -v` → PASS. Then `go test ./cmd/janus/` → green (fix any other `buildChildEnv` call site — there are none besides run.go).

- [ ] **Step 5: Commit**

```bash
git add cmd/janus/envname.go cmd/janus/envname_test.go cmd/janus/runenv.go cmd/janus/run.go cmd/janus/runenv_test.go
git commit -m "feat(cli): janus run skips non-env-var secret keys with a warning"
```

---

## Task 3: `.env` export skips non-env-var keys

**Files:**
- Modify: `cmd/janus/formats.go` (`formatEnv`)
- Test: `cmd/janus/formats_test.go` (append)

- [ ] **Step 1: Write the failing test**

Read `formatEnv` in `formats.go` first (to match its output style). Append:
```go
func TestFormatEnv_SkipsNonEnvVarKeys(t *testing.T) {
	out := string(formatEnv(map[string]string{
		"API_KEY": "v1",
		"vigil-cloud.secrets.backup.txt": "x",
	}))
	if !strings.Contains(out, "API_KEY=") {
		t.Errorf("API_KEY should be present:\n%s", out)
	}
	if strings.Contains(out, "vigil-cloud.secrets.backup.txt=") {
		t.Errorf("non-env-var key must not be an assignment:\n%s", out)
	}
	if !strings.Contains(out, "# skipped: vigil-cloud.secrets.backup.txt") {
		t.Errorf("expected a skipped comment:\n%s", out)
	}
}
```

- [ ] **Step 2: Run to verify fail**

Run: `go test ./cmd/janus/ -run TestFormatEnv_SkipsNonEnvVarKeys -v`
Expected: FAIL — currently every key is emitted as an assignment.

- [ ] **Step 3: Implement**

In `cmd/janus/formats.go` `formatEnv`: before emitting each key as `KEY=value`, branch on `isEnvVarName(k)`. For non-env-var keys, emit a comment `# skipped: <k>` (grouped, e.g. after the assignments) instead of an assignment. Keep deterministic ordering (the function already sorts keys — preserve that; emit skipped comments in sorted order too). Do NOT change json/yaml formatting (they accept any key).

- [ ] **Step 4: Run to verify pass**

Run: `go test ./cmd/janus/ -run 'FormatEnv|Format' -v` → PASS. `go test ./cmd/janus/` → green.

- [ ] **Step 5: Commit**

```bash
git add cmd/janus/formats.go cmd/janus/formats_test.go
git commit -m "feat(cli): .env export skips non-env-var keys with a comment"
```

---

## Task 4: `download --format files` (materialize to disk)

**Files:**
- Modify: `cmd/janus/secrets_download.go`
- Test: `cmd/janus/secrets_download_test.go` (create/append)

- [ ] **Step 1: Write the failing test**

```go
func TestMaterializeSecrets_WritesOnePerKey(t *testing.T) {
	dir := t.TempDir()
	err := materializeSecrets(dir, map[string]string{
		"vigil-cloud.secrets.backup.txt": "line1\nline2\n",
		"API_KEY": "v1",
	})
	if err != nil { t.Fatalf("materialize: %v", err) }
	b, err := os.ReadFile(filepath.Join(dir, "vigil-cloud.secrets.backup.txt"))
	if err != nil || string(b) != "line1\nline2\n" {
		t.Errorf("file contents = %q err=%v", b, err)
	}
}

func TestMaterializeSecrets_RefusesTraversal(t *testing.T) {
	dir := t.TempDir()
	for _, bad := range []string{"..", "../escape", "a/b", "a\\b", "."} {
		if err := materializeSecrets(dir, map[string]string{bad: "x"}); err == nil {
			t.Errorf("materialize key %q should refuse traversal", bad)
		}
	}
	// Nothing escaped the dir:
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape")); err == nil {
		t.Errorf("a file escaped the output dir")
	}
}
```
(Imports: `os`, `path/filepath`, `testing`.)

- [ ] **Step 2: Run to verify fail**

Run: `go test ./cmd/janus/ -run 'Materialize' -v`
Expected: FAIL — `materializeSecrets` undefined.

- [ ] **Step 3: Implement**

In `cmd/janus/secrets_download.go`:
- Add the helper:
```go
// materializeSecrets writes each secret to <dir>/<key> (value verbatim). It
// re-validates every key against path traversal (defense in depth, independent
// of the server's validateKey) and refuses any key that could escape dir. The
// dir is created 0700; files 0600 via writeSecretFile.
func materializeSecrets(dir string, secrets map[string]string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for k, v := range secrets {
		if k == "" || k == "." || k == ".." || strings.ContainsAny(k, `/\`) {
			return fmt.Errorf("refusing to materialize unsafe key %q", k)
		}
		full := filepath.Join(dir, k)
		if filepath.Dir(full) != filepath.Clean(dir) {
			return fmt.Errorf("refusing to materialize key %q outside %s", k, dir)
		}
		if err := writeSecretFile(full, []byte(v)); err != nil {
			return err
		}
	}
	return nil
}
```
(Add `path/filepath` + `strings` to the file's imports.)
- In the download `RunE`: accept `files` as a valid format. Replace the early `formatSecrets(format, map{})` validation with a check that also allows `files` (e.g. `if format != "files" { if _, err := formatSecrets(format, map[string]string{}); err != nil { return err } }`). After fetching secrets, branch:
```go
if format == "files" {
	if output == "" {
		return fmt.Errorf("--format files requires --output <dir>")
	}
	if !plain {
		return fmt.Errorf("refusing to write plaintext to %s without --plain", output)
	}
	if err := materializeSecrets(output, resp.Secrets); err != nil {
		return err
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "Wrote %d secret(s) as files to %s\n", len(resp.Secrets), output)
	return nil
}
```
Place this branch BEFORE the existing `formatSecrets`/`writeSecretFile` path. Update the `--format` flag help to `env|json|yaml|files` and the `Short` description if you like.

- [ ] **Step 4: Run to verify pass**

Run: `go test ./cmd/janus/ -run 'Materialize|Download|Secrets' -v` → PASS. `go test ./cmd/janus/` → green.

- [ ] **Step 5: Commit**

```bash
git add cmd/janus/secrets_download.go cmd/janus/secrets_download_test.go
git commit -m "feat(cli): download --format files materializes secrets to disk (traversal-guarded)"
```

---

## Task 5: Editor inline key validation

**Files:**
- Modify: `web/src/secrets/rowState.ts` (add `isValidKey`), the Add-key component (find `AddKeyRow` — likely in `SecretEditor.tsx` or its own file)
- Test: `web/src/secrets/rowState.test.ts` (append) + the Add-key component's test

- [ ] **Step 1: Write failing tests**

`rowState.test.ts` (append):
```ts
import { isValidKey } from './rowState'
describe('isValidKey', () => {
  it('accepts filename-style keys', () => {
    expect(isValidKey('API_KEY')).toBe(true)
    expect(isValidKey('vigil-cloud.secrets.backup.txt')).toBe(true)
    expect(isValidKey('.secrets')).toBe(true)
  })
  it('rejects empty, dot/dotdot, slashes, spaces', () => {
    for (const k of ['', '.', '..', 'a/b', 'a\\b', 'a b']) expect(isValidKey(k)).toBe(false)
  })
})
```
Add a test to the Add-key component's test file: typing an invalid key (e.g. `a b` or `a/b`) shows a validation message and disables/blocks Add; a valid filename key (`vigil-cloud.secrets.backup.txt`) is accepted. READ the Add-key component + its existing test first to reuse the harness and find how Add is triggered.

- [ ] **Step 2: Run to verify fail**

Run: `cd web && npm test -- --run src/secrets/rowState.test.ts` (+ the component test)
Expected: FAIL — `isValidKey` not exported / no inline validation.

- [ ] **Step 3: Implement**

In `web/src/secrets/rowState.ts`: export a validator mirroring the backend rule (and keep the existing `KEY_RE` identifier for import if it's used elsewhere — search; if `KEY_RE` is only for parseDotenv, leave it):
```ts
const VALID_KEY_RE = /^[A-Za-z0-9._-]+$/
/** Matches the backend validateKey: filename-safe, not '.'/'..'/slashes, ≤255. */
export function isValidKey(k: string): boolean {
  return k.length > 0 && k.length <= 255 && k !== '.' && k !== '..' && !/[/\\]/.test(k) && VALID_KEY_RE.test(k)
}
/** True if the key is injectable by `janus run` (env-var identifier). */
export function isEnvVarKey(k: string): boolean {
  return /^[A-Za-z_][A-Za-z0-9_]*$/.test(k)
}
```
In the Add-key component: validate the typed key with `isValidKey`; when invalid and non-empty, show a small message ("Key: letters, digits, and . _ - ; no spaces or slashes") and disable the Add action (or block onAdd). When valid but NOT `isEnvVarKey`, show a subtle muted hint ("not an env var — skipped by janus run") so the user understands run behavior. Token classes only.

- [ ] **Step 4: Run to verify pass + guards**

Run: `cd web && npm test -- --run src/secrets/rowState.test.ts <component test> && npm run typecheck && npm test -- --run src/test/no-raw-palette.test.ts`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/secrets/rowState.ts web/src/secrets/*.tsx web/src/secrets/*.test.tsx web/src/secrets/rowState.test.ts
git commit -m "feat(web): inline key validation for filename-style keys + not-env-var hint"
```

---

## Task 6: Full gate + smoke + docs

**Files:**
- Modify: `CHANGELOG.md` (if present); optionally a short note in `docs/`

- [ ] **Step 1: Backend gate**

Run: `go build ./... && go test ./... -race` → all packages pass, no races. `gosec ./...` / `govulncheck ./...` — only pre-existing vendored-Shamir + known `GO-2026-5856` acceptable.

- [ ] **Step 2: Web gate**

Run (from `web/`): `npm test -- --run && npm run typecheck && npm run smoke && npm test -- --run src/test/no-raw-palette.test.ts` (+ `no-legacy-alias.test.ts` if present) → all pass; smoke light+dark OK.

- [ ] **Step 3: Docs**

If `CHANGELOG.md` exists, add an Unreleased entry: "Filename-style secret keys (e.g. `foo.bar.txt`); `janus run`/`.env` skip non-env-var keys; `janus secrets download --format files` materializes each secret to a file." Keep the file's style.

- [ ] **Step 4: Commit (if CHANGELOG touched)**

```bash
git add CHANGELOG.md
git commit -m "docs(changelog): filename-style secret keys + materialize-to-files"
```

---

## Self-review checklist (author)

- **Spec coverage:** A (validateKey) → Task 1; B (run + .env skip) → Tasks 2–3; C (download --format files) → Task 4; D (editor) → Task 5; testing/gate → Task 6. All covered.
- **Type consistency:** `validateKey` (server, T1); `isEnvVarName` (cmd/janus, T2) used by `buildChildEnv` (T2) + `formatEnv` (T3); `materializeSecrets(dir, secrets)` (T4); `isValidKey`/`isEnvVarKey` (web, T5). `buildChildEnv` new 2-value return updated at its only call site (run.go).
- **No migration**; keys are `text`; new rule is a superset for identifiers so existing data/behavior is unaffected except the intended run/.env skip.
- **Security:** traversal guarded in BOTH `validateKey` (write) and `materializeSecrets` (write-to-disk); `--plain` required for `--format files`; values written 0600, dir 0700, atomic O_EXCL.
- **Open verification points flagged inline** (formatEnv output style, AddKeyRow location/harness, whether `KEY_RE` is used beyond parseDotenv) — implementer confirms against the code.
