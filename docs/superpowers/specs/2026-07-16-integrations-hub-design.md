# Integrations hub — design

**Status:** approved (brainstorm 2026-07-16)
**Tracker:** closes `gaps.md` §1.15
**Scope:** frontend-only. No backend, no new endpoints, no moved code.

## Overview

"Connect Janus to an external system" is currently scattered across two
screens: GitHub Actions **sync** and Kubernetes **sync** live under
`/operations` (Sync tab); the OIDC **login** provider and GitHub Actions
**CI federation** live under `/settings`. There is no single place that
answers "what can I connect Janus to, and what's connected right now?"

This feature adds a new `/integrations` page: a fixed **catalog of
external-system connector cards** that show best-effort status and
**deep-link out** to the existing configuration surfaces. The card is a
signpost, not a new config surface. Operations remains the run/monitor
data-plane; Settings keeps OIDC/federation configuration. The win is
discoverability and a coherent mental model, built entirely on endpoints
that already exist.

## Goals

- One discoverable home listing every external-system connector.
- At-a-glance status per connector (how many sync targets, whether OIDC /
  federation is enabled) without navigating away.
- Deep-links that land the user on the exact existing screen/tab/section
  that configures each connector.
- Zero backend change; robust for users who lack permission on some or
  all connectors.

## Non-goals (YAGNI — explicitly out of scope)

- Inline create/edit of any sync target, OIDC provider, or federation
  binding. (Configuration stays where it is today.)
- Moving OIDC / CI-federation config out of Settings, or sync config out
  of Operations. (A possible later phase; not specced here.)
- Live status polling, connection health checks, or surfacing
  rotation/sync **failures** in the hub.
- Any new backend endpoint or API change.
- Rotation and dynamic-secrets cards — those are internal engines
  surfaced in Operations, not external-system connectors.

## The catalog (grouped by external system)

