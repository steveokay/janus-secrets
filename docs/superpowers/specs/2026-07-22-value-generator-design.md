# Value generator in the editor — design

**Date:** 2026-07-22
**Status:** approved (all recommendations accepted 2026-07-22)

## Problem

The secret editor has no way to generate a strong value, so people invent weak
ones (or paste from ad-hoc tools). Add an on-demand generator (random password /
hex / base64, with a length picker) directly on the editable value cell. Names
already exist; this closes the "what do I put here" gap for new/rotated secrets.

## Decisions

- **Client-side only.** Generation uses the browser CSPRNG
  (`crypto.getRandomValues`); the value goes straight into the editor's existing
  dirty buffer (`row.draft`) exactly like a typed value and is saved through the
  normal encrypted `SetSecrets` path. No new endpoint, no migration, no new
  leakage surface — the plaintext lives only in component state until save.
- **Generators:** password (with toggles), hex, base64. Length picker for each.
- **Unbiased selection.** Password character selection uses rejection sampling
  over `crypto.getRandomValues`, never `% charset.length` (which biases toward
  low indices). Hex/base64 derive from raw random bytes so they are unbiased by
  construction.

## Component: `web/src/lib/generate.ts` (pure, dependency-free)

```
generatePassword(length: number, opts: { symbols: boolean; excludeAmbiguous: boolean }): string
generateHex(bytes: number): string      // 2*bytes lowercase hex chars
generateBase64(bytes: number): string   // standard base64 of `bytes` random bytes
```

- **Charset (password):** `a–z A–Z 0–9`, plus symbols `!@#$%^&*()-_=+[]{};:,.?`
  when `symbols`. `excludeAmbiguous` removes `0 O o 1 l I |` from the pool.
- **Rejection sampling:** draw a random byte; reject values `>= floor(256 /
  n) * n` (n = pool size) to avoid modulo bias; retry until accepted. Fill to
  `length`.
- **Bounds (enforced in the util, clamped, never throw on UI input):** password
  8–128; hex/base64 8–256 bytes. A pool that ends up empty (all classes off) is
  impossible — the base alphanumerics are always present.

This file is small and obviously correct; it is the unit that owns randomness so
it can be reasoned about in isolation.

## UI: generator popover on the value cell

- On each **editable** value cell (an `added` row, or a row in `editing`), add a
  small "Generate" affordance (a key/dice glyph button) beside the value input.
- Clicking it opens a compact popover anchored to that row containing:
  - **Type**: Password / Hex / Base64 (segmented or select).
  - **Length**: a number input (chars for password, bytes for hex/base64) with
    sensible min/max and the defaults below.
  - **Password only**: "Include symbols" and "Exclude ambiguous" toggles.
  - **Generate** (primary) — fills the row's `draft`, calls the editor's existing
    `markDirty(row)`, keeps the row editing, and closes (or stays open for
    re-rolls — a **Regenerate** re-draws in place).
- **Defaults:** password length 24, symbols on, ambiguous allowed; hex 32 bytes;
  base64 32 bytes.
- Popover dismisses on outside-click / `Esc`, mirrors existing menu/popover
  behaviour in the app, and uses Atrium tokens/primitives (`.btn`,
  `.field-ruled`, `.folio`, popover surface) — renders correctly in both themes.
- The generated value is shown in the normal (revealed, editing) value input so
  the user sees what will be saved; it is never persisted anywhere but the dirty
  buffer, and only reaches the server via the normal save.

Keep the popover self-contained (its own small `GenerateMenu.svelte`, or a
tightly-scoped block in `SecretEditor.svelte`) so `SecretEditor.svelte` doesn't
grow tangled — prefer a small component that takes a callback
`onGenerate(value: string)`.

## Testing / verification

The web app has **no JS test suite** (`npm test` is a stub), so verification is
`npm run check` (svelte-check + tsc, 0 errors) + `npm run build` + review.
`generate.ts` is written to be self-evidently correct: charset coverage, rejection
sampling (no modulo bias), exact output length, and bounds clamping are checked
by inspection. No Go changes.

## Non-goals (YAGNI)

- No passphrase / wordlist (diceware) mode.
- No server-side generation or a generate endpoint.
- No per-type auto-suggest or auto-fill on add — the generator is on-demand.
- No strength meter.
