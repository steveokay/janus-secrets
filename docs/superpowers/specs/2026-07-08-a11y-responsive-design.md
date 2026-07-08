# §7 — Accessibility & Responsiveness — Design

FE punch-list §7. Frontend-only. A targeted a11y/responsive pass. Much of §7 is
ALREADY satisfied by earlier work — this slice closes the remaining concrete gaps
rather than re-doing solved items.

## Already satisfied (verify, don't redo)

- **Focus rings:** `web/src/index.css` has a GLOBAL `:focus-visible { outline-none
  ring-2 ring-brand ring-offset-2 ring-offset-page }` in `@layer base` — every
  interactive element (links, buttons, inputs, icon-buttons, `[tabindex]`) already
  gets the 2px brand ring. No per-component work needed.
- **Contrast:** tokens are AA; the dark-AA guard test (`dark-aa.test.ts`) bans the
  sub-AA `text-brand-deep` class. No known sub-AA text.
- **Menu/dialog keyboarding:** Radix (`DropdownMenu`, `AlertDialog`, `Dialog`,
  `Tooltip`) provides focus-trap, arrow-nav, `Esc`, and `aria-*` out of the box.
- **Board column overflow:** `home/ProjectBoard.tsx` env columns already use
  `overflow-x-auto`.

## Concrete gaps to close

### 1. Secret-editor table — horizontal-scroll containment (responsive)
`web/src/secrets/SecretTable.tsx` renders a fixed 5-column grid
(`grid-cols-[1.3fr_1.5fr_108px_56px_92px]`). On narrow/tablet widths the columns
squish. Wrap the table card in an `overflow-x-auto` container and give the card a
`min-w-[720px]` so that below that width the table scrolls horizontally **inside its
own container** — the page body must never scroll sideways. Radius clipping is
preserved (the card keeps `rounded-card overflow-hidden`).

### 2. Editor `Esc`-to-cancel-edit (keyboard)
In `SecretTable.tsx`, the per-row edit `<input>` can be cancelled today only via the
X button (`onRevert`). Add `onKeyDown` so pressing **Escape** in the edit input calls
`onRevert(key)` — cancels the in-progress edit and exits edit mode. (This is the
editor keyboard gap in §7; deeper editor nav — `⌘S`, arrow row-nav — stays in the
§3-P2 backlog per the approved scope.)

### 3. Page-level responsive overflow guard
Ensure the app never produces a horizontal body scrollbar at tablet width. The main
content region should contain its own overflow (wide children — the secret table now
scrolls internally per #1; the board columns already do). Add `min-w-0` where a flex
child could otherwise force the parent wide (a common flexbox overflow cause), and
verify the shell (`web/src/shell/*`) main area doesn't overflow. Keep changes
minimal and targeted — no layout redesign.

## Testing

- `SecretTable`/editor: a test asserting the table is wrapped in an
  `overflow-x-auto` container (query the container class or a `data-` hook), and a
  test that pressing `Escape` in a row's edit input exits edit mode (the edit input
  disappears), mirroring the existing "cancel edit" coverage.
- Existing suite must stay green (was 195). Gates: `no-raw-palette`, `typecheck`,
  full `vitest`, `build`, dual-theme `smoke`.
- Manual responsive check (documented, not automated): the built UI at a tablet
  width shows no horizontal body scrollbar; the secret table scrolls within its card.

## Task decomposition (TDD, subagent-driven)

1. `SecretTable` — `overflow-x-auto` containment + `min-w` + `Esc`-to-cancel-edit +
   tests.
2. Page-level responsive overflow guard (`min-w-0` on the shell/main flex children as
   needed) — small, verified by build + smoke + a no-horizontal-overflow eyeball.
