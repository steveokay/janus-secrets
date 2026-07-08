# On-Demand Editor Reveal — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development. Steps use checkbox (`- [ ]`) syntax.

**Goal:** The secret editor stops revealing all plaintext on mount. Values load on demand (per-key / bulk) into ephemeral state, never the Query cache; mount loads only masked metadata + the config version.

**Tech Stack:** React 18 + TS, Tailwind tokens, TanStack Query v5, vitest + @testing-library + msw. Frontend-only — no backend/Go changes. Run npm from `web/`.

**Spec:** `docs/superpowers/specs/2026-07-08-editor-ondemand-design.md`. Security: revealed/edited plaintext lives ONLY in `revealed`/`original` component state — never `useQuery`/`useMutation` cache, never logged/URL/toast/tooltip.

---

## Task 1: `revealKeyRaw` endpoint (+ drop orphaned `revealAll`)

**Files:** Modify `web/src/lib/endpoints.ts`; add/extend `web/src/lib/endpoints.test.ts` if one exists (else skip the test file and rely on Task 2's editor tests — check first with `ls web/src/lib/*.test.ts`).

- [ ] **Step 1:** Read `web/src/lib/endpoints.ts` around the reveal helpers (lines ~143-150): `revealKey` (resolved single), `revealAll` (resolved bulk), `rawConfig` (raw bulk). Grep the repo for `revealAll` usages: `cd web && grep -rn "revealAll" src`. If `SecretEditor.tsx` is the ONLY non-definition reference, it's safe to remove in Task 2 — for now just note it (Task 2 removes the last caller, so remove the definition here only if no other file references it; otherwise leave and Task 2 handles).
- [ ] **Step 2:** Add the raw single-key endpoint next to `revealKey`:
```ts
  // Raw (unresolved) single value for the editor — audited secret.reveal.
  revealKeyRaw: (cid: string, key: string) =>
    api.get<{ key: string; value: string }>(`/v1/configs/${cid}/secrets/${encodeURIComponent(key)}?raw=true`),
```
- [ ] **Step 3:** If a `web/src/lib/endpoints.test.ts` exists, add a case asserting `revealKeyRaw('c1','DB URL')` requests `/v1/configs/c1/secrets/DB%20URL?raw=true` and returns the `{key,value}` body (mirror the existing `revealKey` test). Run it. If no such test file exists, skip (Task 2 covers the endpoint via the editor).
- [ ] **Step 4:** `cd web && npm run typecheck` — clean.
- [ ] **Step 5: Commit** `git add web/src/lib/endpoints.ts <test if changed>` → `feat(web): revealKeyRaw endpoint for on-demand raw editor reveals`

---

## Task 2: Editor on-demand rework

**Files:** Modify `web/src/secrets/SecretEditor.tsx`; update `web/src/secrets/SecretEditor.test.tsx` and `web/src/secrets/SecretEditor.save.test.tsx`. May need to touch `web/src/lib/endpoints.ts` (remove orphaned `revealAll` if Task 1 left it). Do NOT edit `SecretTable.tsx`/`rowState.ts`/`DirtyBar.tsx`/`ReviewDiffDialog.tsx` (their props are unchanged — only the *values* passed in change); if a test for those relied on mount-time full `original`, fix the TEST, not the component.

### Context — current editor (`SecretEditor.tsx`)
- Mount fires TWO queries: `masked = useQuery(['config',cid,'masked'], maskedSecrets)` and `raw = useQuery(['config',cid,'raw'], rawConfig)`. **`raw` is the problem** — `rawConfig` hits `?reveal=true&raw=true`, returning all plaintext + version into the cache and auditing a reveal on mount.
- `const original = raw.data?.secrets ?? {}`, `const version = raw.data?.version ?? 0`.
- `revealed` state holds RESOLVED values (from `revealKey`); `revealAll` uses resolved bulk.
- `original` feeds `summarize`/`isDirty`/`toChanges`/`rowState`/`SecretTable` prefill + `ReviewDiffDialog`.
- `edit(key)` is sync (just sets `editing[key]=true`). Value prefill in `SecretTable.tsx:101` reads `original[key]`.

### Step 1 — Failing tests
Read `SecretEditor.test.tsx` + `SecretEditor.save.test.tsx` fully first. Note the `seed()`/`renderApp` helpers and how msw is set up. The existing tests almost certainly mock the `raw` bulk (`?reveal=true&raw=true`) returning values on mount and assert reveal/edit behavior against it — those setups must change (mount no longer calls raw). Update the shared seed to serve, on mount, ONLY: masked metadata (`GET /v1/configs/c1/secrets` no reveal param) and versions (`GET /v1/configs/c1/versions` → `{ versions: [{ version: 2, created_at:'', author:'' }] }`), plus on-demand handlers for per-key raw (`GET /v1/configs/c1/secrets/:key?raw=true` → `{key, value}`) and bulk raw (`GET /v1/configs/c1/secrets?reveal=true&raw=true` → `{version, secrets}`). Then add/adjust these behaviors (adapt names/fixtures to the file's helpers):

```tsx
test('mount reveals nothing — no reveal request, all values masked', async () => {
  seed()
  let revealHits = 0
  server.use(http.get('/v1/configs/c1/secrets/:key', () => { revealHits++; return HttpResponse.json({ key: 'x', value: 'y' }) }))
  server.use(http.get('/v1/configs/c1/secrets', ({ request }) => {
    const u = new URL(request.url)
    if (u.searchParams.get('reveal') === 'true') { revealHits++; return HttpResponse.json({ version: 2, secrets: {} }) }
    return HttpResponse.json({ secrets: { DB_URL: { value_version: 3, created_at: '', origin: 'own' } } })
  }))
  renderApp(<SecretEditor />, { route: '/projects/p1/configs/c1', withAuth: false })
  await screen.findByText('DB_URL')
  expect(screen.getByText('••••••••••••')).toBeInTheDocument()
  expect(revealHits).toBe(0) // nothing revealed on mount
})

test('clicking the eye fetches that one key raw and unmasks only it', async () => {
  seed()
  server.use(http.get('/v1/configs/c1/secrets/DB_URL', ({ request }) => {
    expect(new URL(request.url).searchParams.get('raw')).toBe('true')
    return HttpResponse.json({ key: 'DB_URL', value: 'postgres://a' })
  }))
  renderApp(<SecretEditor />, { route: '/projects/p1/configs/c1', withAuth: false })
  await screen.findByText('DB_URL')
  await userEvent.click(screen.getByRole('button', { name: /reveal DB_URL/i }))
  expect(await screen.findByText('postgres://a')).toBeInTheDocument()
})

test('editing a masked key fetches its raw original; same value is not dirty, real edit saves', async () => {
  seed()
  server.use(http.get('/v1/configs/c1/secrets/DB_URL', () => HttpResponse.json({ key: 'DB_URL', value: 'postgres://a' })))
  renderApp(<SecretEditor />, { route: '/projects/p1/configs/c1', withAuth: false })
  await screen.findByText('DB_URL')
  await userEvent.click(screen.getByRole('button', { name: /edit DB_URL/i }))
  const input = await screen.findByRole('textbox', { name: /value for DB_URL/i })
  expect(input).toHaveValue('postgres://a')   // prefilled from fetched raw original
  // typing the same value back is a no-op (not dirty) — no dirty bar
  expect(screen.queryByRole('button', { name: /save as v/i })).toBeNull()
})

test('reveal-all fetches once (bulk raw), Hide all and blur re-mask, a pending edit survives blur', async () => {
  seed()
  let bulk = 0
  server.use(http.get('/v1/configs/c1/secrets', ({ request }) => {
    const u = new URL(request.url)
    if (u.searchParams.get('reveal') === 'true') { bulk++; return HttpResponse.json({ version: 2, secrets: { DB_URL: 'postgres://a' } }) }
    return HttpResponse.json({ secrets: { DB_URL: { value_version: 3, created_at: '', origin: 'own' } } })
  }))
  server.use(http.get('/v1/configs/c1/secrets/DB_URL', () => HttpResponse.json({ key: 'DB_URL', value: 'postgres://a' })))
  renderApp(<SecretEditor />, { route: '/projects/p1/configs/c1', withAuth: false })
  await screen.findByText('DB_URL')
  // make a pending edit first
  await userEvent.click(screen.getByRole('button', { name: /edit DB_URL/i }))
  const input = await screen.findByRole('textbox', { name: /value for DB_URL/i })
  await userEvent.clear(input); await userEvent.type(input, 'postgres://B')
  await screen.findByRole('button', { name: /save as v3/i })  // dirty; version from listVersions (2)+1
  // reveal all (bulk, once)
  await userEvent.click(screen.getByRole('button', { name: /reveal all/i }))
  expect(bulk).toBe(1)
  window.dispatchEvent(new Event('blur'))
  // pending edit survives blur — still dirty
  await waitFor(() => expect(screen.getByRole('button', { name: /save as v3/i })).toBeInTheDocument())
})
```
Adapt config id / route / seed fixtures to whatever the existing passing tests use. Keep the existing save-batch test and the PR #44 ⌘S test working (they may need their mount handlers updated to include the `versions` endpoint and to drop the mount `raw` expectation).

- [ ] **Step 2 — Run → FAIL:** `cd web && npx vitest run src/secrets/SecretEditor.test.tsx src/secrets/SecretEditor.save.test.tsx`

### Step 3 — Implement `SecretEditor.tsx`
Replace the mount `raw` query and the derived `original`/`version` with on-demand state:

```tsx
const masked = useQuery({ queryKey: ['config', cid, 'masked'], queryFn: () => endpoints.maskedSecrets(cid) })
const versions = useQuery({ queryKey: ['config', cid, 'versions'], queryFn: () => endpoints.listVersions(cid) })
const [buffer, setBuffer] = useState<Buffer>(emptyBuffer())
const [editing, setEditing] = useState<Record<string, boolean>>({})
const [revealed, setRevealed] = useState<Record<string, string>>({}) // viewing (re-maskable), RAW
const [original, setOriginal] = useState<Record<string, string>>({}) // edit-originals (persist while dirty), RAW
// ...filter/showHistory/importOpen/reviewOpen unchanged...

// Config version from the value-free versions list (no mount reveal). Robust to
// ordering: take the max present, 0 for a config with no versions yet.
const version = Math.max(0, ...(versions.data ?? []).map((v) => v.version))
const summary = useMemo(() => summarize(buffer, original), [buffer, original])
const dirty = isDirty(buffer, original)
```

Update the pieces:
```tsx
const save = useMutation({
  mutationFn: () => endpoints.saveSecrets(cid, toChanges(buffer, original), ''),
  onSuccess: (res) => {
    setBuffer(emptyBuffer()); setEditing({}); setRevealed({}); setOriginal({})
    setReviewOpen(false)
    void qc.invalidateQueries({ queryKey: ['config', cid] })
    toast({ title: `Saved as v${res.version}` })
  },
  onError: (e) => toast({ title: errorMessage(e, 'Save failed.'), tone: 'danger' }),
})

function discard() { setBuffer(emptyBuffer()); setEditing({}); setOriginal({}); setReviewOpen(false) }

// Viewing reveal — RAW, imperative, into ephemeral `revealed` only.
async function reveal(key: string): Promise<string> {
  if (key in revealed) return revealed[key]
  const r = await endpoints.revealKeyRaw(cid, key)
  setRevealed((m) => ({ ...m, [key]: r.value }))
  return r.value
}
const anyRevealed = Object.keys(revealed).length > 0
async function revealAll() {
  const r = await endpoints.rawConfig(cid)   // one audited bulk RAW reveal
  setRevealed(r.secrets)
}
function hideAll() { setRevealed({}) }

async function copy(key: string) {
  const value = await reveal(key)
  try { await navigator.clipboard?.writeText(value); toast({ title: `Copied ${key}` }) } catch { /* no-op */ }
}

// Editing a masked existing key needs its raw original (for prefill + diff) —
// fetch on demand (audited), reusing an already-revealed value. Added keys have
// no server value.
async function edit(key: string) {
  if (!(key in original)) {
    const existing = key in (masked.data ?? {})
    if (existing) {
      const v = key in revealed ? revealed[key] : (await endpoints.revealKeyRaw(cid, key)).value
      setOriginal((o) => ({ ...o, [key]: v }))
    }
  }
  setEditing((s) => ({ ...s, [key]: true }))
}
function changeValue(key: string, value: string) { setBuffer((b) => setValue(b, key, value)) }
function remove(key: string) { setBuffer((b) => removeKey(b, key)) }
function undo(key: string) {
  setBuffer((b) => revert(b, key))
  setEditing((s) => { const { [key]: _d, ...rest } = s; return rest })
  setOriginal((o) => { const { [key]: _d, ...rest } = o; return rest })
}
```
Keep the auto-re-mask effect EXACTLY as is (it already clears only `revealed`) and the ⌘S effect + beforeunload guard unchanged. Loading guard: change to `if (masked.isLoading || versions.isLoading)`. Keep `if (masked.isError) return …`. In the JSX, wire `onEdit={(key) => void edit(key)}` (edit is now async) and pass `original={original}` to `SecretTable` and `ReviewDiffDialog` as before. Remove the `raw` references. If `endpoints.revealAll` is now unused repo-wide, delete its definition from `endpoints.ts` in this task.

- [ ] **Step 4 — Run → GREEN**, then fix any downstream test fallout (e.g. a `SecretTable`/`rowState` test that seeded a full mount `original`): `cd web && npx vitest run src/secrets && npx vitest run`
- [ ] **Step 5 — Full gates:** `npm run typecheck && npx vitest run src/test/no-raw-palette.test.ts && npx vitest run && npm run build && npm run smoke`
- [ ] **Step 6 — Commit:** `git add web/src/secrets/SecretEditor.tsx web/src/secrets/SecretEditor.test.tsx web/src/secrets/SecretEditor.save.test.tsx web/src/lib/endpoints.ts` → `feat(web): on-demand editor reveal — mount loads masked metadata only, values fetched per-key/bulk into ephemeral state`

---

## Final verification
- [ ] `cd web && npm run typecheck && npx vitest run && npm run build && npm run smoke` — all green, dual-theme.
- [ ] Rebuild dev container: `docker compose up -d --build janus && ./scripts/dev-unseal.sh`.
