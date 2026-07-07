# §3 Secret Editor Redesign — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Rebuild the secret editor to match mockup §06 — sticky table with origin pills, reveal/copy on hover, colored rails + change pills for pending edits, a bottom dirty-bar (Review diff / Discard / Save as vN), a key filter, an Import .env modal, and inherited→override editing.

**Architecture:** Extract the editor into focused components (`SecretTable`, `EditorToolbar`, `DirtyBar`, `ReviewDiffDialog`, `ImportEnvDialog`) plus a pure `rowState.ts`; `SecretEditor.tsx` keeps the queries/buffer/mutations (`dirty.ts` unchanged) and composes them. Frontend-only. Token classes only.

**Tech Stack:** React 18 + TS + Tailwind (dark tokens) + TanStack Query v5 + Vitest/MSW.

**Spec:** `docs/superpowers/specs/2026-07-07-editor-redesign-design.md` (read it). **Mockup:** `docs/design/ui-redesign-mockup.html` §06 (canonical look).

**Constraints:** token classes only (`no-raw-palette` + `dark-aa` gates; `text-brand-deep` banned → `text-brand-text`). Both themes. Security invariants (spec §Security): no reveal on mount; revealed plaintext + import values in ephemeral state only; Review-diff shows no values; Save = one `PUT …/secrets`.

**Preserve** these `SecretEditor.test.tsx` behaviors (stable aria-labels/text): `reveal {key}` button, `new key`/`new value` inputs, `/history/i` button, origin text `own`/`inherited`/`overridden`, `No secrets yet`, no-audited-reveal-on-mount.

**Branch:** `milestone-23-editor-redesign` (created).

---

### Task 1: Pure row-state + `.env` parser

**Files:** Create `web/src/secrets/rowState.ts`, `web/src/secrets/rowState.test.ts`.

- [ ] **Step 1 (TDD): `web/src/secrets/rowState.test.ts`:**
```ts
import { rowState, parseDotenv } from './rowState'
import type { MaskedSecret } from '../lib/endpoints'

const masked: Record<string, MaskedSecret> = {
  OWN: { value_version: 3, created_at: '', origin: 'own' },
  INH: { value_version: 1, created_at: '', origin: 'inherited' },
}
const original = { OWN: 'a' }

test('no buffer entry → no change, server origin', () => {
  expect(rowState('OWN', masked, {}, original)).toEqual({ change: null, origin: 'own', existing: true })
  expect(rowState('INH', masked, {}, original)).toEqual({ change: null, origin: 'inherited', existing: true })
})
test('editing an own key to a new value → edited', () => {
  expect(rowState('OWN', masked, { OWN: { value: 'b' } }, original)).toMatchObject({ change: 'edited', origin: 'own' })
})
test('buffer value equal to original → not a change', () => {
  expect(rowState('OWN', masked, { OWN: { value: 'a' } }, original).change).toBeNull()
})
test('editing an inherited key → edited + overridden', () => {
  expect(rowState('INH', masked, { INH: { value: 'x' } }, original)).toMatchObject({ change: 'edited', origin: 'overridden' })
})
test('removing an existing key → removed', () => {
  expect(rowState('OWN', masked, { OWN: { value: null } }, original)).toMatchObject({ change: 'removed' })
})
test('a brand-new key → added', () => {
  expect(rowState('NEW', masked, { NEW: { value: 'v' } }, original)).toMatchObject({ change: 'added', existing: false })
})

test('parseDotenv: KEY=VALUE, comments, blanks, quotes, invalid', () => {
  const r = parseDotenv(['# comment', '', 'A=1', 'B="two words"', "C='q'", 'bad key=x', 'D=', 'nokeyval'].join('\n'))
  expect(r.pairs).toEqual({ A: '1', B: 'two words', C: 'q', D: '' })
  expect(r.skipped).toBe(3) // comment/blank are ignored (not skipped); 'bad key', 'nokeyval' + note below
})
```
Note: define "skipped" = lines that look like content but are invalid (a bad key, or a line with no `=`). Blank lines and `#` comments are ignored and NOT counted. Adjust the expected `skipped` count in the test to match that rule precisely (here: `bad key=x` and `nokeyval` → 2; if you also count something else, align the assertion to the implementation you write — keep the rule: ignore blank/`#`, count malformed content lines).

