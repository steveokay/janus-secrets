# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.3.0] - 2026-07-28

RBAC at organisation scale. An organisation with many product teams can now run
one Janus: teams own their projects, manage access through their identity
provider, create their own projects without seeing anyone else's, and audit
themselves — with production changes gated by four-eyes approval as a property
of the environment rather than a checkbox somebody had to remember.

### Changed
- **BREAKING: secret ownership moved from the key to the project.** Per-key
  `owner` was the wrong grain. A *service* has an owner; its individual keys
  almost never do, so the field was either repeated on every key or left empty
  — and neither told anyone anything. It also read confusingly beside
  group-owned projects, where a binding genuinely says which team owns what.

  `owner` is now a field on the **project** (`PATCH /v1/projects/{pid}`, guarded
  by the same `project:update` as the name; shown on the dossier header). It
  remains **advisory**: a display label that grants nothing, blocks nothing, and
  is never consulted in an authorization decision. Real ownership is a role
  binding. Project, not environment, because ownership does not differ between a
  service's dev and prod — an environment-level field would be the same value
  copied three times and would drift.

  The per-key **note** is unchanged and stays where it is: "read replica, rotate
  with the primary" is real per-key information that does not generalise.

  Sending `owner` to `PUT /v1/configs/{cid}/secrets/{key}/annotation` now
  returns **400** rather than being ignored — silently dropping a field an
  operator supplied would leave them believing ownership was recorded when it
  was not. `owner` is likewise gone from the masked-list entries.

  **No operator-entered data is destroyed on upgrade** (migration `000049`).
  Where every annotated key in a project agreed on a single owner, it is
  promoted to the project — the case this change is for, and it converts
  cleanly. Where they disagreed, promotion would lose the detail, so each owner
  is folded into that key's note instead (`owner: alice@corp.io`). Verified
  against real Postgres with a fixture covering both shapes.

### Added
- **Scoped audit read.** Every audit endpoint authorized against the instance
  scope, so a team lead could not review their own project's trail without being
  handed every event in the organisation — and audit rows carry resource paths
  and key names, so that leaked the shape of every other team's secrets. It cut
  the other way too: teams that should have been self-auditing simply could not.
  With project visibility, groups and delegated creation already shipped, this
  was the last place the isolation story leaked.

  `audit:read` is now honoured at **project** scope. Filtering could not be
  derived from the resource string — it is free-form, and a prefix/`LIKE` scheme
  would silently mis-scope events, which for an audit view is the worst possible
  failure because it would *look* complete. So the scope is recorded on the
  event at write time (migration `000048`), captured at **authorization** time
  rather than threaded through 144 `record()` call sites: an event's scope is
  the scope its operation was authorized against. A request that authorizes
  against two different projects is marked ambiguous and recorded with no scope,
  so a cross-project action is never mis-attributed to one side and hidden from
  the other's trail.

  The column is deliberately **outside the chain hash** — `computeHash` covers a
  fixed field list under the `janus:audit:v1` tag, and a hashed field would
  invalidate every existing event and break `GET /v1/audit/verify` on upgrade.
  That makes `project_id` an **index, not evidence**: someone with direct
  database access could re-point an event to hide it from a *scoped* view. The
  instance-wide view stays complete and the chain still detects tampering with
  the event's own contents, but the distinction is recorded in
  `docs/threat-model.md` as an explicit non-defense rather than glossed over.

  `NULL` means "not attributable to one project" — instance-level actions,
  cross-project operations, and everything written before the upgrade — all
  visible only to instance-wide readers, so a team's scoped history starts at
  the upgrade rather than being backfilled with guesses. `verify`, `checkpoint`
  and `prune` stay instance-only, because the hash chain covers every event and
  a subset cannot be verified. Scoping is project-level; an environment-scoped
  binding confers nothing and such a caller is denied outright rather than shown
  a partial trail. The restriction is applied in SQL, pinned by a test that
  walks every page at `limit=3` over interleaved two-project writes and asserts
  an exact count — a post-filter would let the keyset cursor silently truncate.
  `GET /v1/audit/events` returns `scoped`/`scope_projects`, and the viewer shows
  a **Scoped view** stamp so a partial trail is never presented as the ledger.

