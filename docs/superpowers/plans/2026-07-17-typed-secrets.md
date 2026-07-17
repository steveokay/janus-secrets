# Typed Secrets Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give each secret a lightweight `type` (value/password/json/ssh_key/certificate/note) that drives editor rendering (multi-line box, validation, password generate) and a per-type icon — a display/handling hint, not a storage or crypto change.

**Architecture:** A `type` column on `secret_values` (per-version, `DEFAULT 'string'`, migration 000022) threaded through the store `Change`/`SecretValue`, the service `SecretChange`/`Secret`/`MergedMeta`, the API (masked list + reveal + write), promotion and clone carry-through, and a small CLI flag. The frontend adds a capability registry (`secretTypes.ts`) that maps each type to `{multiline, monospace, validate?, generate?, icon}`, plus editor rendering (textarea vs input) and per-type affordances.

**Tech Stack:** Go (pgx, chi), golang-migrate SQL, React+TS+Tailwind+Vitest.

---

## Reference facts (verified against the code)

- `secret_values` (migration 000001) has no `type`. Latest migration is `000021`, so this is `000022`. Migration files live in `migrations/` (`.up.sql`/`.down.sql`), embedded via `migrations/embed.go`.
- Store: `store.SecretValue` (`internal/store/models.go:74`) embeds `EncryptedValue`; `store.Change` (`models.go:87`) = `{Key, Encrypt func(vv int)(*EncryptedValue,error)}`. The INSERT is in `SecretRepo.SaveConfigVersion` (`internal/store/secrets.go:91`, columns `config_id, key, value_version, wrapped_dek, ciphertext, nonce, dek_key_version`). Reads scan the column list at `secrets.go:171/202/282`; `GetLatest`/`GetVersion` return `map[string]SecretValue`.
- Service: `secrets.SecretChange` (`internal/secrets/secrets.go:20`) = `{Key, Value, Delete}`; `secrets.Secret` (`secrets.go:27`) = `{Key, Value, ValueVersion}` built at `secrets.go:122,149,183`. `SetSecrets` (`secrets.go:34`) builds `store.Change` per edit. `MergedMeta` (`internal/secrets/merged_meta.go:14`) = `{Key, ValueVersion, CreatedAt, Origin}`, built via `storeMetaEntry{vv, at}` (`merged_meta.go:21,55,87`).
- API: masked list handler `handleSecretsList` (`internal/api/secrets_handlers.go:74-82`) emits `{value_version, created_at, origin}`; reveal emits `{key, value, value_version}` (`:113`). Write bodies: `secretChangeBody{Key,Value,Delete}` (`internal/api/secrets_write_handlers.go:13`), converted to `SecretChange` at `:80` (batch) and `:104` (per-key PUT).
- Carry-through: promotion builds `SecretChange` at `internal/promote/service.go:304`; clone builds it in `copyOwnSecrets` (`internal/secrets/clone.go`).
- Web: `MaskedSecret` (`web/src/lib/endpoints.ts:30`) = `{value_version, created_at, origin}`. Editor value cell is a single-line `<input>` at `web/src/secrets/SecretTable.tsx:154`; dirty model in `web/src/secrets/dirty.ts` + `rowState.ts`; review diff `ReviewDiffDialog.tsx`; import `importClassify.ts`.
- **Allowed type set (single source of truth):** `string, password, json, ssh_key, certificate, note`. Unknown/absent → normalized to `string` (never errors on read).
- Go toolchain: go.mod pins `toolchain go1.26.5`. Docker available (testcontainers). Web: `npm test` is watch → use `npm test -- --run`; tsconfig ES2020 (no `.at()`); token classes only; guard test `web/src/test/no-raw-palette.test.ts`; smoke `npm run smoke`.

---

## Task 1: Migration + store `type` column