- [ ] **Step 2: implement `web/src/secrets/rowState.ts`:**
```ts
import type { MaskedSecret } from '../lib/endpoints'
import type { Buffer } from './dirty'

export type Change = 'added' | 'edited' | 'removed' | null
export interface RowState { change: Change; origin: MaskedSecret['origin']; existing: boolean }

export function rowState(
  key: string,
  masked: Record<string, MaskedSecret>,
  buffer: Buffer,
  original: Record<string, string>,
): RowState {
  const existing = key in masked
  const serverOrigin = masked[key]?.origin ?? 'own'
  const entry = buffer[key]
  if (!entry) return { change: null, origin: serverOrigin, existing }
  if (entry.value === null) {
    return { change: existing ? 'removed' : null, origin: serverOrigin, existing }
  }
  const had = key in original
  const changed = !had || original[key] !== entry.value
  if (!changed) return { change: null, origin: serverOrigin, existing }
  if (existing) {
    const origin = serverOrigin === 'inherited' ? 'overridden' : serverOrigin
    return { change: 'edited', origin, existing }
  }
  return { change: 'added', origin: 'own', existing }
}

const KEY_RE = /^[A-Za-z_][A-Za-z0-9_]*$/
function unquote(v: string): string {
  const t = v.trim()
  if (t.length >= 2 && ((t[0] === '"' && t.at(-1) === '"') || (t[0] === "'" && t.at(-1) === "'"))) {
    return t.slice(1, -1)
  }
  return t
}

export function parseDotenv(text: string): { pairs: Record<string, string>; skipped: number } {
  const pairs: Record<string, string> = {}
  let skipped = 0
  for (const raw of text.split(/\r?\n/)) {
    const line = raw.trim()
    if (line === '' || line.startsWith('#')) continue
    const eq = line.indexOf('=')
    if (eq <= 0) { skipped++; continue }
    const key = line.slice(0, eq).trim()
    if (!KEY_RE.test(key)) { skipped++; continue }
    pairs[key] = unquote(line.slice(eq + 1))
  }
  return { pairs, skipped }
}
```

- [ ] **Step 3:** Run `npx vitest run src/secrets/rowState.test.ts` (green — align the `skipped` count assertion to the rule), `npx vitest run`, `npm run typecheck`. Commit: `feat(web): editor rowState + dotenv parser`.

---

### Task 2: Table redesign + editor shell

**Files:** Create `web/src/secrets/SecretTable.tsx`; rewrite `web/src/secrets/SecretEditor.tsx` to compose it; reconcile `web/src/secrets/SecretEditor.test.tsx`.

This is the structural core. `SecretEditor` keeps: `masked`/`raw` queries, `buffer`/`setBuffer`, `editing`/`setEditing`, `revealed`/`setRevealed`, `reveal(key)`, `save` mutation, beforeunload guard, `AddKeyRow`, the History `Sheet`+`VersionHistory`. It now also holds `filter` state (Task 3), `importOpen`/`reviewOpen` state (Tasks 4/5) — add those `useState`s now with the dialogs rendered as `{importOpen && …}` placeholders so later tasks slot in without restructuring. Compose: `<EditorToolbar/>` (Task 3 — for now a minimal header with the History button so its test passes), `<SecretTable/>`, `<DirtyBar/>` (Task 4 — for now the existing save/summary inline). 