### Fixed
- **The owning-team picker was unusable by delegated creators.** Listing the
  group catalog needs instance `group:manage`, so the picker shipped invisible
  to exactly the people it exists for — and anyone belonging to two creating
  groups could not create a project at all, because the server rightly refuses
  to guess which team owns it and the UI offered no way to name one. Adds
  `GET /v1/auth/me/groups`, authenticated-only, returning just the caller's own
  memberships and never the catalog. The picker now merges the catalog (admins)
  with the caller's own groups (everyone).
- **Project creation authorized after reading the request body.** Delegated
  project creation moved body decoding ahead of the permission check so it could
  read `owner_group_id`. An unauthorized caller could therefore tell a malformed
  body (`400`) from a denial (`403`), and — more materially — the
  `project.create` **denied** audit event was skipped on that path, so a probing
  account left no trail. The decision is now made before the body is read: "may
  you create projects at all?" never depends on request content, only "which
  group owns this one" does. Found by a consolidation review of the same day's
  changes and pinned by a test that sends six unusable bodies and requires `403`
  for every one.
- **An instance admin could not hand a new project to a team they were not in.**
  The owner-group check required membership for every caller. That is the right
  rule for a delegated creator — it is what stops someone planting access into
  another team's group — but wrong for an instance admin, who already holds
  `member:manage` everywhere and could bind the group a moment later. Admins may
  now name any existing group; an unknown one is a `400` rather than a `403`.
  Belonging to several creating groups without naming one is also now a `400`,
  since the caller is authorized and merely needs to disambiguate.

### Added
- **Delegated project creation.** `handleProjectCreate` authorized against the
  instance scope, so the only way to let a team create its own projects was to
  grant instance admin — which carries `project:read` everywhere and therefore
  reveals every project in the organisation. "Teams self-serve" and "teams
  cannot see each other" were mutually exclusive, and an org faced with that
  choice picks self-serve and quietly makes everything visible.

  A group may now be marked `can_create_projects`. A member creates a project
  owned by that group, and it is bound to the **group at admin** so the whole
  team can work in it at once, and to the **creator at owner** so it always has
  someone who can administer and delete it (a group binding can never be owner,
  by design). No instance-wide read is granted at any point.

  This is a narrow capability, deliberately **not** a new rung on the role
  ladder. Roles are cumulative bundles — viewer ⊂ developer ⊂ admin ⊂ owner —
  so any role granting `project:create` at instance scope would also grant
  `project:read` there, which is exactly the leak being closed. It is checked
  alongside the authorization engine rather than inside it, which is defensible
  because creation is the one operation with no existing resource to authorize
  against — the reason it sat at instance scope in the first place.
  `internal/authz` stays a pure decision function over roles.

  Naming a group you are not a member of returns the same `403` as having no
  capability at all, so it is not a way to discover which groups exist; a member
  of several creating groups must name the owner rather than have Janus guess.
  Ships migration `000047`, `PUT /v1/groups/{gid}/capabilities`,
  `janus group delegate-creation <name> [--off]`, `janus group create
  --can-create-projects`, a **delegate…** control on the Groups screen, and an
  **Owning team** picker on project creation.
- **Four-eyes protection at environment scope.** `require_approval` already
  existed per config, but it defaulted to false and nothing ever set it — so a
  config created in a production environment started **unprotected** and stayed
  that way until a human remembered to tick a box. That was load-bearing in a
  way it did not look: the recorded reason this system has no deny rules is that
  a broad grant is acceptable *because* a write to production becomes a
  four-eyes request rather than a commit. The argument rested on a default that
  did not exist. Group bindings sharpened it further, since one project-scope
  binding now grants production write to a whole team at once.

  Protection is therefore a property of the **environment** as well, and the
  effective value is the union — `config.require_approval OR
  environment.require_approval` — deliberately the same shape as role bindings:
  union, no precedence, no deny. A config may add protection its environment
  does not require; it can never remove protection the environment does, so
  production four-eyes survives a newly created config and cannot be switched
  off one config at a time. Every path that can mutate secrets (batch save,
  per-key set, delete, rollback, promote-apply) resolves protection through one
  shared check, so no door skips it, and that check fails **closed** if the
  environment cannot be read.

  Ships migration `000046`, `PUT /v1/projects/{pid}/environments/{eid}/require-approval`
  (`promotion:manage`), `janus env protect <slug> [--off]`, a Protect control and
  four-eyes badge on each environment column of the project board, and an editor
  banner that distinguishes protection inherited from the environment from the
  config's own — the toggle says so rather than firing a request that would
  appear to succeed and change nothing. Config responses gain
  `environment_require_approval` and `effective_require_approval`; clients must
  read the latter.
