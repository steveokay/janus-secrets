# B5 — Transit (KMS) Web UI Design Spec

**Status:** Approved (2026-07-07). Built in the dark redesign system (R1–R4). New screen — not in `docs/design/ui-redesign-mockup.html`; composes from its tokens + kit.

## Goal

A web UI for the transit engine: manage named encryption/signing keys and run a small, plaintext-free crypto playground. Instance-scoped. Dev-focused, Doppler-ish, minus SaaS.

## Scope decision (locked)

**Console + safe playground (no decrypt).** The playground exposes only operations whose responses never contain plaintext: `encrypt`, `rewrap` (aes256-gcm) and `sign`, `verify` (ed25519). **No `decrypt`, no `datakey/plaintext`, no `datakey/wrapped`** in the UI — those stay CLI/API-only. Result: zero secret plaintext ever enters the browser via transit.

## Placement & nav

- New top-level **instance** nav item **"Transit"** in `web/src/shell/Sidebar.tsx` (alongside Members/Tokens/Settings), icon `KeyRound` (lucide). Route **`/transit`** wired in `web/src/App.tsx`.
- Not under any project (transit is instance-scoped).

## API contract (verified against `internal/api` + `internal/transit`)

All routes under `/v1/transit/`, `RequireAuth`, instance-scoped RBAC. Error envelope `{"error":{"code,message}}`. 503 when sealed.

**Key management** (audited):
- `POST /v1/transit/keys` — `transit:manage` — req `{name, type}` (`type` ∈ `aes256-gcm`|`ed25519`; name `^[A-Za-z0-9_-]{1,64}$`) → 201 key-meta. 409 on duplicate.
- `GET /v1/transit/keys` — `transit:read` — → `{keys: KeyMeta[]}`.
- `GET /v1/transit/keys/{name}` — `transit:read` — → KeyMeta.
- `POST /v1/transit/keys/{name}/rotate` — `transit:manage` — empty body → KeyMeta (latest_version++).
- `POST /v1/transit/keys/{name}/config` — `transit:manage` — `{min_decryption_version?:int, deletion_allowed?:bool}` (min_dec ∈ [1, latest]) → KeyMeta.
- `POST /v1/transit/keys/{name}/trim` — `transit:manage` — `{min_available_version:int}` (≤ min_decryption_version) → KeyMeta.
- `DELETE /v1/transit/keys/{name}` — `transit:manage` — → 204. **409 `conflict` "deletion not allowed for this key"** when `deletion_allowed=false`.

**KeyMeta** (exact json): `{ name:string, type:string, latest_version:int, min_decryption_version:int, deletion_allowed:bool, versions:int[] }`.

**Crypto ops** (NOT audited), `transit:use`:
- `POST /v1/transit/encrypt/{name}` — `{plaintext:base64, associated_data?:base64}` → `{ciphertext:"janus:vN:…"}`.
- `POST /v1/transit/rewrap/{name}` — `{ciphertext, associated_data?}` → `{ciphertext}` (re-wrapped under latest).
- `POST /v1/transit/sign/{name}` — `{input:base64}` → `{signature:"janus:vN:…"}` (ed25519 only).
- `POST /v1/transit/verify/{name}` — `{input:base64, signature}` → `{valid:bool}` (ed25519 only; bad signature = 200 `valid:false`, malformed = 400).

Wrong-key-type / bad-base64 / version-too-old / bad-ciphertext → 400 `validation` "invalid input". Not-found → 404. RBAC denial → 403.

## Screen layout

Two regions using existing kit (cards, `Pill`, `Sheet`/`ConfirmDialog`, `EmptyState`, `Toast`, `apiErrorTitle`), dark tokens only.

### ① Keys list
- Header: "Transit" title + **New key** primary button. Subtitle: one line explaining named keys.
- Empty state (`EmptyState`, `KeyRound` icon): "No transit keys yet" + create CTA.
- Each key row/card: name (mono, `text-ink`) · **type badge** (`aes256-gcm` → `Pill tone="info"`, `ed25519` → `Pill tone="brand"`) · `v{latest_version}` chip (`Pill tone="muted"`) · `min_decryption_version` shown as `min-dec v{n}` when > 1 · a `deletion_allowed` cue (open padlock when true, closed when false, `text-faint`). Selecting a key opens/focuses the playground for it (selected state highlighted).
- Per-key **⋯ menu** (Radix dropdown, same pattern as UserMenu): **Rotate** (immediate, toast on success), **Configure…** (opens a small dialog: `min_decryption_version` number input bounded `[1, latest]`, `deletion_allowed` checkbox), **Trim…** (dialog: `min_available_version` number, ≤ min_decryption_version), **Delete** (ConfirmDialog; on 409 surface the server message verbatim as a danger toast).
- New-key modal: name input (pattern-validated client-side with a hint) + type radio (aes256-gcm / ed25519). 409 duplicate → verbatim error line.

### ② Playground (for the selected key)
Operation set depends on `key.type`:
- **aes256-gcm:** *Encrypt* (multiline text input; encoded to base64 client-side before send; optional "Associated data" text field also base64-encoded) → shows `ciphertext` (mono, copyable). *Rewrap* (paste a `janus:vN:…` ciphertext; optional AAD) → new `ciphertext`.
- **ed25519:** *Sign* (text input → base64) → `signature` (mono, copyable). *Verify* (text input + a `signature` field) → a result badge: green "Valid" (`Pill tone="success"`) / red "Invalid" (`Pill tone="danger"`).
- Inputs default to a **UTF-8 text** box that the UI base64-encodes on submit (dev convenience), with the sent base64 not hidden. Ciphertext/signature envelopes are pasted verbatim.
- Every op is a **mutation** (`useMutation`), results held in **local component state only — never written to the query cache, never logged**. Errors (400/403/404) → `apiErrorTitle`-derived toast; 403 explains the missing `transit:use`.

## Data & security posture

- `useTransitKeys()` → `useQuery(['transit','keys'])`. Mgmt mutations `invalidateQueries(['transit','keys'])`.
- Crypto op mutations are **not cached** (no query key); outputs live in component state and clear on key-switch / unmount.
- No plaintext in any response (decrypt/datakey excluded), so no reveal/audit machinery needed. Inputs the user types are ephemeral.
- Follow B4's guardrail posture: attempt actions; surface `403`/`409` server messages via `apiErrorTitle` (403/409-only exposure) rather than pre-hiding controls. Non-exposed statuses collapse to a generic message.
- msw mocks MUST mirror the Go wire shapes above (mock-drift rule).

## Testing

- Unit/component (vitest + msw): key list render (both types, version/min-dec/deletion cues), create (success + 409 duplicate), rotate, configure (bounds), trim, delete (success + 409 deletion-not-allowed verbatim), playground encrypt (base64 encoding of input, ciphertext shown), rewrap, sign, verify (valid + invalid badge), 403 on a crypto op surfaces guardrail. no-raw-palette + dark-aa gates stay green. `npm run smoke` both themes.

## Out of scope

- decrypt, datakey (plaintext/wrapped) in the UI.
- Transit usage metrics (data-plane isn't audited; belongs to Phase-2 D).
- Backend changes (frontend-only over existing `/v1/transit` APIs).
