# B4 — Token & Member Management (design)

- **Status:** APPROVED 2026-07-06 (queue order + standing autonomy per Steve).
- **Visual authority:** locked visual system; tables/dialogs follow Slice-1/B2
  conventions (Pill, ConfirmDialog, Sheet, Toast, EmptyState all exist).
- **Tracker:** `fe-improvements.md` §8 (token management, member management).

## API contract (recon verified against Go handlers/e2e — mocks mirror EXACTLY)

### Tokens
- `POST /v1/tokens` body `{name, scope:{kind,id}, access, ttl_seconds?}` —
  kind ∈ `config|environment|transit` (transit id = `""` for all-keys); access
  ∈ `read|readwrite` (config/env) or `use|manage` (transit); ttl positive.
  → 200 `{token, id, name, scope:{kind,id}, access, expires_at}` — **`token`
  is the raw one-time value, never retrievable again.** 400 invalid, 404 scope
  target, audited `token.mint`.
- `GET /v1/tokens` → `{tokens:[TokenMeta]}` where TokenMeta =
  `{id, name, scope_kind, scope_id, access, created_by, created_at,
  expires_at?, revoked_at?}` (omitempty on the two optionals). Rows are
  filtered server-side to scopes the caller can TokenRead.
- `DELETE /v1/tokens/{id}` → 204; audited `token.revoke`; 404 unknown.

### Users
- `POST /v1/users` `{email}` → 200 `{id, email, password}` — **one-time
  password, same show-once semantics as tokens.** 400 duplicate/missing.
- `GET /v1/users` → `{users:[{id, email, disabled}]}`.
- `POST /v1/users/{id}/disable` → 204; 409 "cannot disable yourself" / "cannot
  disable the last instance owner"; audited `user.disable`.
- All gated by `user:manage` (instance) — 403 for lower roles.

### Members (three scopes, identical shape)
- `/v1/instance/members` · `/v1/projects/{pid}/members` ·
  `/v1/projects/{pid}/environments/{eid}/members`
- `GET /` → `{members:[{user_id, role}]}` (`member:read`). **No email — join
  client-side with `/v1/users`** (fall back to a truncated user_id when the
  caller can't list users).
- `PUT /{uid}` `{role}` role ∈ `viewer|developer|admin|owner` → 204
  (`member:manage`, audited `member.grant`). 403 "cannot grant a role above
  your own" (delegation ceiling); 404 user; 409 "cannot demote the last
  instance owner".
- `DELETE /{uid}` → 204 (audited `member.revoke`); 404 binding; 409 last owner.

## Units

1. **`web/src/ui/RevealOnce.tsx`** — shared show-once modal (Radix Dialog like
   Sheet but centered): props `{open, onClose, title, secret, hint}`; renders
   the secret in a mono `select-all` block with a Copy button (toast "Copied —
   store it now, it won't be shown again"), a warning line, and a single
   "I've stored it" close button. Secret lives ONLY in caller state; never in
   query cache, storage, or toasts.
2. **`web/src/lib/endpoints.ts`** — `TokenMeta`, `MintTokenRequest`,
   `MintTokenResult`, `UserInfo`, `Member` types per the contract; endpoints
   `mintToken`, `listTokens`, `revokeToken`, `createUser`, `listUsers`,
   `disableUser`, `listMembers(scopePath)`, `putMember(scopePath, uid, role)`,
   `deleteMember(scopePath, uid)` where `scopePath` is one of the three
   member base paths built by a `memberScopePath(scope)` helper.
3. **`web/src/tokens/TokensPage.tsx`** (route `/tokens`):
   - Table: Name · Scope (kind Pill — config=brand, environment=info,
     transit=muted — plus the config/env NAME resolved from the nav query
     caches when available, else a truncated id) · Access · Created
     (timeAgo) · Expires (timeAgo-style or "never") · status (revoked_at →
     danger Pill "revoked").
   - "Mint token" primary button → Sheet with the mint form: name input;
     scope kind select; for config/environment a cascading project → env
     (→ config) picker built on `useProjects/useEnvironments/useConfigs`;
     access select whose options switch by kind (`read|readwrite` vs
     `use|manage`); optional TTL (number input, seconds, blank = never).
     Submit → on success close the sheet and open `RevealOnce` with the raw
     token; invalidate `['tokens']`.
   - Revoke: ConfirmDialog (danger tone, "Revoke <name>? Clients using it stop
     working immediately.") → DELETE → toast; row stays with revoked pill
     after invalidation.
   - 403 → EmptyState "Token access required" (admin+); loading skeletons;
     zero tokens → EmptyState with mint CTA.
4. **`web/src/members/MembersPage.tsx`** (route `/members`):
   - Scope selector row: segmented select Instance | Project (project picker
     appears) | Environment (project + env pickers) → resolves to a
     `scopePath`; members query keyed `['members', scopePath]`.
   - Members table (joined with `['users']` when available): Email (or
     user_id prefix) · Role (per-row `<select>` of the four roles; change →
     ConfirmDialog "Change role to X?" → PUT → toast; 403 ceiling / 409
     last-owner surface as danger toasts with the server message) · Remove
     (ConfirmDialog danger → DELETE → toast).
   - "Add member" button → Sheet: user select (from `/v1/users`, enabled
     users only) + role select → PUT.
   - **Users section** (below, instance scope only, requires user:manage):
     table Email · Status (disabled Pill) · Disable action (ConfirmDialog;
     self/last-owner 409s surface as danger toasts); "Create user" button →
     Sheet with email input → on success `RevealOnce` with the one-time
     password; invalidate `['users']`.
   - 403 on members → EmptyState "Member access required".
5. **Routes** — `web/src/App.tsx`: `/tokens` → TokensPage, `/members` →
   MembersPage (Placeholder stays only for transit/settings).

## Security

- Raw token / one-time password: component state only, cleared on RevealOnce
  close; never in query cache (mutation responses aren't cached), storage,
  URLs, or toast titles. No masking theater — it's a one-time reveal by
  design, matching the CLI's behavior.
- All guardrails are server-enforced; the UI's job is honest surfacing:
  ceiling/last-owner/self errors become danger toasts carrying the server's
  message (these messages are static server strings, never user input echoes).
- Role changes and revokes always confirm first (ConfirmDialog).

## Error handling

Per-surface: query errors inline; mutation errors → danger toast with
`ApiError.message` when status ∈ {403, 409} (curated server strings) else
generic "Request failed."; 403 list-level → EmptyState.

## Testing (msw, recon shapes verbatim)

- RevealOnce: renders secret + copy → toast; close clears via onClose.
- endpoints: param/body/envelope tests incl. `memberScopePath` for all three
  scopes and omitempty handling on TokenMeta optionals.
- TokensPage: list render incl. revoked pill + scope pills; mint flow (kind
  switch changes access options; POST body asserted; RevealOnce shows raw
  token; list invalidated); revoke confirm → DELETE; 403 EmptyState.
- MembersPage: instance list joined emails; scope switch to project (path
  asserted); role change confirm → PUT body; ceiling 403 → danger toast with
  server message; remove → DELETE; add-member flow; user create → RevealOnce
  password; disable self → 409 toast; 403 EmptyState.
- Gates: full web + `npm run smoke` + go build (no Go changes in B4).

## Out of scope

Token rotation/renaming · re-enabling disabled users (no endpoint) · project/
env member EmptyStates beyond the standard ones · transit scope picker beyond
the all-keys default (id stays `""`) · pagination (lists are small,
single-tenant) · OIDC-related member sources (sub-project C).