- **Group-based role bindings — one binding for a whole team.** A binding may
  target a **group** instead of a user, so "Team Payments owns these projects"
  is one row per project rather than one per person per project, and
  offboarding stops being a hunt for individual rows. Group bindings **union**
  with direct ones exactly as two direct bindings already do — no precedence
  tier, no deny rules, deny-by-default unchanged.

  A group is one of two kinds and never both. `oidc` groups carry a claim value
  and their membership is a snapshot refreshed from the IdP's group claim at
  each login; an admin can never hand-add a member. `local` groups have an
  explicit list, which is what an instance with no identity provider uses and
  what covers password logins. The distinction is enforced by the schema rather
  than by a handler — `group_members` carries a denormalised `group_kind` and a
  composite foreign key to `groups(id, kind)` — so a hand-added member of an
  IdP-fed group is *unrepresentable*. That is what makes one statement true and
  keeps it true: **access granted via an IdP group is fully described by the
  IdP**, so an access review run against Entra or Okta is complete for those
  bindings. A single group that was both IdP-fed and hand-editable would have
  made that review return a clean result that was not true, and would have
  turned an IdP outage into a permanent invisible grant, since a login sync
  only ever clears the rows it owns (temporary access already has a
  purpose-built path in break-glass, which is TTL-clamped, loudly audited and
  expires by itself). Group names are unique across both kinds, and a group's
  display name is separate from its claim value because Entra emits group
  GUIDs.

  **A group binding can never grant `owner`** — refused by the API and by a
  `CHECK` constraint. Owner rotates the master key, prunes the audit chain and
  hard-destroys secret history; group-deriving it would hand that tier to
  whoever administers the identity provider, who can add themselves silently
  and whose membership list Janus cannot authoritatively enumerate (the
  snapshot only covers users who have logged in). A consequence worth naming:
  every instance owner therefore remains a direct binding, so the never-lock-out
  guard needed no change and an IdP outage can never leave the instance without
  an owner.

  The authorization engine stayed a pure decision function. Group bindings
  arrive through an optional second source (`WithGroups`, mirroring the
  existing break-glass `WithGrants`) as ordinary role bindings stamped with
  their origin, so `userAllows`, `bindingApplies`, `BoundRole` and
  `EffectiveRole` gained no new concepts — they simply see a longer slice. It
  fails closed: an error from the group store denies rather than quietly
  resolving against direct bindings alone. Group-derived roles do count toward
  the delegation cap, because a group binding is durable; the M-1 invariant is
  untouched, since break-glass still arrives through a different interface and
  is still excluded from `BoundRole`.

  Managing the catalog and binding a group are deliberately different
  authorities: creating groups, editing local membership and setting the claim
  mapping is instance-scoped `group:manage`, while binding a group at a scope
  is `member:manage` **at that scope**, under the identical `BoundRole` cap
  `memberPut` applies. Without that cap, groups would have been a way around
  it. The practical effect is that a project admin can grant a group access to
  their own project but cannot add themselves to a group bound elsewhere, and
  so cannot reach a project they do not already administer.

  The claim resolver distinguishes *"this user is in no groups"* from *"this
  token cannot tell us"*, which is the whole safety of the sync. An absent
  claim clears the snapshot, failing closed — access is lost, never gained — if
  an operator misconfigures the IdP. But Entra stops emitting `groups` once a
  user exceeds roughly 200 of them and sends a Microsoft Graph pointer instead;
  reading *that* as "no groups" would clear every membership the user has and
  read exactly like a legitimate removal from all of them, so it is treated as
  unknown, the snapshot is retained, and a `group.sync` audit event records
  `status=overage`. Non-string elements, more than 512 values, and a genuinely
  ambiguous dotted path (a claim literally named `a.b` alongside a nested
  `{"a":{"b":…}}` — the same fail-closed rule CI-federation claim matching
  already uses) all reject rather than parse partially. A space- or
  comma-delimited string is never split, because that invents a parse we cannot
  verify and breaks any group whose name contains the delimiter. A sync that
  cannot be written fails the login outright: completing one against a snapshot
  we just failed to update is precisely the silent-stale case groups exist to
  remove.

  Ships migration `000045`, a `/groups` screen (catalog, local membership, and
  every scope a group reaches before you delete it), a Groups section on the
  Members screen, `janus group` with ten subcommands, 16 documented API paths,
  and a [guide](docs/guides/groups.md).
