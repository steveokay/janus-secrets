/* The one list of places you can go.
 *
 * The shell rail and the command palette both render from this. They used to
 * keep separate lists of the same destinations, which is fine until they are
 * permission-gated — then a hidden nav item stays reachable through the palette
 * and the gating means nothing. One list, one gate.
 *
 * The gate is a HINT. The server authorizes every request regardless, so a
 * hidden destination is still reachable by typing the URL and will simply
 * behave as it always did. What this buys is that a non-admin stops discovering
 * their permissions by collecting 403s. */

import type { Permission, Permissions } from './api'

export type Section = 'Registry' | 'Instruments' | 'Record' | 'Office'

export interface Destination {
  /** Two-letter folio code shown on the rail. */
  code: string
  label: string
  href: string
  section: Section
  /** Extra search terms for the command palette. */
  keywords: string
  /** Second key of the `g`-chord that jumps here, if it has one. */
  chord?: string
  /** What it takes to see this. Omitted = always visible. */
  gate?:
    /** Held against the instance-scoped resource. */
    | { at: 'instance'; needs: Permission }
    /** Held at instance scope or on any one project/environment. */
    | { at: 'anywhere'; needs: Permission | readonly Permission[] }
}

/* Each gate mirrors what the handlers behind that screen actually require —
 * see the `authorize` calls in internal/api. A gate that is stricter than the
 * handler hides a working feature; one that is looser is just the 403 again. */
export const DESTINATIONS: readonly Destination[] = [
  { code: 'OV', label: 'Overview', href: '/', chord: 'h', section: 'Registry',
    keywords: 'home dashboard overview' },
  { code: 'PR', label: 'Projects', href: '/projects', chord: 'p', section: 'Registry',
    keywords: 'projects registry dossiers', gate: { at: 'anywhere', needs: 'project:read' } },

  // Transit is instance-scoped: a project viewer holds transit:read INSIDE
  // their project, which is exactly the false positive `instance` exists for.
  { code: 'TR', label: 'Transit', href: '/transit', chord: 't', section: 'Instruments',
    keywords: 'transit encrypt sign kms keys', gate: { at: 'instance', needs: 'transit:read' } },
  { code: 'OP', label: 'Operations', href: '/operations', chord: 'o', section: 'Instruments',
    keywords: 'operations rotation sync dynamic leases',
    gate: { at: 'anywhere', needs: ['rotation:manage', 'sync:manage', 'dynamic:manage', 'dynamic:issue'] } },
  // A static connector catalogue with no data behind it — nothing to gate.
  { code: 'IN', label: 'Integrations', href: '/integrations', chord: 'i', section: 'Instruments',
    keywords: 'integrations oidc sso federation github kubernetes' },

  { code: 'AU', label: 'Audit ledger', href: '/audit', chord: 'a', section: 'Record',
    keywords: 'audit activity log events record chain', gate: { at: 'anywhere', needs: 'audit:read' } },
  { code: 'AP', label: 'Approvals', href: '/approvals', chord: 'r', section: 'Record',
    keywords: 'approvals promotion requests review four eyes',
    gate: { at: 'anywhere', needs: ['promotion:request', 'promotion:manage', 'secret:promote'] } },
  { code: 'CD', label: 'Cross-env diff', href: '/compare', section: 'Record',
    keywords: 'compare diff cross env config staging prod difference values masked',
    gate: { at: 'anywhere', needs: 'secret:read' } },

  { code: 'TK', label: 'Service tokens', href: '/tokens', chord: 'k', section: 'Office',
    keywords: 'tokens service machine api', gate: { at: 'anywhere', needs: 'token:read' } },
  { code: 'MB', label: 'Members', href: '/members', chord: 'm', section: 'Office',
    keywords: 'members users roles rbac team', gate: { at: 'anywhere', needs: 'member:read' } },
  // Same gate as Members, and deliberately so: the endpoints behind it
  // authorize `member:read` PER SCOPE from one batch load, and a caller bound
  // to a single project gets a real (if partial) answer there. Gating it on
  // `instance` would hide the cross-scope view from exactly the project admins
  // who have to run an access review on their own team.
  { code: 'AX', label: 'Access review', href: '/access', chord: 'v', section: 'Office',
    keywords: 'access review matrix grid cross scope who can write prod offboarding revoke union',
    gate: { at: 'anywhere', needs: 'member:read' } },
  { code: 'GR', label: 'Groups', href: '/groups', chord: 'g', section: 'Office',
    keywords: 'groups teams oidc idp membership', gate: { at: 'instance', needs: 'group:manage' } },
  // Break-glass is self-service elevation: the server decides whether the
  // requested role is above the one you hold. Everyone may ask.
  { code: 'BG', label: 'Break-glass', href: '/break-glass', section: 'Office',
    keywords: 'break glass emergency access elevation escalation incident' },
  { code: 'NT', label: 'Notifications', href: '/notifications', chord: 'n', section: 'Office',
    keywords: 'notifications alerts webhook slack channels',
    gate: { at: 'instance', needs: 'notification:manage' } },
  // Your own account: password, TOTP, passkeys, sessions.
  { code: 'ST', label: 'Settings', href: '/settings', chord: 's', section: 'Office',
    keywords: 'settings master key backup password passkey totp' },
  // Trash lists only what you could restore, so with none of these it is empty.
  { code: 'TS', label: 'Trash', href: '/trash', chord: 'x', section: 'Office',
    keywords: 'trash deleted restore bin',
    gate: { at: 'anywhere', needs: ['config:delete', 'env:delete', 'project:delete'] } },
]

