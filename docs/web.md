# Web UI — the Atrium

The SPA is the **Atrium**: Svelte 5 + TypeScript + Vite with a hand-written CSS
design system, built to static assets and embedded in the `janus` binary via
`go:embed` (`internal/web`), served same-origin — no Node in production. It
replaces the earlier React/Nocturne UI.

## Visual system — "Security Printing"

The design language is drawn from banknote engraving, archival ledgers, and
rubber stamps. Two themes, switched from the top bar and persisted in
`localStorage`:

- **Daylight** (default) — warm parchment ground, near-black ink
- **Nightwatch** — the same office after dark: warm black, cream ink

All colors come from CSS-variable tokens in `web/src/styles/tokens.css` (both
themes live there); shared primitives — buttons, stamps, pills, ledger tables,
sheets/plates, ruled fields — in `web/src/styles/base.css`. Type: Fraunces
(display serif), Archivo (UI), IBM Plex Mono (keys, values, hashes), all
bundled locally via Fontsource, so the strict `'self'` CSP holds. Environment
accents: **dev = verdigris, staging = ochre, prod = vermilion**. Native
browser dialogs are never used — confirmations and prompts render as in-app
modals (`web/src/lib/dialog.svelte.ts` + `DialogHost`).

## The gate: init → unseal → login

The app fronts the full server lifecycle:

- **Init ceremony** — on an uninitialized server the UI performs `POST
  /v1/sys/init`: choose shares/threshold and the first admin email; the Shamir
  shares and one-time admin password are displayed exactly once behind an
  acknowledgement gate, then never again.
- **Unseal** — live progress from `seal-status` (keyholes fill as shares land,
  including shares submitted elsewhere, e.g. by the CLI); KMS auto-unseal gets
  a single button. All other routes 503 until unsealed.
- **Login** — email + password, or SSO when an OIDC provider is enabled
  (gated by the unauthenticated `oidc/status` probe).

## Screens

| Route | Screen |
|---|---|
| `/` | Overview — greeting masthead, reads-24h stat strip with audit-histogram sparkline, chain-verified stamp, in-tray (**active break-glass grants**, failing rotations, sync errors, expiring leases, denials, secrets past max-age, secrets not read in 90d), project cards, live event feed. On a fresh instance a **first-run onboarding checklist** (create project → add secrets → mint token → `janus run`) sits at the top; each step auto-checks from existing state, it hides once set up, and it is dismissible (remembered per browser) |
| `/projects` | Dossier list + create |
| `/projects/:id` | Environment board — pipeline editor, env rename/clone/delete, config create, **drag a config tile onto another env column to stage a promotion** |
| `/projects/:id/configs/:cid` | **Secret editor** (below) |
| `/audit` | Audit ledger — chain-verify stamp, hash stitch, result filter, text filter (accepts `?q=`), pagination, JSONL/CSV export |
| `/approvals` | Promotion requests — four-eyes review, approve/reject/cancel, value-free diff |
| `/compare` | **Cross-env diff** — pick any two configs (project → env → config) and see a key-level comparison: only-A / only-B / same / differs, per-side origin, env-accent status pills. **Values stay masked** — the screen never reveals; it only shows presence and whether the values match. Requires read on **both** configs and records one value-free `config.compare` audit event |
| `/tokens` | Service tokens — mint (shown once), revoke |
| `/members` | Members — scoped RBAC bindings at instance / project / environment |
| `/break-glass` | **Break-glass** — guarded, time-boxed emergency role elevation. Loud vermilion treatment: activation form (scope + role + mandatory reason + TTL) and a live list of active grants with a countdown to expiry and an End (revoke) control. Activation is audited and forwarded to notification channels |
| `/transit` | Transit keys — create, rotate, version notches, encrypt/sign bench |
| `/operations` | Rotation / sync / dynamic consoles with **create** flows, pause/resume, run history, credential issuance |
| `/integrations` | OIDC SSO provider, CI federation trust bindings, sync summary |
| `/notifications` | Alerting channels (webhook / Slack) — subscribe to rotation/sync failures, denials, pending approvals; write-only URL/HMAC, test delivery, value-free delivery history |
| `/settings` | Instance info, master-key rotate + Shamir **rekey ceremony**, encrypted backup download, passphrase change, **active sessions** (list + revoke one / sign out everywhere else) |
| `/trash` | Soft-deleted projects/envs/configs — restore or destroy |

`Ctrl+K` opens the command palette (projects, configs, pages including
**Cross-env diff**, actions like theme toggle and audit export). `?` opens a **keyboard-shortcuts help modal**,
and `g`-prefixed chords jump anywhere (`g p` Projects, `g a` Audit, `g s`
Settings, … — the full table lives in the `?` modal). Chords never fire while
typing in a field or while a dialog is open.

## The secret editor

The flagship screen. Reads are masked by default — the list shows key,
origin (own / inherited / override), value version, and age only. Everything
else is explicit:

- **Reveal** — clicking a masked value is an audited read (`secret.reveal`);
  bulk *Reveal all* records one event per key. A toast confirms the ledger
  entry. Revealed plaintext lives only in component state.