- **`janus admin reset-password` — a way back in when the last owner is locked
  out.** There was previously no recovery path at all: the documented remedy was
  to start over from an empty database, i.e. destroy every secret you own, while
  the data sat there intact and decryptable. The new command runs on the server
  host and talks straight to Postgres — deliberately **not** reachable over
  HTTP — and requires the instance's seal material first (the Shamir quorum,
  prompted with echo off, or the configured cloud KMS key). Argon2id hashing
  does not need that key; requiring it is an authority control that keeps the
  ceremony identical to unseal and stops incidental database access becoming an
  owner takeover. Sessions are revoked *before* the credential is replaced, so a
  partial failure leaves everyone logged out with the old password still
  working; a failed audit append rolls the credential back, preserving the rule
  that no credential change survives unaudited. TOTP is **not** cleared unless
  `--clear-mfa` is passed, which is audited separately. See
  [docs/guides/disaster-recovery.md](docs/guides/disaster-recovery.md).
- **`janus doctor` — 18 preflight checks for configuration that parses fine and
  is still wrong.** Prints `PASS`/`WARN`/`FAIL` per check with the concrete fix;
  `--strict` fails on warnings, `--offline` skips anything needing the network,
  `--json` for machines. It reads the environment through the *same* parse the
  server uses at boot, so it cannot drift. The headline checks are unrecognised
  `JANUS_*` variables (a typo is silently ignored today, and a misspelled name
  never errors) and whether the configured passkey origins actually describe how
  this server is reached — a mismatch fails the ceremony with no server-side
  error and reads exactly like an application bug. Secrets are scrubbed from all
  output, including third-party error text. See
  [docs/guides/troubleshooting.md](docs/guides/troubleshooting.md).

### Fixed
- **A deep link to a protected config showed it as unprotected.** The secret
  editor read the four-eyes `require_approval` flag from the registry cache
  without waiting for hydration — and a deep link is a cold load, so the lookup
  returned nothing and quietly defaulted to "unprotected". The config rendered
  with no banner, no pending-review panel, and a Save button reading "Save as
  vN", telling the operator their edit would commit when the server would in
  fact file it for approval. The server was never wrong and nothing was
  exploitable, but the UI misreported a security control, and did so more often
  the larger the instance grew (hydration is three round-trips deep). The flag
  now comes from the server, falling back to the cache only if that read fails.
- **`janus master-key rekey` could not be driven from a pipe or heredoc.** The
  share reader built a fresh `bufio.Reader` per share; bufio reads ahead, so the
  first read consumed one line and buffered the rest, then discarded them with
  the reader — the second share hit EOF. `janus unseal` takes one share per
  invocation and never hit it, which is why a master-key rotation ceremony was
  broken for scripted input.

### Tests
- **Browser coverage for the destructive and security screens.** The existing
  E2E suite covered the happy path; everything it missed was either irreversible
  or a security control — which is exactly where the Trash destroy bug lived. 26
  new tests across Trash (soft-delete → restore → destroy for config,
  environment and project), service tokens (shown once, usable as a real bearer
  credential, revocation withdraws access), members and scope confinement,
  four-eyes approvals (self-approval *and* self-rejection refused), and
  break-glass elevation. Verified to fail against the pre-fix handlers.

## [0.2.0] - 2026-07-26

### Added
- **The web UI works on a phone and a tablet.** The SPA previously had no shell
  breakpoint at all: the 236px nav took most of a phone screen and the rest was
  *clipped rather than scrolled*, so the page heading, the top bar and every
  ledger column were cut off with no way to reach them. Below 1024px the nav is
  now an off-canvas drawer (scrim, `Esc`, closes on navigation, and out of the
  tab order while shut); 1024 keeps the sidebar for tablet landscape. Wide
  ledgers scroll horizontally within their sheet rather than being cut off,
  page headers wrap instead of squeezing the title to one word per line, and the
  Audit result filter no longer pushes its `denied` segment off-screen where it
  could not be reached at all. Desktop layout is unchanged — though it also
  loses a stray horizontal scrollbar the dashboard ornament had been causing at
  every width.
- **Kubernetes service-account federation + multi-issuer trust.** The federation
  trust anchor is now a *set* of issuers (`/v1/sys/oidc/federation/issuers`), so a
  CI provider and a Kubernetes cluster can be trusted at the same time; every
  trust binding is pinned to one issuer, and the verifier is chosen by the
  token's own `iss` (signature checked against that issuer's JWKS), so a token
  from one issuer can never satisfy a binding written for another. Nested JWT
  claims are matched by dotted path (`kubernetes.io.serviceaccount.name`) with
  non-string values still dropped rather than coerced and any path collision
  rejecting the token. A `kubernetes` preset forces bindings to pin
  service-account identity (`sub`, or namespace + service-account name).
  The pre-existing single issuer and its bindings are preserved on upgrade.