Three cards, always rendered (a fixed catalog — not derived from what
exists or from the viewer's permissions).

| Card | Status line(s) | Deep-link action(s) |
|---|---|---|
| **GitHub** | `Actions sync: N` · `CI federation: on/off` | `Sync →` `/operations?tab=sync` · `Federation →` `/settings?section=federation` |
| **Kubernetes** | `Sync targets: N` | `Manage →` `/operations?tab=sync` |
| **OIDC (SSO login)** | `Login: enabled/disabled` | `Configure →` `/settings?section=oidc` |

GitHub is one card covering both of its uses (secret sync **and** CI
federation) because that matches how users think ("I want to connect
GitHub"); it carries two actions.

## Status data sources (all best-effort, 403/404 → neutral)

Every status fetch is best-effort. A rejected promise (403 no-permission,
404 not-configured, network error) or an empty result resolves to a
**neutral state** (`—` for counts, "not set up" for on/off), never an
error screen. This mirrors the existing 403-tolerant Operations console
fan-out.

| Datum | Source (existing) | Notes |
|---|---|---|
| GitHub / k8s sync counts | `useSync('all')` from `web/src/operations/useAggregated.ts` → `Aggregated<SyncView>`, `SyncView.provider: 'github' \| 'k8s'` | Already a cross-project, 403-tolerant fan-out. Count by `provider`. |
| CI federation on/off | `endpoints.getFederationConfig()` → `{ issuer, audience, enabled }` | 200 → `enabled`; 404/403 → neutral. |
| OIDC login on/off | `endpoints.oidcLoginStatus()` → `{ enabled, name? }` via `/v1/auth/oidc/status` | **Unauthenticated** probe — no 403 for any user. Preferred over the admin `getOIDCConfig()` for status. |

## Card states

Each card renders one of three visual states per status datum, driven
only by the best-effort result:

- **Loading** — a token skeleton in the status line while the query is
  in flight.
- **Neutral** — query rejected/empty/not-configured: `—` or "not set
  up". The deep-link action is still enabled (the target page enforces
  authz and shows the real thing or a permission message).
- **Populated** — real count / `enabled|disabled` badge.

The card itself and its deep-links are **always** present regardless of
state — the catalog is a fixed signpost.

## Components & files

New directory `web/src/integrations/`:

- `IntegrationsPage.tsx` — the `/integrations` route. Renders a
  responsive grid of the three cards. Owns the status queries (via the
  hook below) and passes resolved status into each card.
- `ConnectorCard.tsx` — presentational only: icon, title, one-line
  description, one or more status lines, and one or more deep-link
  actions. No data fetching, no business logic — takes props, renders
  Nocturne-token markup. Independently testable.
- `useIntegrationStatus.ts` — a thin hook that composes the **existing**
  data sources: `useSync('all')` (reused from the Operations console) for
  per-provider counts, and TanStack queries wrapping
  `endpoints.getFederationConfig` / `endpoints.oidcLoginStatus`. Returns a
  typed, already-neutralised status object (`{ githubSync, k8sSync,
  federation, oidcLogin }`) so the page/card never handle raw errors.

Reuse, do not duplicate: the sync aggregation lives in
`operations/useAggregated.ts` and is imported, not re-implemented.

## Wiring

- **Route:** add `<Route path="/integrations" element={<IntegrationsPage />} />`
  to `web/src/App.tsx`.
- **Sidebar:** add an item to `web/src/shell/Sidebar.tsx`, placed **just
  above Operations**, always shown (same visibility model as
  Operations/Settings — no per-role gating on the nav item). Proposed
  lucide icon: `Blocks` (fallback `Puzzle`), pending the visual check.
- **Command palette:** add a "Go to Integrations" navigation command to
  `web/src/palette/` (names/navigation only — consistent with the
  existing palette's no-secrets rule).

## Deep-link contract (verified against current code)

- Operations reads `?tab=` via `useSearchParams`; valid ids are
  `rotation | sync | dynamic`. → `/operations?tab=sync`.
- Settings reads `?section=`; valid keys are
  `instance | oidc | federation | appearance`. →
  `/settings?section=oidc`, `/settings?section=federation`.

These are pre-existing behaviours; the hub only constructs the URLs. If a
target page's param handling changes, a hub test (below) will catch the
drift.

## Visual system

Nocturne tokens only — no raw palette classes, no `dark:` variants, no
hex (per `CLAUDE.md` "Web UI visual system" and the
`no-raw-palette` test). Cards compose from the existing kit
(`Card`, `Button`/link, `Skeleton`, status `Pill`). Must render correctly
in both light and dark themes. Env-colour semantics do not apply here
(these are connectors, not environments); status badges use the semantic
green/neutral tokens only.

## Testing

Component/integration tests under `web/src/integrations/`:

1. **Admin, populated:** with mocked sync targets (2 github, 1 k8s),
   federation `enabled: true`, and OIDC login `enabled: true`, all three
   cards render with the correct counts/badges.
2. **Limited-permission user (403-tolerance):** sync fan-out and
   federation config reject with 403; all three cards **still render**
   with neutral status, and every deep-link is present and enabled.
3. **Deep-link correctness:** each action links to the exact expected
   route+param (`/operations?tab=sync`, `/settings?section=federation`,
   `/settings?section=oidc`).
4. **Loading:** status lines show skeletons while queries are in flight.
5. Dual-theme **smoke** (`npm run smoke`) includes the new route.

msw mocks must mirror the real wire shapes of `useSync`,
`getFederationConfig`, and `oidcLoginStatus` (the standing mock-drift
rule).

## Risks & mitigations

- **Status/target-page drift:** if Operations tab ids or Settings section
  keys change, deep-links silently break. Mitigated by test #3 asserting
  the exact URLs.
- **Empty-catalog perception:** a viewer sees three cards all reading
  `—`, which could look broken. Mitigated by each card's one-line
  description making its purpose clear even at neutral status, and the
  action verbs ("Sync →", "Configure →") signalling it's reachable.

## Future phase (noted, not specced)

If the team later wants the hub to **own** configuration, a follow-up spec
would migrate sync-target create/edit and OIDC/federation config into
`/integrations`, reducing Operations to run/monitor and Settings to
instance/appearance. This design deliberately does not build toward that
beyond keeping the connector taxonomy stable.