/** Is this destination worth offering to a principal holding `perms`?
 *
 * `perms` undefined means the server sent no hint (an older build, or a
 * response from before the field existed). That yields "show everything":
 * degrading to the previous behaviour costs a 403 on a click, where degrading
 * to "show nothing" would leave a signed-in owner staring at an empty rail.
 *
 * Pure on purpose — no store import — so the gating can be unit-tested without
 * a Svelte runtime, which is the only place these rules are checkable at all.
 */
export function visibleFor(d: Destination, perms: Permissions | undefined): boolean {
  if (!d.gate) return true
  const needs = d.gate.needs
  return Array.isArray(needs)
    ? needs.some(n => holds(perms, d.gate!.at, n))
    : holds(perms, d.gate.at, needs as Permission)
}

/** Does `perms` carry `p` at the given scope? Same undefined-means-yes rule as
    visibleFor. Exported for the handful of in-shell CONTROLS that are gated the
    same way a destination is — "Seal server" needs instance `sys:seal`, and
    offering it to someone who will be refused is the same discover-by-403 the
    nav gating removes. */
export function holds(
  perms: Permissions | undefined,
  at: 'instance' | 'anywhere',
  p: Permission,
): boolean {
  if (!perms) return true
  return (at === 'instance' ? perms.instance : perms.anywhere).includes(p)
}

/** The `g`-chord table, filtered the same way. The help modal must not teach a
    chord that lands on a screen the rail is hiding — and pressing it should do
    nothing rather than navigate somewhere the account cannot use. */
export function chordsFor(perms: Permissions | undefined): Array<{ keys: string; label: string; to: string }> {
  return DESTINATIONS
    .filter(d => d.chord && visibleFor(d, perms))
    .map(d => ({ keys: d.chord as string, label: d.label, to: d.href }))
}

/** The visible destinations, grouped in rail order. Sections that end up empty
    are dropped, so the rail never shows a heading over nothing. */
export function sectionsFor(perms: Permissions | undefined): Array<{ title: Section; items: Destination[] }> {
  const order: Section[] = ['Registry', 'Instruments', 'Record', 'Office']
  return order
    .map(title => ({ title, items: DESTINATIONS.filter(d => d.section === title && visibleFor(d, perms)) }))
    .filter(s => s.items.length > 0)
}