- **WebAuthn passkeys for UI login.** Enrol per-device passkeys from
  **Settings → Passkeys** and sign in with one in a single step — no password,
  no TOTP code. Two ceremonies, in separate challenge pools: **email-identified**
  (the account comes from the challenge) and **passwordless/discoverable** (no
  address typed; the account is resolved from the presented credential id in
  Janus's own store, and the assertion's `userHandle` is only cross-checked
  against that owner, never used to select the account — plus conditional-
  mediation autofill where the browser supports it). Enrolment sets
  `residentKey: required` so new passkeys always work passwordlessly, and records
  the `credProps.rk` hint so Settings can report per-credential discoverability.
  `userVerification` is required on **every** ceremony, challenges are single-use
  and expiring, the signature counter is enforced as a compare-and-swap, and both
  login-begin endpoints answer identically for real, absent, and disabled
  accounts. Only public credential material is stored, so master-key rotation has
  nothing to re-wrap. Off until an operator sets `JANUS_WEBAUTHN_RP_ID` /
  `JANUS_WEBAUTHN_ORIGINS` (validated at boot, never derived from the request
  `Host`); removing the last passkey can never lock a user out. Routes under
  `/v1/auth/webauthn/*`; migrations 000040 and 000044. See
  [docs/guides/passkeys.md](docs/guides/passkeys.md).
- **Sync drift detection.** Sync was one-way: push and forget. A verification
  pass now reads the destination back and reports `missing` / `modified` /
  `extra` / `unreadable` keys — `POST /v1/sync/targets/{id}/verify`,
  `GET .../verify-runs`, `PATCH .../verify-schedule`, and `janus sync verify` /
  `verify-schedule`. Every one of the eight providers declares a read
  capability: six compare **values** (`k8s`, `gitlab`, `aws_ssm`,
  `aws_secrets`, `vercel`, `netlify`), while `github` and `cloudflare` are
  write-only at the API and so report **`names_only`** — a clean result there
  means the key names line up, not that the values match, and the API says so.
  Comparison is value-free: remote values are HMACed under the existing
  sync-fingerprint subkey and compared in constant time, never persisted or
  logged. Extras count as drift only when the target prunes. The scheduler is
  **off by default** (`JANUS_SYNC_VERIFY_TICK`, unset/`0`) because verifying
  reads values back out of external systems; manual verification always works.
  Migration 000041.
- **Secret value-version retention.** Nothing had ever removed a
  `secret_values` row, so a long-lived instance kept every value a key had ever
  held. Pruning is now explicit, audited, and done at **config-version**
  granularity (the unit of diff and rollback), so every retained version stays
  fully restorable — unreferenced value rows are garbage-collected afterwards,
  and the prune transaction re-asserts the invariant. `GET/PUT
  /v1/configs/{cid}/versions/retention` and `POST .../versions/prune`, plus
  `janus secrets retention get|set|clear` and `janus secrets prune` (with
  `--dry-run`). Gated by the new **owner-only** `secret:prune` action — the only
  operation in Janus that destroys secret history irreversibly. An instance-wide
  floor (`JANUS_SECRET_RETAIN_MIN_VERSIONS` / `JANUS_SECRET_RETAIN_MIN_DAYS`,
  both off by default) and an optional per-config override can only ever retain
  **more**, never less. Migration 000043.
- **Kubernetes service-account federation + multi-issuer trust.** The federation
  trust anchor is now a *set* of issuers (`/v1/sys/oidc/federation/issuers`), so a
  CI provider and a Kubernetes cluster can be trusted at the same time; every
  trust binding is pinned to one issuer, and the verifier is chosen by the
  token's own `iss` (signature checked against that issuer's JWKS), so a token
  from one issuer can never satisfy a binding written for another. Nested JWT
  claims are matched by dotted path (`kubernetes.io.serviceaccount.name`) with
  non-string values still dropped rather than coerced and any path collision
  rejecting the token. A `kubernetes` preset forces bindings to pin
  service-account identity (`sub`, or namespace + service-account name), joining
  the existing `github` / `gitlab` / `buildkite` / `circleci` presets.
  The pre-existing single issuer and its bindings are preserved on upgrade.
  Migration 000042.