- **Dirty buffer** — edits, adds, and deletes accumulate locally (amber rows)
  and commit together as **one immutable config version** ("Save as vN"),
  with Discard as the escape hatch.
- **Multi-line values** — the value editor is a growing textarea; paste JSON,
  PEM blocks, whole files. `Ctrl+Enter` or blur commits to the buffer.
  Collapsed rows show the first line plus a `⏎ n lines` marker.
- **JSON/PEM awareness** — while editing, values that look like JSON (or are
  typed `json`) or contain PEM blocks (or are typed `certificate`/`ssh_key`)
  get a format badge and a client-side well-formedness check: JSON parse
  errors and malformed PEM blocks (label mismatch, bad base64) are surfaced
  inline, and valid JSON offers a one-click **Pretty-print**. Advisory only —
  an invalid value still saves; nothing leaves the browser.
- **Filename-style keys** — keys may be filenames (`service-account.json`);
  invalid keys are rejected inline with the same rule as the server, and
  non-env-var keys carry a `file` badge ("skipped by `janus run` — use
  `janus secrets download --format files`").
- **Import…** — bulk import from `.env` or Java `.properties` (paste or file
  picker), parsed locally with a preview (new / overwrite / invalid per line)
  and per-key selection; staged into the dirty buffer, committed on Save.
  See [Importing & exporting](guides/import-export.md).
- **Download .env** — confirm-gated export: every value is revealed (audited
  per key) and serialized as a properly quoted dotenv file; file-keys are
  skipped with a comment.
- **Bulk selection** — a checkbox per persisted row plus a select-all header
  checkbox (which selects only the currently *visible* rows, so a filter
  narrows the set). When one or more rows are selected a bulk-action bar
  appears with **Reveal selected** (audited per-key reveal of each row),
  **Export selected .env** (the same confirm-gated, audited dotenv export
  scoped to just the selection), and **Delete selected** (stages soft deletes
  into the dirty buffer, confirmed via the in-app dialog, committed on Save).
  Selection clears after any action and on save. All reveals/exports go
  through the existing audited reveal path.
- **Per-key history** — value-free version list per key, with audited reveal
  of any historical value.
- **Locked keys** — lock/unlock per key (`⚿`); locked keys cannot be
  overwritten by promotions.
- **Max-age (advisory)** — a vermilion "past max-age" chip appears on any
  key older than its effective max-age (per-key override, else the config
  default). A per-row **Max-age** control sets/clears a key's override and
  a toolbar control sets the config default. Purely advisory — nothing is
  blocked; the overview In tray also surfaces "N secrets past max-age".
- **Annotations (advisory)** — a per-row **Owner…** control opens a popover
  to set an **owner** label and a free-text **note** on a key ("what is this
  and who do I ask"). When set, an **owner · note** line renders under the
  key. Value-free metadata (no secret value); purely informational — nothing
  is blocked. Setting/clearing is a `secret:write` and emits a value-free
  audit event.
- **Unused (advisory)** — an ochre "not read 90d+" / "never read" chip
  appears on any key with no per-key reveal within the unused window
  (default 90 days, `JANUS_UNUSED_SECRET_DAYS`), computed from audit
  reveals. Purely advisory — nothing is blocked; the overview In tray also
  surfaces "N secrets not read in 90d". Bulk raw reads are not per-key
  attributable, so a bulk-only key still reads as "never read".
- **Read insights (advisory)** — a per-row **Reads…** control expands a panel
  showing the key's **last-read** relative time and a **30-day daily reveal
  sparkline**, derived from audit reveals (`GET /v1/configs/{cid}/read-insights`,
  loaded once per editor open). Value-free — counts and timestamps only, never
  a value. Only per-key reveals are attributed (bulk raw reads are not), so a
  bulk-only key shows "never read per-key".
- **Promote →** — key-level diff against the next pipeline stage; apply
  directly or file an approval request. See
  [Promoting between environments](guides/promoting-environments.md).
- **Config versions** — history panel with real diffs (added/changed/removed
  chips) and rollback (a new version identical to the target — nothing is
  rewritten).

## Security posture

- Revealed plaintext and unseal shares never enter persistent storage; the
  only shown-once surfaces (init shares/password, minted tokens, issued
  dynamic credentials, rekey shares) render once and are gone on dismiss.
- All mutations flow through the `/v1` API with the session cookie; the SPA
  is same-origin, so CORS stays closed and the CSP stays `'self'`.
- Write-only credential fields (rotation admin DSNs, sync PATs/tokens/CA
  certs, dynamic-role DSNs, the OIDC client secret) are never echoed back by
  the API and never rendered from fetched data.

## Development

```sh
cd web && npm run dev     # Vite on :5173, proxies /v1 → the Go server
npx svelte-check          # type-check
make build                # build web → embed → binary
```

Data flows through the typed client `web/src/lib/api.ts` (mirrors `/v1`) and
the rune stores `session.svelte.ts` / `registry.svelte.ts`. The ops list
endpoints require scope params — aggregate across the tree via
`web/src/lib/ops.ts`.
