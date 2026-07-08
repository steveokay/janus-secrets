# §3 P2 — Secret Editor Polish — Design

FE punch-list §3 P2 follow-ups on the shipped secret editor. Frontend-only.
Three pieces: reveal-all + auto-re-mask, `⌘S`-to-save, and dialog a11y.

## 1. Reveal-all toggle + auto-re-mask (security-sensitive)

Today reveal is per-key (`endpoints.revealKey`, one audited `secret.reveal` each),
stored in ephemeral `revealed` state (never cached). Add a bulk toggle:

- **Reveal all** (toolbar toggle in `EditorToolbar`): calls the BULK reveal endpoint
  `endpoints.revealAll(cid)` (`GET …/secrets?reveal=true`) — **one** audited
  `secret.reveal` for the whole config (matches `janus run`), returns resolved values
  for every key. Populate the ephemeral `revealed` map with the result. The toggle
  flips to **Hide all** while any values are revealed; Hide all clears `revealed`.
- **Auto-re-mask (security):** revealed plaintext must not linger. Re-mask (clear
  `revealed`) on:
  - **window blur** — user tabs/switches away.
  - **idle timeout** — no user interaction (key/mouse) for **60s**. A timer resets on
    activity while anything is revealed; on fire it clears `revealed`.
- **Ephemeral-only (unchanged invariant):** the bulk reveal is called imperatively
  (not via `useQuery`), so resolved plaintext lives ONLY in component state — never in
  the TanStack Query cache, never persisted/logged. Cleared on save (existing) + blur +
  idle + Hide all.

## 2. `⌘/Ctrl+S` to save

A keydown listener (while the editor is mounted): `(metaKey||ctrlKey) && key==='s'`
→ `preventDefault()` and, **only when dirty**, trigger the same save as the dirty-bar
`Save as vN` (`save.mutate()`). No-op when not dirty (don't hijack the browser Save
with nothing to do — but still `preventDefault` when dirty to avoid the browser
dialog). `Esc`-to-cancel-edit already shipped in §7. Arrow/enter row-nav is out of
scope (deferred).

## 3. Dialog accessibility

The hand-rolled `ReviewDiffDialog` + `ImportEnvDialog` use `role="dialog"` +
backdrop-click-close but lack `aria-modal`, `Esc`-to-close, and a focus trap.
Introduce a small shared **`Modal`** primitive wrapping **Radix Dialog**
(`@radix-ui/react-dialog`, already a dependency) — accessible for free (focus trap,
`Esc`, `aria-modal`, backdrop, restore-focus). Refactor both dialogs to compose it,
preserving their existing content, `aria-label`s, and value-free surface (Review-diff
still lists key names only; Import still stages into the buffer). No behavior change
beyond the a11y improvements.

### `web/src/ui/Modal.tsx` (new)
Wraps Radix `Dialog.Root`/`Portal`/`Overlay`/`Content`. Props: `open`,
`onClose` (wired to `onOpenChange`), `label` (→ `aria-label` on Content), `children`.
Token-styled overlay (`bg-ink/30`) + panel (`rounded-card border border-line bg-card
shadow-pop`), centered, `max-w`/width via a `className` passthrough. Content sets
`aria-modal` (Radix does this) and traps focus.

## Testing

- Reveal-all: clicking the toggle calls the bulk endpoint (msw), reveals all rows'
  values; toggle flips to Hide all; Hide all re-masks. A test that a `window` blur
  event re-masks. A test that the bulk reveal fires exactly ONE request (audit-count
  proxy). Idle-timeout re-mask can be tested with fake timers (or documented as
  manually verified if timer testing is flaky — prefer a fake-timer test).
- `⌘S`: dispatching `Meta/Ctrl+S` while dirty triggers the save mutation (one PUT);
  while clean it does not.
- Dialogs: Review + Import render under the new `Modal` with `aria-modal`; `Esc`
  closes; focus is trapped. Keep the existing value-free assertions.
- Security: a test (or assertion) that revealed bulk values never enter the Query
  cache (they're in component state only) — mirror the editor's existing
  ephemeral-plaintext posture.
- Gates: `no-raw-palette`, `typecheck`, full `vitest`, `build`, dual-theme `smoke`.

## Task decomposition (TDD, subagent-driven)

1. `Modal` primitive (Radix Dialog wrapper) + test.
2. Refactor `ReviewDiffDialog` + `ImportEnvDialog` onto `Modal` (+ update tests).
3. Reveal-all toggle + Hide-all + auto-re-mask (blur + 60s idle) in the editor
   (`EditorToolbar` toggle + `SecretEditor` logic) + tests.
4. `⌘/Ctrl+S`-to-save (dirty-only) in `SecretEditor` + test.