- **Grafana dashboard + example alert rules** (`deploy/grafana/`): an import-ready
  `janus-overview.json` board and 13 Prometheus alerting rules in `alerts.yaml`
  covering sealed/down/absent, restart loops, error rate and latency, stalled
  schedulers, rotation & sync failure trends, unreapable dynamic leases, DB-pool
  exhaustion, a stalled audit head, and goroutine growth. Built only from metrics
  the server actually exports; `/metrics` still requires `JANUS_METRICS_TOKEN`.
- **SDK background lease auto-renew + `Run`-style helpers** across all three
  client SDKs: a lease renewer that keeps a dynamic credential alive in the
  background (fractional renew-ahead with jitter, a minimum interval, and
  typed stop reasons for max-TTL / lease-gone / unauthorized / rejected), plus a
  helper that issues a lease, holds it renewed for the duration of your callback,
  and revokes it on the way out — `janus.RunWithDynamic` (Go),
  `LeaseRenewer` / `withDynamic` (TypeScript), `LeaseRenewer` (Python).
- **Terraform: environment-scoped service tokens and a `janus_secrets` batch
  resource.** `janus_service_token` now accepts `environment` as well as `config`
  scope (the only two kinds Janus mints), and the new `janus_secrets` resource
  manages a whole key/value map through the batch write endpoint, so an apply
  touching N keys creates exactly **one** config version instead of N.
- **Paste-based import wizard in the web UI.** The secret editor's importer now
  recognises **Doppler**, **Vault KV v2**, and **AWS Secrets Manager** CLI export
  output alongside `.env` / Java `.properties`, with format auto-detection and an
  override. Deliberately **paste-based**: it adds no server endpoint, holds no
  third-party credential, and makes no outbound call — you run the export command
  yourself and paste the result. Leaf-flattening semantics mirror the `janus
  import` CLI sources.
- **`janus run --watch`** — supervise the child and gracefully restart it
  (SIGTERM → grace → Kill) when the bound config's version bumps, re-fetching
  secrets; poll cadence via `--watch-interval` (default `10s`).
- **`janus render`** — fill a Go `text/template` with a config's secrets and
  write it to a file (atomic, `0600`); `--watch` re-renders on
  version bumps.
- **Audit hash-chain checkpointing + retention** — owner-only
  (`audit:manage`) `POST/GET /v1/audit/checkpoint` and `POST /v1/audit/prune`.
  Signed (domain-separated HMAC) checkpoints let `verify` anchor on a trusted
  checkpoint and walk forward, so verified-and-shipped prefixes can be pruned;
  prune is fail-closed and clamped to the audit-ship high-water mark. An
  owner-only "create checkpoint" affordance is surfaced in the audit viewer.

### Fixed
- **Destroying a config or environment from the Trash always failed.** Both
  delete handlers resolved the authorization scope through a live-only read, but
  everything reachable from the Trash is by definition already soft-deleted — so
  the lookup returned `404` before the permission check ever ran, and the UI
  reported "Action failed" even for an owner. The restore handlers had always
  done this correctly; only the delete side was missed. Projects were unaffected,
  which is why it presented as config-specific. Also fixes the parent chain:
  soft-delete does not cascade, so a config whose *environment* was separately
  deleted was listed in the Trash yet could be neither restored nor destroyed.
  Authorization is unchanged in strength — only finding the row changed.
- **A failed passkey enrolment silently signed the user out.** The 401
  session-expiry handling keyed off the HTTP status alone, but enrolling a
  passkey is done by an already-authenticated user and a ceremony that fails to
  verify (wrong RP origin, a cancelled prompt, a cloned authenticator) answers
  `401`. The client now reads the error *code*, which the server already
  distinguishes, and treats only `session_expired` / `unauthenticated` as a lost
  session.
- **An expired session reported the wrong thing entirely.** Every request
  returned `401` while the shell still looked signed in, so the failure surfaced
  as a feature-level message ("Could not create the project.") that pointed
  nowhere near the cause. The UI now returns to the login gate — except on a
  wrong password, a failed passkey assertion, or the probe used to *test*
  authentication.
- **The secret editor's value field collapsed to a sliver.** The row's action
  cluster is `nowrap` and has gained a button per feature, so under the table's
  auto layout the value column absorbed the entire shortfall.