**Files:**
- Create: `migrations/000022_secret_type.up.sql`, `migrations/000022_secret_type.down.sql`
- Modify: `internal/store/models.go` (`SecretValue`, `Change`), `internal/store/secrets.go` (INSERT + 3 SELECT column lists + scans)
- Test: `internal/store/secrets_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `internal/store/secrets_test.go` (reuse the package harness `requireStore(t)`/`resetDB(t)` and whatever helper existing tests use to make a project/env/config; read an existing `SaveConfigVersion` test in this file first and mirror its setup exactly):

```go
func TestSecretRepo_TypePersisted(t *testing.T) {
	st := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	// ... create project p, env e, config c via the same helpers other tests use ...
	sr := store.NewSecretRepo(st)
	_, err := sr.SaveConfigVersion(ctx, c.ID, []store.Change{{
		Key:  "CONF",
		Type: "json",
		Encrypt: func(vv int) (*store.EncryptedValue, error) {
			return &store.EncryptedValue{WrappedDEK: []byte("w"), Ciphertext: []byte("c"), Nonce: []byte("n"), DEKKeyVersion: 1}, nil
		},
	}}, "init", "tester")
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	_, state, err := sr.GetLatest(ctx, c.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if state["CONF"].Type != "json" {
		t.Errorf("Type = %q, want json", state["CONF"].Type)
	}
	// A change with empty Type must default to "string" (DB default).
	_, err = sr.SaveConfigVersion(ctx, c.ID, []store.Change{{
		Key: "PLAIN", Encrypt: func(vv int) (*store.EncryptedValue, error) {
			return &store.EncryptedValue{WrappedDEK: []byte("w"), Ciphertext: []byte("c"), Nonce: []byte("n"), DEKKeyVersion: 1}, nil
		},
	}}, "", "tester")
	if err != nil {
		t.Fatalf("save2: %v", err)
	}
	_, state2, _ := sr.GetLatest(ctx, c.ID)
	if state2["PLAIN"].Type != "string" {
		t.Errorf("empty Type should default to string, got %q", state2["PLAIN"].Type)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/store/ -run TestSecretRepo_TypePersisted -v`
Expected: FAIL — `store.Change` has no field `Type` (compile error).

- [ ] **Step 3: Migration**

`migrations/000022_secret_type.up.sql`:
```sql
ALTER TABLE secret_values ADD COLUMN type text NOT NULL DEFAULT 'string';
```
`migrations/000022_secret_type.down.sql`:
```sql
ALTER TABLE secret_values DROP COLUMN type;
```

- [ ] **Step 4: Thread `Type` through the store**

In `internal/store/models.go`:
- Add `Type string` to `SecretValue` (after `Key`, before `ValueVersion` is fine; keep it a plain field).
- Add `Type string` to `Change`.

In `internal/store/secrets.go`:
- In `SaveConfigVersion`'s INSERT (`:91-92`), add `type` to the column list and bind the value. The current insert passes the `EncryptedValue` fields; add `ch.Type` (defaulting empty→`"string"` so a zero-value Change still satisfies the NOT NULL default explicitly — either pass `"string"` when `ch.Type == ""`, or rely on the column DEFAULT by NOT listing it. Simplest and matches the test: compute `t := ch.Type; if t == "" { t = "string" }` and include `type` in the column list + `t` in the args).
- In the three SELECT column lists (`:171, :202, :282`) add `type`, and in each corresponding `scanSecretValue`/row.Scan add `&sv.Type` in the matching position. Read the exact scan helper (there's likely a `scanSecretValue`) and add the field consistently to ALL read paths (GetLatest, GetVersion, and the version-history read).

- [ ] **Step 5: Run to verify it passes**

Run: `go test ./internal/store/ -run TestSecretRepo_TypePersisted -v`
Expected: PASS. Also run the full package: `go test ./internal/store/` → all green (no scan-arity regressions).

- [ ] **Step 6: Commit**

```bash
git add migrations/000022_secret_type.up.sql migrations/000022_secret_type.down.sql internal/store/models.go internal/store/secrets.go internal/store/secrets_test.go
git commit -m "feat(store): type column on secret_values (migration 000022)"
```

---

## Task 2: Service — allowed types, validation, carry to store, reveal type

**Files:**
- Create: `internal/secrets/types.go` (allowed-set + validation helper)
- Modify: `internal/secrets/secrets.go` (`SecretChange`, `Secret`, `SetSecrets`, the three `Secret{...}` builds)
- Test: `internal/secrets/types_test.go`, `internal/secrets/secrets_test.go` (append)

- [ ] **Step 1: Write failing tests**

`internal/secrets/types_test.go`:
```go
package secrets

import "testing"

func TestNormalizeAndValidateType(t *testing.T) {
	for _, ok := range []string{"string", "password", "json", "ssh_key", "certificate", "note"} {
		if err := validateType(ok); err != nil {
			t.Errorf("%q should be valid: %v", ok, err)
		}
	}
	if normalizeType("") != "string" {
		t.Errorf("empty should normalize to string")
	}
	if normalizeType("bogus") != "bogus" {
		t.Errorf("normalizeType only fills empty; validation rejects unknowns")
	}
	if err := validateType("bogus"); err == nil {
		t.Errorf("bogus should be invalid")
	}
}
```

Append to `internal/secrets/secrets_test.go` a test (reuse the package harness — see `TestCloneEnvironment`/existing tests for how to build a `*Service`, project, env, config) that: `SetSecrets` a key with `Type: "json"`, then `RevealConfig` (or `GetSecret`) and assert the returned `Secret.Type == "json"`; and that `SetSecrets` with `Type: "bogus"` returns a validation error.

- [ ] **Step 2: Run to verify fail**

Run: `go test ./internal/secrets/ -run 'NormalizeAndValidateType|Type' -v`
Expected: FAIL — `validateType`/`normalizeType` undefined; `SecretChange`/`Secret` have no `Type`.

- [ ] **Step 3: Implement `internal/secrets/types.go`**

```go
package secrets

// allowedTypes is the single source of truth for secret display/handling types.
// Type is a hint (rendering/validation/icon), never a storage or crypto concern.
var allowedTypes = map[string]bool{
	"string": true, "password": true, "json": true,
	"ssh_key": true, "certificate": true, "note": true,
}

// normalizeType maps an empty type to the default "string"; non-empty values are
// returned unchanged (validation is a separate step).
func normalizeType(t string) string {
	if t == "" {
		return "string"
	}
	return t
}

// validateType returns ErrValidation for a non-allowed type. Empty is allowed
// (normalized to "string" at the write boundary).
func validateType(t string) error {
	if t == "" || allowedTypes[t] {
		return nil
	}
	return ErrValidation
}
```
(Confirm `ErrValidation` is the package's validation sentinel — it's used elsewhere in `internal/secrets`; if the name differs, use the real one.)

- [ ] **Step 4: Thread through `secrets.go`**

- `SecretChange` (`:20`): add `Type string`.
- `Secret` (`:27`): add `Type string`.
- In `SetSecrets` (`:34`): before building `store.Change`, validate each non-delete change's type: `if err := validateType(ch.Type); err != nil { return store.ConfigVersion{}, err }`. When appending the `store.Change`, set `Type: normalizeType(ch.Type)`.
- In the three `Secret{Key: ..., Value: pt, ValueVersion: sv.ValueVersion}` builds (`:122, :149, :183`), add `Type: sv.Type`.

- [ ] **Step 5: Run to verify pass**

Run: `go test ./internal/secrets/ -run 'NormalizeAndValidateType|Type' -v`
Expected: PASS. Full package: `go test ./internal/secrets/` → green.

- [ ] **Step 6: Commit**

```bash
git add internal/secrets/types.go internal/secrets/types_test.go internal/secrets/secrets.go internal/secrets/secrets_test.go
git commit -m "feat(secrets): validated secret type, carried on write + reveal"
```

---

## Task 3: `MergedMeta.Type` for the masked list

**Files:**
- Modify: `internal/secrets/merged_meta.go`
- Test: `internal/secrets/merged_meta_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append a test (reuse this file's harness) that writes a key with `Type: "password"`, calls `ListSecretsMerged`, and asserts the returned `MergedMeta` for that key has `Type == "password"`. Mirror an existing `merged_meta_test.go` test's setup.

- [ ] **Step 2: Run to verify fail**

Run: `go test ./internal/secrets/ -run Merged -v`
Expected: FAIL — `MergedMeta` has no `Type`.

- [ ] **Step 3: Implement**

In `internal/secrets/merged_meta.go`:
- Add `Type string` to `MergedMeta` (`:14`).
- Add `typ string` to `storeMetaEntry` (`:21`).
- Where `lvl[k] = storeMetaEntry{vv: sv.ValueVersion, at: sv.CreatedAt}` (`:55`), add `typ: sv.Type`.
- Where `out = append(out, MergedMeta{Key: k, ValueVersion: e.vv, CreatedAt: e.at, Origin: origin})` (`:87`), add `Type: e.typ`.

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/secrets/ -run Merged -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/secrets/merged_meta.go internal/secrets/merged_meta_test.go
git commit -m "feat(secrets): surface type in the inheritance-merged masked view"
```

---

## Task 4: API — write parses type, masked list + reveal emit type, OpenAPI

**Files:**
- Modify: `internal/api/secrets_write_handlers.go` (`secretChangeBody`, both conversions), `internal/api/secrets_handlers.go` (masked list + reveal emit), `docs/openapi.yaml`
- Test: `internal/api/secrets_e2e_test.go` (or the existing secrets handler test file — find it) 

- [ ] **Step 1: Write the failing test**

In the existing API secrets test file (search `internal/api` for a test that PUTs `/v1/configs/{cid}/secrets` and GETs the masked list), add a test: batch-write a key with `"type":"json"`, GET the masked list and assert the key's entry has `"type":"json"`; reveal the key (`GET .../secrets/{key}`) and assert the response has `"type":"json"`; batch-write with `"type":"bogus"` → 400.

- [ ] **Step 2: Run to verify fail**

Run: `go test ./internal/api/ -run 'SecretType|Type' -v`
Expected: FAIL (type absent / bogus not rejected).

- [ ] **Step 3: Implement**

- `secrets_write_handlers.go`: add `Type string \`json:"type"\`` to `secretChangeBody` (`:13`). Batch conversion (`:80`): `secrets.SecretChange{Key: c.Key, Value: []byte(c.Value), Delete: c.Delete, Type: c.Type}`. Per-key PUT (`:104`): if that request body has a type field, thread it; if the per-key PUT body is a separate struct, add `Type` there too and pass `Type: req.Type`.
- `secrets_handlers.go`: masked list emit (`:74-82`) add `"type": m.Type`. Reveal emit (`:113` and the historical/versioned reveal emit near `:164`) add `"type": sec.Type`.
- `docs/openapi.yaml`: add `type` (enum: string/password/json/ssh_key/certificate/note) to the masked-secret schema, the reveal-secret schema, and the write-change request schema. No new routes → drift test stays green.

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/api/ -run 'SecretType|Type|Drift|OpenAPI' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/secrets_write_handlers.go internal/api/secrets_handlers.go internal/api/*_test.go docs/openapi.yaml
git commit -m "feat(api): secret type on write, masked list, and reveal"
```

---

## Task 5: Carry type through promotion and clone

**Files:**
- Modify: `internal/promote/service.go` (`:304`), `internal/secrets/clone.go` (`copyOwnSecrets`)
- Test: `internal/promote/*_test.go` (append), `internal/secrets/clone_test.go` (append)

- [ ] **Step 1: Write failing tests**

- Promotion: in the promote test package, add/extend a test so the source key has `Type: "json"`, run a promote, and assert the promoted target key's revealed `Secret.Type == "json"` (reveal the target config). Mirror an existing promote test's setup.
- Clone: extend `internal/secrets/clone_test.go`'s `TestCloneEnvironment` (or add a sibling) — give the source `root` config's secret `Type: "ssh_key"`, clone, and assert `GetSecret(newRoot, key).Type == "ssh_key"`.

- [ ] **Step 2: Run to verify fail**

Run: `go test ./internal/promote/ ./internal/secrets/ -run 'Promote|Clone' -v`
Expected: FAIL — type not carried (comes back `"string"`).

- [ ] **Step 3: Implement**

- `internal/promote/service.go:304`: change to `changes = append(changes, secrets.SecretChange{Key: sel.Key, Value: append([]byte(nil), sec.Value...), Type: sec.Type})`.
- `internal/secrets/clone.go` `copyOwnSecrets`: where it builds `SecretChange{Key: sec.Key, Value: sec.Value}`, add `Type: sec.Type`.

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/promote/ ./internal/secrets/ -run 'Promote|Clone' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/promote/service.go internal/secrets/clone.go internal/promote/*_test.go internal/secrets/clone_test.go
git commit -m "feat: carry secret type through promotion and clone"
```

---

## Task 6: CLI — `secrets set --type` and `secrets list` TYPE column

**Files:**
- Modify: `cmd/janus/secrets_write_cmd.go` (add `--type`), `cmd/janus/secrets_list_cmd.go` (or wherever `secrets list` renders — find it), the client helper that posts writes
- Test: `cmd/janus/*_test.go` (append)

- [ ] **Step 1: Write the failing test**

Find the CLI test harness (search `cmd/janus` for a test that runs `secrets set`/`secrets list` against a fake or real server). Add a test asserting: `secrets set KEY=VALUE --type json` sends `type: "json"` in the write payload (assert against the captured request or the resulting masked list), and `secrets list` output includes a `TYPE` column showing `json`.

- [ ] **Step 2: Run to verify fail**

Run: `go test ./cmd/janus/ -run 'SecretsSet|SecretsList|Type' -v`
Expected: FAIL (no `--type` flag / no TYPE column).

- [ ] **Step 3: Implement**

- `secrets_write_cmd.go`: add `var typ string`; `cmd.Flags().StringVar(&typ, "type", "", "secret type: string|password|json|ssh_key|certificate|note")`. Thread `typ` into the write payload (the `{key,value,type}` change body the CLI posts). Empty → omit/`string`.
- `secrets list`: add a `TYPE` column sourced from the masked list's `type` field (the client type for the masked entry needs a `Type` field — add it). Keep the existing column layout; append TYPE.

- [ ] **Step 4: Run to verify pass**

Run: `go test ./cmd/janus/ -run 'SecretsSet|SecretsList|Type' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/janus/ 
git commit -m "feat(cli): secrets set --type and list TYPE column"
```

---

## Task 7: Web — types + `secretTypes` registry

**Files:**
- Modify: `web/src/lib/endpoints.ts` (`MaskedSecret`, reveal type, write-change type)
- Create: `web/src/secrets/secretTypes.ts` + `web/src/secrets/secretTypes.test.ts`

- [ ] **Step 1: Write the failing test** (`secretTypes.test.ts`)

```ts
import { describe, it, expect } from 'vitest'
import { SECRET_TYPES, SECRET_TYPE_ORDER, normalizeType } from './secretTypes'

describe('secretTypes', () => {
  it('normalizes unknown/empty to string', () => {
    expect(normalizeType(undefined)).toBe('string')
    expect(normalizeType('')).toBe('string')
    expect(normalizeType('bogus')).toBe('string')
    expect(normalizeType('json')).toBe('json')
  })
  it('multiline types are json/ssh_key/certificate/note', () => {
    expect(SECRET_TYPES.json.multiline).toBe(true)
    expect(SECRET_TYPES.ssh_key.multiline).toBe(true)
    expect(SECRET_TYPES.string.multiline).toBe(false)
    expect(SECRET_TYPES.password.multiline).toBe(false)
  })
  it('json validator rejects invalid json, accepts valid', () => {
    expect(SECRET_TYPES.json.validate!('{bad')).toBeTruthy()   // returns an error string
    expect(SECRET_TYPES.json.validate!('{"a":1}')).toBeNull()
  })
  it('order covers all types', () => {
    expect(SECRET_TYPE_ORDER).toEqual(['string','password','json','ssh_key','certificate','note'])
  })
})
```

- [ ] **Step 2: Run to verify fail**

Run: `cd web && npm test -- --run src/secrets/secretTypes.test.ts`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement `secretTypes.ts`**

```ts
import { Key, KeyRound, Braces, TerminalSquare, BadgeCheck, FileText } from 'lucide-react'
import type { ComponentType } from 'react'

export type SecretType = 'string' | 'password' | 'json' | 'ssh_key' | 'certificate' | 'note'

export interface SecretTypeSpec {
  label: string
  icon: ComponentType<{ size?: number; strokeWidth?: number; className?: string }>
  multiline: boolean
  monospace: boolean
  generate?: boolean
  /** Returns an error string when invalid, or null when ok. Warn-only. */
  validate?: (v: string) => string | null
}

export const SECRET_TYPES: Record<SecretType, SecretTypeSpec> = {
  string:      { label: 'Value',       icon: Key,            multiline: false, monospace: true },
  password:    { label: 'Password',    icon: KeyRound,       multiline: false, monospace: true, generate: true },
  json:        { label: 'JSON',        icon: Braces,         multiline: true,  monospace: true, validate: (v) => { try { JSON.parse(v); return null } catch { return 'Not valid JSON' } } },
  ssh_key:     { label: 'SSH key',     icon: TerminalSquare, multiline: true,  monospace: true },
  certificate: { label: 'Certificate', icon: BadgeCheck,     multiline: true,  monospace: true, validate: (v) => /-----BEGIN [^-]+-----[\s\S]*-----END [^-]+-----/.test(v) ? null : 'Missing PEM BEGIN/END block' },
  note:        { label: 'Note',        icon: FileText,       multiline: true,  monospace: true },
}

export const SECRET_TYPE_ORDER: SecretType[] = ['string', 'password', 'json', 'ssh_key', 'certificate', 'note']

export function normalizeType(t: string | undefined | null): SecretType {
  return t && (t in SECRET_TYPES) ? (t as SecretType) : 'string'
}
```
Confirm the lucide icon names exist (`TerminalSquare`, `BadgeCheck`, `Braces` — verify against the installed lucide version; if one is missing, substitute a present equivalent and note it).

- [ ] **Step 4: Extend endpoints types** (`web/src/lib/endpoints.ts`)

- `MaskedSecret`: add `type?: string` (`:30`).
- Wherever the reveal response type is declared, add `type?: string`.
- Wherever the batch-write change is posted (find the `setSecrets`/batch endpoint), add optional `type` to the change payload shape.

- [ ] **Step 5: Run to verify pass + typecheck**

Run: `cd web && npm test -- --run src/secrets/secretTypes.test.ts` → PASS; `npm run typecheck` → clean.

- [ ] **Step 6: Commit**

```bash
git add web/src/secrets/secretTypes.ts web/src/secrets/secretTypes.test.ts web/src/lib/endpoints.ts
git commit -m "feat(web): secret type registry + endpoint types"
```

---

## Task 8: Web — dirty buffer + rowState track type

**Files:**
- Modify: `web/src/secrets/dirty.ts`, `web/src/secrets/rowState.ts`
- Test: `web/src/secrets/dirty.test.ts`, `web/src/secrets/rowState.test.ts` (append)

- [ ] **Step 1: Write failing tests**

Read `dirty.ts` (the `Buffer` shape) and `rowState.ts` first. Add tests asserting: a buffer entry can carry a `type`, and a change of ONLY the type (value unchanged) yields `rowState().change === 'edited'` (a real dirty change). Mirror the existing `rowState.test.ts` cases for the buffer/original/masked fixtures, adding the `type` dimension. (The exact API depends on how `dirty.ts` models an entry — extend `{ value }` to `{ value, type? }` and pass an `originalType` map to `rowState`, or track type in the same shape as value. Match the existing structure.)

- [ ] **Step 2: Run to verify fail**

Run: `cd web && npm test -- --run src/secrets/dirty.test.ts src/secrets/rowState.test.ts`
Expected: FAIL.

- [ ] **Step 3: Implement**

- `dirty.ts`: extend the buffer entry to carry an optional `type` alongside `value`, and add a helper to set a key's type (mirroring how value edits are recorded). Keep delete/tombstone semantics unchanged.
- `rowState.ts`: extend `rowState(...)` so a type change (buffer type differs from the original/server type) counts as `change: 'edited'` for an existing key (same branch as a value edit), and does not spuriously mark unchanged rows. Add an `originalType`/server-type input parallel to `original` values.

- [ ] **Step 4: Run to verify pass**

Run: `cd web && npm test -- --run src/secrets/dirty.test.ts src/secrets/rowState.test.ts` → PASS; `npm run typecheck` → clean.

- [ ] **Step 5: Commit**

```bash
git add web/src/secrets/dirty.ts web/src/secrets/rowState.ts web/src/secrets/dirty.test.ts web/src/secrets/rowState.test.ts
git commit -m "feat(web): track secret type in the dirty buffer + row state"
```

---

## Task 9: Web — SecretTable input-vs-textarea + type dropdown

**Files:**
- Modify: `web/src/secrets/SecretTable.tsx`, `web/src/secrets/SecretEditor.tsx` (thread type + save)
- Test: `web/src/secrets/SecretTable.test.tsx` (append)

- [ ] **Step 1: Write failing tests**

Append to `SecretTable.test.tsx` (reuse its render harness): (a) a row whose type is `json` renders a `<textarea>` for the value (query by role `textbox` / a `data-testid` you add), while a `string`/`password` row renders a single-line `<input>`; (b) a per-row type control (a `<select aria-label="type for KEY">` or a menu) is present and changing it to `json` invokes the editor's type-change handler (assert via the buffer/dirty state or a spy). Read `SecretEditor.tsx` to see how value edits currently flow (`onChangeValue`/buffer) and mirror that for type.

- [ ] **Step 2: Run to verify fail**

Run: `cd web && npm test -- --run src/secrets/SecretTable.test.tsx`
Expected: FAIL.

- [ ] **Step 3: Implement**

- In `SecretEditor.tsx`: compute each row's current type (buffer type ?? masked `type` ?? `'string'`, via `normalizeType`); pass it + an `onChangeType(key, type)` handler down to `SecretTable`. `onChangeType` records the type in the dirty buffer (Task 8 helper). On save, include `type` in each change sent to the batch-write endpoint.
- In `SecretTable.tsx`: import `SECRET_TYPES`, `SECRET_TYPE_ORDER`, `normalizeType`. For the value cell (currently the `<input>` at `:154`), branch on `SECRET_TYPES[type].multiline`: render an auto-growing monospace `<textarea>` (rows grow with content, `font-mono`, token classes, same value/`onChange` wiring) when multiline, else the existing single-line `<input>`. Add a small per-row **type control**: a `<select aria-label={\`type for ${key}\`}>` listing `SECRET_TYPE_ORDER` (label from `SECRET_TYPES[t].label`), value = current type, `onChange` → `onChangeType(key, e.target.value)`. Show the type icon next to the key or in the control.
- Token classes only (no raw palette). Keep reveal/copy/remove wiring intact.

- [ ] **Step 4: Run to verify pass + guards**

Run: `cd web && npm test -- --run src/secrets/SecretTable.test.tsx src/secrets/SecretEditor.test.tsx` → PASS; `npm run typecheck` → clean; `npm test -- --run src/test/no-raw-palette.test.ts` → PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/secrets/SecretTable.tsx web/src/secrets/SecretEditor.tsx web/src/secrets/SecretTable.test.tsx
git commit -m "feat(web): per-type value editor (textarea for multiline) + type selector"
```

---

## Task 10: Web — password Generate + JSON validate/pretty affordances

**Files:**
- Create: `web/src/secrets/generatePassword.ts` + `web/src/secrets/generatePassword.test.ts`
- Modify: `web/src/secrets/SecretTable.tsx`
- Test: `web/src/secrets/SecretTable.test.tsx` (append)

- [ ] **Step 1: Write failing tests**

- `generatePassword.test.ts`: `generatePassword(24)` returns a 24-char string; two calls differ; uses `crypto.getRandomValues` (assert length + charset membership).
- `SecretTable.test.tsx`: for a `password` row, a **Generate** button is present and clicking it fills the value (buffer updates / input value changes). For a `json` row with invalid JSON typed in, a non-blocking warning (`/not valid json/i`) appears but the row is still saveable (no disabled Save).

- [ ] **Step 2: Run to verify fail**

Run: `cd web && npm test -- --run src/secrets/generatePassword.test.ts src/secrets/SecretTable.test.tsx`
Expected: FAIL.

- [ ] **Step 3: Implement**

`generatePassword.ts`:
```ts
const CHARS = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!@#$%^&*()-_=+'
export function generatePassword(length = 24): string {
  const out = new Uint32Array(length)
  crypto.getRandomValues(out)
  let s = ''
  for (let i = 0; i < length; i++) s += CHARS[out[i] % CHARS.length]
  return s
}
```
In `SecretTable.tsx`: for `password` rows (`SECRET_TYPES[type].generate`), render a Generate button that calls `onChangeValue(key, generatePassword())`. For rows whose `SECRET_TYPES[type].validate` exists, run it on the current value and render a non-blocking warn badge (token danger/amber class) when it returns a non-null string — do NOT disable save or block the buffer. Optionally a JSON pretty-print button that sets the value to `JSON.stringify(JSON.parse(v), null, 2)` guarded by a try/catch (no-op on invalid).

- [ ] **Step 4: Run to verify pass + guards**

Run: `cd web && npm test -- --run src/secrets/generatePassword.test.ts src/secrets/SecretTable.test.tsx` → PASS; `typecheck` clean; `no-raw-palette` pass.

- [ ] **Step 5: Commit**

```bash
git add web/src/secrets/generatePassword.ts web/src/secrets/generatePassword.test.ts web/src/secrets/SecretTable.tsx web/src/secrets/SecretTable.test.tsx
git commit -m "feat(web): password generator + non-blocking JSON/PEM validation"
```

---

## Task 11: Web — review-diff shows type change + masked row icon

**Files:**
- Modify: `web/src/secrets/ReviewDiffDialog.tsx`, `web/src/secrets/SecretTable.tsx` (masked row icon if not already added in Task 9)
- Test: `web/src/secrets/*` (append the relevant test)

- [ ] **Step 1: Write the failing test**

Add a test asserting the review-diff dialog, given a key whose type changed from `string` to `json` (value unchanged), lists that key with a `type: string → json` indication (value-free — no secret value shown). Read `ReviewDiffDialog.tsx` for how it currently lists added/edited/removed keys and extend that rendering.

- [ ] **Step 2: Run to verify fail**

Run: `cd web && npm test -- --run src/secrets/ReviewDiffDialog` (adjust to the actual test file) 
Expected: FAIL.

- [ ] **Step 3: Implement**

In `ReviewDiffDialog.tsx`: for an edited row where only/also the type changed, render a small `type: <old> → <new>` line (labels from `SECRET_TYPES`), keeping the dialog value-free. Ensure the row-level type icon appears in the masked table (if not already done in Task 9), sourced from `normalizeType(masked[key]?.type)`.

- [ ] **Step 4: Run to verify pass**

Run: `cd web && npm test -- --run src/secrets/` → PASS; `typecheck` clean; `no-raw-palette` pass.

- [ ] **Step 5: Commit**

```bash
git add web/src/secrets/ReviewDiffDialog.tsx web/src/secrets/SecretTable.tsx web/src/secrets/*.test.tsx
git commit -m "feat(web): show type change in review diff + type icon in masked rows"
```

---

## Task 12: Full gate + smoke + docs

**Files:**
- Modify: none (verification) + a short note in `CHANGELOG.md` if present

- [ ] **Step 1: Backend gate**

Run: `go build ./... && go test ./... -race`
Expected: all packages `ok`, no races. Then `gosec ./...` and `govulncheck ./...` — treat only NEW findings as real (pre-existing vendored-Shamir gosec findings and the local-toolchain `GO-2026-5856` govulncheck note are known non-issues on pinned go1.26.5).

- [ ] **Step 2: Web gate**

Run (from `web/`): `npm test -- --run && npm run typecheck && npm run smoke && npm test -- --run src/test/no-raw-palette.test.ts`
Expected: all pass; smoke reports light + dark OK.

- [ ] **Step 3: Migration up/down sanity**

Confirm migration 000022 applies and reverts against the dev DB (or note that the integration suite's testcontainer already runs it). If a `CHANGELOG.md` exists, add a one-line entry under Unreleased for typed secrets.

- [ ] **Step 4: Commit (if CHANGELOG touched)**

```bash
git add CHANGELOG.md
git commit -m "docs(changelog): typed secrets"
```

---

## Self-review checklist (author)

- **Spec coverage:** registry (A) → Task 7; backend type column + validation + carry (B) → Tasks 1–6; editor multiline/textarea + selector + affordances (C) → Tasks 9–11; promotion+clone carry-through → Task 5; CLI → Task 6; testing/gate → Task 12. All covered.
- **Type consistency:** `store.Change.Type` / `store.SecretValue.Type` (T1) → `secrets.SecretChange.Type` / `Secret.Type` / `normalizeType`/`validateType` (T2) → `MergedMeta.Type` (T3) → API `type` field (T4) → carry-through `Type: sec.Type` (T5) → web `SecretType`/`SECRET_TYPES`/`normalizeType` (T7) → dirty/rowState type (T8) → SecretTable rendering (T9). Names consistent across tasks.
- **Migration:** 000022 additive, `DEFAULT 'string'`, down drops the column. No new routes (OpenAPI drift stays green).
- **Value-free preserved:** type is metadata (never a secret); leak tests unaffected; review-diff stays value-free.
- **Open verification points flagged inline** (exact `scanSecretValue` positions, `ErrValidation` name, per-key PUT body struct, lucide icon names, `dirty.ts` entry shape, CLI test harness) — implementer confirms against the code, no guesses baked in.