- [ ] **Step 1: implement `web/src/secrets/SecretTable.tsx`.** Grid table matching mockup §06 (read it). Props:
```tsx
export function SecretTable({
  rows, masked, buffer, original, editing, revealed, filter,
  onReveal, onCopy, onEdit, onChangeValue, onRemove, onRevert,
}: {
  rows: string[]                              // ordered key list (existing masked keys + added buffer keys)
  masked: Record<string, MaskedSecret>
  buffer: Buffer
  original: Record<string, string>
  editing: Record<string, boolean>
  revealed: Record<string, string>
  filter: string
  onReveal: (key: string) => void
  onCopy: (key: string) => void
  onEdit: (key: string) => void
  onChangeValue: (key: string, value: string) => void
  onRemove: (key: string) => void             // delete existing / discard-or-revert buffer entry (caller decides via rowState)
  onRevert: (key: string) => void             // drop a buffer entry (discard add / revert edit / restore remove)
}) { /* … */ }
```
Requirements (per mockup + spec §Row semantics):
  - Container `rounded-card border border-line bg-card overflow-hidden`. A sticky header row (grid `1.3fr 1.5fr 108px 56px 92px`, `bg-page` / dark `#101017` — but hex is banned in components, so use a token: header `bg-page` is fine; for the dark header tint use `bg-elevated` or a token, NOT a hex literal) with `text-faint` uppercase micro-labels `Key/Value/Origin/Ver/Actions`.
  - Each visible key (filtered by `filter`, case-insensitive substring on the key) is a row: relative container with an absolute left `w-[3px]` rail colored by `rowState().change` (added=`bg-success`, edited=`bg-warning`, removed=`bg-danger`; none when `null`); a `group` for hover.
  - **Key cell:** mono `text-ink` + an inline change chip when `change` set (`text-[10px] font-bold uppercase` add=`text-success`, edit=`text-warning`, remove=`text-danger`). Removed rows: `line-through opacity-45` on key+value.
  - **Value cell:** when `editing[key]` → an `<input aria-label={`value for ${key}`}>` bound to the buffer value (`onChangeValue`). Else masked dots `••••••••••••` (or the revealed value if `key in revealed`), plus a hover group (`opacity-0 group-hover:opacity-100`) with a **reveal** icon-button `aria-label={`reveal ${key}`}` (hidden once revealed) and a **copy** icon-button `aria-label={`copy ${key}`}` (`onCopy`). Use lucide `Eye`/`Copy`.
  - **Origin cell:** `<Pill tone={originTone[rowState.origin]}>{rowState.origin}</Pill>` — own=`success`, inherited=`muted`, overridden=`brand`. (Pill renders the literal origin text so existing tests match.)
  - **Ver cell:** existing → `v{masked[key].value_version}` (`text-faint tabular-nums`); added → `—`.
  - **Actions cell** (`flex justify-end gap-1`): derive from `rowState`/editing — editing→(nothing, the input is inline); `change==='added'`→ discard `aria-label={`discard ${key}`}` (`onRevert`); `change==='edited'`→ revert `aria-label={`revert ${key}`}` (`onRevert`) + remove; `change==='removed'`→ restore `aria-label={`restore ${key}`}` (`onRevert`); otherwise (own/overridden, not inherited) → edit `aria-label={`edit ${key}`}` (`onEdit`) + remove `aria-label={`remove ${key}`}` (`onRemove`); inherited (no change) → edit only (`onEdit`, creates an override). lucide `Pencil`/`X`/`RotateCcw`/`Undo2`.
  - Token classes only; no `text-brand-deep`; renders both themes.