- **Unmatched `/v1/` paths returned `200` + the SPA's `index.html`.** The SPA
  fallback answered every unmatched route, so a typo'd or removed endpoint
  replied with HTML — breaking the documented error envelope and making SDKs
  parse HTML as JSON. API paths now get the standard JSON `404` (and a JSON
  `405` where chi previously sent a bodiless one), while client-side deep links
  still fall through to the SPA.
- **Clearer validation errors.** A malformed batch-secret-write body no longer
  reports "at least one change is required" (it says the body is invalid JSON),
  and "invalid scope kind" now names the two kinds a service token may use.
- **`janus --config <uuid>` was unusable.** The CLI insisted on
  project + environment + config and resolved slugs through project-level reads
  that a config-scoped token — the narrowest, CI-recommended credential —
  legitimately lacks. A config given as a UUID now short-circuits resolution.
- **`event_count` is a lifetime total.** It is carried forward across checkpoints
  and prunes rather than silently becoming a retained-row count, so the number
  `verify` reports stays comparable after a prune.
- **A prerelease no longer hijacks the `latest` image tag.** `docker_manifests`
  pushed `ghcr.io/steveokay/janus:latest` unconditionally, so an `-rc` tag became
  `latest`; `skip_push: auto` now omits the manifest for prerelease tags, matching
  the `release.prerelease: auto` behaviour already applied to the GitHub Release.

### Security
- **The SSRF guard could be defeated by an HTTP proxy.** With `HTTP_PROXY` /
  `HTTPS_PROXY` / `ALL_PROXY` set, the hardened dialer only ever saw the
  *proxy's* address, so the link-local / cloud-metadata block silently stopped
  applying to the real destination. Proxying is now **off by default** on the
  guarded clients; `JANUS_OUTBOUND_ALLOW_PROXY` re-enables it for deployments
  whose only egress path is a proxy, logging a startup warning (naming only the
  variables that are set — a proxy URL can embed credentials) and applying a
  partial URL-time host check.
- **Audit checkpoints verify the chain before signing.** Signing a tampered head
  would have laundered a detectable compromise into an undetectable one. Both
  `POST /v1/audit/checkpoint` and `POST /v1/audit/prune` now fail closed with
  `chain does not verify` unless the chain validates at that moment.
- **Prune's retention guards.** The "never prune un-shipped events" ceiling is
  now bound to **shipping history** (the durable high-water mark) rather than to
  whether the shipper happens to be wired into the running process, so removing
  `JANUS_AUDIT_SHIP_*` from a deployment that has shipped cannot quietly drop the
  protection. An optional minimum-retention floor was added on top —
  `JANUS_AUDIT_RETAIN_MIN_DAYS` and `JANUS_AUDIT_RETAIN_MIN_EVENTS`, both off by
  default, combined by taking the strictest ceiling. Unreadable state aborts the
  prune rather than pruning unguarded.
- **Systemic SSRF hardening** (`internal/nethard`): a shared hardened dialer
  re-checks the resolved IP on every dial (defeating DNS-rebinding), blocks
  link-local/cloud-metadata ranges unconditionally (optional private-range block
  via `JANUS_OUTBOUND_BLOCK_PRIVATE`), and caps redirect hops/scheme — applied to
  every operator-configured outbound caller (rotation, sync, notifications, and
  OIDC discovery/JWKS).
- **Trust & supply chain:** `SECURITY.md` disclosure policy (GitHub private
  vulnerability reporting) and `docs/threat-model.md`; releases now cosign
  keyless-signed with syft SBOMs and SLSA build-provenance attestations; a
  `.github/dependabot.yml` keeps Go/npm/pip/Actions dependencies current.
- White-box audit remediation: TOTP codes are no longer replayable; a password
  change revokes other sessions and rotates the caller's own cookie; a
  break-glass-elevated user can no longer mint a durable binding at the elevated
  role; uniform security headers on `/v1/` responses.

### Tests
- Playwright browser smoke suite (`web/tests/e2e/`): init → unseal → login →
  create project → save secret → audited reveal → chain-verified badge — since
  extended to cover the passkey ceremonies and the paste-based import wizard.
- **Go fuzz targets over the hostile-input parsers** — eight in total, across
  `internal/transit` (envelope parse + round-trip), `internal/resolve` (reference
  segment parsing), `internal/secrets` (key validation), `internal/rotation`
  (RESP encoding, asserting no command injection), and `internal/auth` (JWT
  string-claim extraction, nested-claim flattening, and fail-closed claim
  matching).
