# B2 — Config Version History Drawer (design)

- **Status:** APPROVED 2026-07-06 (functional decisions banked during Slice-1
  planning session; Steve: "continue to b2 as recommended").
- **Visual authority:** `2026-07-06-ui-visual-design.md` + mockup; compose from
  tokens + existing kit, quieter than the editor.
- **Tracker:** `fe-improvements.md` §3 "Version history drawer" (P2) + §4 kit
  items (Modal/Dialog, Toast) — B2 pulls those kit pieces forward per the
  shadcn-lean decision.

## API contract (verified against `internal/api/versions_handlers.go` — mocks MUST mirror these exactly)

- `GET /v1/configs/{cid}/versions` (authz `config:read`, NOT audited) →
  `{"versions":[{"version":n,"message":"...","created_by":"...","created_at":"RFC3339"}]}`
- `GET /v1/configs/{cid}/versions/diff?a=<n>&b=<n>` (`config:read`, NOT audited;
  400 unless a,b ≥ 1) → `{"a":n,"b":n,"added":[keys],"changed":[keys],"removed":[keys]}`
  — **key names only, never values.**
- `POST /v1/configs/{cid}/rollback` (authz `secret:write`, audited
  `config.rollback`) body `{"target_version":n,"message":"..."}` →
  `{"version":m,"id":"...","created_at":"RFC3339"}` (existing `VersionResult`).

New deps (sanctioned by the recorded shadcn-lean decision):
`@radix-ui/react-dialog`, `@radix-ui/react-alert-dialog`, `@radix-ui/react-toast`.
Dev-only: `puppeteer-core` (real-browser smoke — new slice-end rule).

## Units

1. **`web/src/ui/Toast.tsx`** — Radix Toast: `ToastProvider` (mounted once in
   `App.tsx` inside QueryClientProvider), `useToast()` returning
   `push({ title, tone? })` (`tone: 'success' | 'danger'`, default success).
   Visual: bottom-right, `bg-ink text-card rounded-card shadow-pop px-4 py-2.5
   text-[12.5px]`, leading ✓ in `text-success` (or ✕ `text-danger`),
   auto-dismiss 4s, `swipeDirection="right"`. Never render secret values in
   toasts.
2. **`web/src/ui/ConfirmDialog.tsx`** — Radix AlertDialog:
   `ConfirmDialog({ open, onOpenChange, title, body, confirmLabel, tone?, onConfirm })`;
   card styling like CreateForms' dialog (`bg-ink/30` overlay, `rounded-card
   border-line bg-card p-5 shadow-pop`); confirm button `bg-brand` (or
   `bg-danger` when `tone="danger"`), cancel secondary.
3. **`web/src/ui/Sheet.tsx`** — Radix Dialog styled as right slide-over:
   `Sheet({ open, onOpenChange, title, children })` — overlay `bg-ink/30`,
   panel `fixed inset-y-0 right-0 w-[380px] max-w-full border-l border-line
   bg-card shadow-pop p-5 overflow-y-auto`, header row with title
   (`text-[15px] font-semibold`) and an X close icon-button (lucide `X`,
   aria-label "close"). No animation requirements this slice (motion is P2).
4. **`web/src/lib/endpoints.ts` additions** — types `VersionMeta`
   `{version:number; message:string; created_by:string; created_at:string}` and
   `VersionDiff` `{a:number; b:number; added:string[]; changed:string[]; removed:string[]}`;
   endpoints `listVersions(cid)` (unwraps `.versions`),
   `diffVersions(cid, a, b)`, `rollback(cid, target_version, message)` →
   existing `VersionResult`.
5. **`web/src/secrets/VersionHistory.tsx`** — content rendered inside a Sheet:
   - Query `['config', cid, 'versions']` → list newest-first. Row: `Pill
     tone="brand">v{n}</Pill>` (latest row gets `tone="success"` + "current"),
     message (`text-[13px]`; empty → `text-faint` "no message"), sub-line
     `created_by · timeAgo(created_at)`.
   - Clicking a row toggles its diff: v1 renders "Initial version" (`text-faint`,
     no diff request); v>1 fetches `['config', cid, 'diff', n-1, n]` and renders
     three labeled groups (Added/Changed/Removed) of key-name chips
     (`font-mono text-[11.5px]` Pills: added=success, changed=warning,
     removed=danger); empty diff → "No key changes" faint. Diff errors render
     inline `text-danger` "Couldn't load diff."
   - Roll back button per non-latest row (`secondary sm`); `disabled` +
     `title="Save or discard your changes first"` when the editor is dirty
     (passed as prop). Click → ConfirmDialog: title "Roll back to v{n}?", body
     "This creates a new version that restores v{n}'s keys — nothing is
     deleted.", confirm "Roll back". On success: toast
     `Rolled back to v{n} — saved as v{m}`, invalidate `['config', cid]`
     (refreshes editor masked/raw + versions), keep sheet open.
     Mutation error → toast tone danger "Rollback failed."
6. **`web/src/secrets/SecretEditor.tsx` integration** — a "History" button
   (lucide `History` icon + label, secondary style) in the header row next to
   the save button; state `showHistory`; renders
   `<Sheet open=... title="Version history"><VersionHistory cid={cid} dirty={dirty} /></Sheet>`.
   No other editor changes.
7. **`web/scripts/smoke.mjs`** (+ `npm run smoke`) — puppeteer-core drive of the
   BUILT bundle served by the dev container (or `vite preview`): intercepts
   `/v1/*` with REAL-shape fixtures (me `{kind,id,name}`, seal-status, projects,
   versions), asserts the authenticated shell renders non-empty and no
   pageerrors. Codifies the real-browser smoke rule from the Slice-2 incident.

## Security

- History/diff surfaces render key NAMES only — no values exist in these API
  responses at all. No new audit events from reads (server-defined); rollback
  is audited server-side. Toasts/messages never include secret values.
- Rollback disabled while dirty so the editor's unsaved buffer can't silently
  diverge from a rolled-back baseline.

## Error handling

List/diff errors inline in the sheet (`text-danger` lines); rollback errors via
danger toast; no blank states — every branch has loading (`text-faint`
"Loading…"), error, and empty treatments.

## Testing

- Kit: Toast (push renders + auto-dismiss not asserted, presence + tone),
  ConfirmDialog (confirm/cancel callbacks), Sheet (open/close, title, aria).
- VersionHistory (msw, REAL shapes): list renders newest-first with
  current-pill; v1 shows "Initial version"; diff groups render key chips; diff
  error branch; rollback flow = click → dialog → confirm → POST body
  `{target_version, message}` asserted → success toast text → versions+config
  queries invalidated (assert refetch); dirty=true disables the button.
- SecretEditor: History button opens the sheet (one appended test).
- Real-browser smoke script runs clean.

## Out of scope

Value-level diffs (server never exposes them) · restoring the sheet across
navigation · versions pagination (list is small, single-tenant) · audit viewer
(B3) · motion/animation polish (P2).