- [ ] **Step 2: rewrite `SecretEditor.tsx`** to compute the ordered `rows` (existing masked keys, then added buffer keys `Object.keys(buffer).filter(k => !(k in masked) && buffer[k].value !== null)`), hold all state, and render: a header with the History button (`/history/i`), `<SecretTable …/>` passing `reveal`/`copy`/`edit`/`changeValue`/`remove`/`revert` handlers, the existing `AddKeyRow` (unchanged labels), a temporary inline Save button (`Save as v{version+1}`, disabled unless dirty) — Task 4 replaces this with `<DirtyBar/>`, the `Sheet`+`VersionHistory`. Implement:
  - `copy(key)`: if `!(key in revealed)`, `await reveal(key)` first (audited), then `navigator.clipboard?.writeText(revealedValue)` guarded; success → a toast if a `ToastProvider` is present (use `useToast` — no-op default when absent, as in KeyActions).
  - `edit(key)`: set `editing[key]=true` and seed the buffer with the current effective value so the input shows it (for inherited, seed from the revealed/masked — since inherited values aren't in `original`, seed empty or the revealed value if available; keep simple: seed `original[key] ?? ''`).
  - `remove(key)`: `setBuffer(removeKey(b, key))`. `revert(key)`: `setBuffer(revert(b, key))` and clear `editing[key]`.
  - Keep the `beforeunload` dirty guard, the `save` mutation (clears buffer+editing, invalidates), the masked/raw queries.

- [ ] **Step 3: reconcile `SecretEditor.test.tsx`.** Run it; the existing 4 tests should largely pass (stable labels). Fix ONLY assertions the redesign legitimately changed (e.g. if the masked value placeholder text changed from `•••••••` to `••••••••••••`, or a pending-summary string moved into the dirty-bar — but the dirty-bar arrives in Task 4, so keep a temporary inline summary/save here). Do NOT weaken security assertions (no-reveal-on-load, masked-by-default). Add a test: an added buffer key renders a row with a `discard {key}` action and an `added` chip.

- [ ] **Step 4:** Run `npx vitest run src/secrets/` (green), `npx vitest run`, `npm run typecheck`, `npm run build`. Commit: `feat(web): redesigned secret table (pills, rails, reveal/copy, per-row actions)`.

---

### Task 3: Editor toolbar (filter + import trigger + history)

**Files:** Create `web/src/secrets/EditorToolbar.tsx`; wire into `SecretEditor.tsx`; test `web/src/secrets/EditorToolbar.test.tsx` (or extend SecretEditor.test).

- [ ] **Step 1 (TDD):** a test that typing in the filter narrows the visible rows, and that the toolbar exposes `Import .env` and `History` buttons:
```tsx
test('filter narrows rows by key', async () => {
  seed() // DB_URL (own) + SENTRY_DSN (inherited)  [reuse SecretEditor.test seed shape]
  renderApp(<ToastProvider><SecretEditor /></ToastProvider>, { route: '/projects/p1/configs/c1', withAuth: false })
  await screen.findByText('DB_URL')
  await userEvent.type(screen.getByRole('searchbox', { name: /filter keys/i }), 'sentry')
  expect(screen.queryByText('DB_URL')).toBeNull()
  expect(screen.getByText('SENTRY_DSN')).toBeInTheDocument()
})
test('toolbar exposes Import .env and History', async () => {
  seed()
  renderApp(<ToastProvider><SecretEditor /></ToastProvider>, { route: '/projects/p1/configs/c1', withAuth: false })
  expect(await screen.findByRole('button', { name: /import \.env/i })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /history/i })).toBeInTheDocument()
})
```

- [ ] **Step 2: implement `EditorToolbar.tsx`** matching mockup §06 toolbar: a `search`-styled key filter `<input type="search" role="searchbox" aria-label="filter keys">` (lucide `Search` icon, `bg-card border-line rounded`), an **Import .env** secondary button (lucide `Upload`) → `onImport()`, a **History** secondary button (lucide `Clock`/`History`) → `onHistory()`. Props `{ filter, onFilter, onImport, onHistory }`. Wire into `SecretEditor` (lift `filter` state; pass to `SecretTable`; `onImport` sets `importOpen`; `onHistory` opens the sheet). Move the History button out of the temp header into the toolbar.

- [ ] **Step 3:** Run the toolbar tests + full suite + typecheck. Commit: `feat(web): editor toolbar — key filter, import, history`.

---

### Task 4: Dirty-bar + Review-diff modal

**Files:** Create `web/src/secrets/DirtyBar.tsx`, `web/src/secrets/ReviewDiffDialog.tsx`; wire into `SecretEditor.tsx`; test `web/src/secrets/DirtyBar.test.tsx`.

- [ ] **Step 1 (TDD):**
```tsx
test('dirty-bar summarizes pending edits and saves', async () => {
  seed()
  server.use(http.put('/v1/configs/c1/secrets', () => HttpResponse.json({ version: 4, id: 'v4', created_at: '' })))
  renderApp(<ToastProvider><SecretEditor /></ToastProvider>, { route: '/projects/p1/configs/c1', withAuth: false })
  await screen.findByText('DB_URL')
  await userEvent.click(screen.getByRole('button', { name: /remove db_url/i }))
  expect(screen.getByText(/1 removed/i)).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: /save as v4/i }))
  // buffer clears on success → dirty-bar gone
  await waitFor(() => expect(screen.queryByText(/1 removed/i)).toBeNull())
})
test('review diff lists changed key names, no values', async () => {
  seed()
  server.use(http.get('/v1/configs/c1/secrets/DB_URL', () => HttpResponse.json({ key: 'DB_URL', value: 'postgres://a' })))
  renderApp(<ToastProvider><SecretEditor /></ToastProvider>, { route: '/projects/p1/configs/c1', withAuth: false })
  await screen.findByText('DB_URL')
  await userEvent.click(screen.getByRole('button', { name: /remove db_url/i }))
  await userEvent.click(screen.getByRole('button', { name: /review diff/i }))
  expect(await screen.findByText(/DB_URL/)).toBeInTheDocument()
  expect(screen.queryByText('postgres://a')).toBeNull() // no values in the diff
})
```

- [ ] **Step 2: implement `DirtyBar.tsx`** — only rendered when `dirty`. Matches mockup: `bg-card border border-brand-line rounded-card`, a warning icon (`text-brand-text`), summary `+{added} added · {changed} changed · {removed} removed` (from `summarize`), and right-aligned actions **Review diff** (ghost → `onReview`), **Discard** (secondary → `onDiscard`, clears buffer+editing), **Save as v{version+1}** (primary → `onSave`, disabled while pending). Props `{ summary, version, dirty, saving, onReview, onDiscard, onSave }`.
- [ ] **Step 3: implement `ReviewDiffDialog.tsx`** — a centered `Dialog` (reuse the `CreateForms` Dialog pattern) titled "Review changes". Body: three grouped lists (Added / Changed / Removed) of **key names only**, computed from the buffer via `rowState`/`summarize` (NO values). A footer `Save as v{n}` (→ `onSave`) + Cancel. Props `{ open, onClose, buffer, masked, original, version, onSave, saving }`.
- [ ] **Step 4:** wire into `SecretEditor` (replace the temp inline save with `<DirtyBar/>`; `onReview` opens `<ReviewDiffDialog/>`; both call the same `save.mutate()`). Run tests + full suite + typecheck. Commit: `feat(web): editor dirty-bar + review-diff modal`.

---

### Task 5: Import .env modal

**Files:** Create `web/src/secrets/ImportEnvDialog.tsx`; wire into `SecretEditor.tsx`; test `web/src/secrets/ImportEnvDialog.test.tsx`.

- [ ] **Step 1 (TDD):**
```tsx
test('import .env applies pairs to the buffer', async () => {
  seed()
  renderApp(<ToastProvider><SecretEditor /></ToastProvider>, { route: '/projects/p1/configs/c1', withAuth: false })
  await screen.findByText('DB_URL')
  await userEvent.click(screen.getByRole('button', { name: /import \.env/i }))
  await userEvent.type(screen.getByLabelText(/paste .*env/i), 'NEW_KEY=abc\n# c\nBAD KEY=x')
  await userEvent.click(screen.getByRole('button', { name: /^import/i }))
  // new key now appears as an added row; skipped count surfaced
  expect(await screen.findByText('NEW_KEY')).toBeInTheDocument()
})
```

- [ ] **Step 2: implement `ImportEnvDialog.tsx`** — centered `Dialog` "Import .env". A `<textarea aria-label="paste .env contents">`, a live or on-submit summary using `parseDotenv` ("N keys · M skipped"), and an **Import** button that calls `onApply(pairs)` then closes. Props `{ open, onClose, onApply }`. In `SecretEditor`, `onApply(pairs)` = `setBuffer(b => Object.entries(pairs).reduce((acc,[k,v]) => setValue(acc,k,v), b))` — existing keys become edits, new keys adds; toast "N imported" if a provider is present. Nothing is saved until Save.
- [ ] **Step 3:** Run tests + full suite + typecheck + build. Commit: `feat(web): editor import .env modal`.

---

### Task 6: Gates, final review, tracker, merge

- [ ] **Step 1:** Full gates (web/): `npm run typecheck && npx vitest run && npm run build`. Rebuild container `docker compose up -d --build janus && ./scripts/dev-unseal.sh`; `npm run smoke` (both themes). `go build ./...`.
- [ ] **Step 2:** Final whole-branch review subagent — verify: no reveal on mount; revealed plaintext + import values never enter the query cache or logs; Review-diff renders no values; rowState override/edit/remove classification correct; token-only styling (gates); both themes; existing security tests intact; a11y on the new icon-buttons/dialogs.
- [ ] **Step 3:** In `fe-improvements.md` §3, check off the delivered items (table layout, origin pills, per-row actions, dirty-state bar, diff preview, search/`.env` paste, add-secret UX) and note the P2 deferrals (reveal-all/auto-remask/keyboard). Commit `docs(fe): check off §3 editor redesign`.
- [ ] **Step 4 (controller):** PR → merge per standing orders → rebuild container → update memory.

**Exit criteria:** the editor matches mockup §06 — sticky pill/rail table, reveal+copy on hover, per-row actions incl. inherited→override, bottom dirty-bar with Review diff/Discard/Save, key filter, Import .env; no secret value leaks (mount/cache/logs/diff); both themes pass smoke; final review clean.