- **Schema/enum drift guard** — a test introspects the live database's `CHECK`
  constraints and asserts they accept exactly the values the code's registries
  produce, so a rotator, sync provider, or notification channel can never again
  exist in Go while being impossible to persist.
- **WebAuthn browser ceremony verified end to end** with Chrome's CDP virtual
  authenticator, so registration and both sign-in paths are exercised by a real
  browser rather than only by a synthetic authenticator in Go.

## [0.1.0] - 2026-07-24

First tagged release. Feature-complete across build Phases 1–3, plus the UI
depth, additional auth/notification/integration work, and the pre-release
security hardening that followed. (Nothing was tagged before this; everything on
`main` ships as 0.1.0.)

### Added
- **Core (Phase 1):** envelope-encryption key hierarchy with Shamir and cloud-KMS
  unseal; PostgreSQL store + migrations; Project → Environment → Config → Secret
  model with two-level (config + per-key) versioning, soft-delete/restore, config
  inheritance, and cross-config secret references; password + service-token auth;
  RBAC (viewer/developer/admin/owner); hash-chained audit log; REST API; `janus`
  CLI with `run` secret injection.
- **Transit + UI (Phase 2):** transit engine (encrypt/decrypt/sign/verify/rewrap,
  key versioning); the Svelte 5 **"Atrium"** SPA (banknote-engraving /
  archival-ledger aesthetic, `daylight`/`nightwatch` themes, hand-written CSS
  tokens — no Tailwind) covering init/unseal/login, projects → envs → configs,
  the secret editor, promotion + approvals, audit ledger, token/member
  management, transit, operations, an integrations hub, trash, and settings, with
  a command palette; OIDC login and CI federation (GitHub Actions, GitLab,
  Buildkite, CircleCI); reads-24h usage metrics.
- **Rotation + dynamic (Phase 3):** scheduled static rotation (Postgres, webhook,
  MySQL, Redis, plus external-credential-generating OAuth and AWS IAM rotators);
  sync integrations to eight providers (GitHub Actions, Kubernetes, GitLab CI,
  Cloudflare, Vercel, Netlify, AWS SSM, AWS Secrets Manager); dynamic Postgres
  credentials with a TTL/renewal/revocation lease manager.
- **Hardening & depth:** project-KEK and master-key rotation; cursor pagination;
  Idempotency-Key middleware; HTTP timeouts/body caps; trash/restore, per-key
  history, and audit expand/timeline UI; a self-sufficient CLI control plane
  (project/env/config CRUD, token mint/list/revoke, whoami, completion, diff);
  break-glass emergency role elevation; protected configs (require-approval /
  four-eyes edit requests); env→env promotion with an approval workflow and
  locked keys.
- **Auth & accounts:** two-factor authentication (TOTP + single-use recovery
  codes); progressive account lockout; self-service session management; per-token
  IP allowlists.
- **Editor & search:** bulk import from `.env` / Java `.properties` with preview;
  confirm-gated **Download .env** export; typed secrets
  (value/password/json/ssh_key/certificate/note); a client-side value generator;
  filename-style secret keys; global secret-key-name search from the command
  palette; environment rename on the project board.
- **Notifications & observability:** outbound webhook / Slack / SMTP alerting on
  rotation & sync failures, denials, and pending approvals; a Prometheus
  `/metrics` endpoint and an admin health panel; audit shipping to a
  webhook/syslog SIEM; configurable log level/format.
- **Integrations & SDKs:** Go, TypeScript, and Python client SDKs and a Terraform
  provider; inbound importers for Doppler, Vault, and AWS Secrets Manager;
  scheduled encrypted S3-compatible backups with retention.
- **Release:** Apache-2.0 license; OpenAPI 3.1 spec; goreleaser multi-arch
  binaries + GHCR image; production deployment guide.

### Security
- Closed two require-approval (four-eyes) bypasses where **rollback** and
  **promote-apply** committed directly to protected configs; both now route the
  resulting changeset through the edit-request approval flow. Made edit-request
  approval **claim-before-commit** to prevent a double-commit race under
  concurrent approvers, added **GitLab sync project-id validation** (request-URL
  injection guard), and protected pending edit requests from **KEK-version
  retirement** during project-KEK rotation.

[Unreleased]: https://github.com/steveokay/janus-secrets/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/steveokay/janus-secrets/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/steveokay/janus-secrets/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/steveokay/janus-secrets/releases/tag/v0.1.0
