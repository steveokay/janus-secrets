/**
 * Unit tests for the nav permission gates in src/lib/nav.ts.
 *
 * Zero dependencies: Node's built-in test runner, with native TypeScript type
 * stripping handling the `.ts` import (Node >= 22.18 / 24). nav.ts is kept free
 * of store imports precisely so it can be exercised here — a rune module would
 * need a Svelte runtime and these rules would go untested.
 *
 * What can go wrong, in both directions:
 *   - a gate stricter than the handler hides a feature the account can use,
 *     and there is no 403 to explain the absence — the screen simply is not there
 *   - a gate looser than the handler shows a screen that 403s, which is the
 *     behaviour this whole feature exists to remove
 */
import test from 'node:test'
import assert from 'node:assert/strict'

import { DESTINATIONS, visibleFor, chordsFor, sectionsFor } from '../../src/lib/nav.ts'
import type { Permission, Permissions } from '../../src/lib/api.ts'

const perms = (instance: Permission[], anywhere: Permission[]): Permissions => ({
  instance,
  anywhere: [...new Set([...instance, ...anywhere])],
})

const at = (href: string) => {
  const d = DESTINATIONS.find(x => x.href === href)
  assert.ok(d, `no destination for ${href}`)
  return d
}

test('a server that sends no permissions shows everything', () => {
  // The degrade direction matters: showing everything costs a 403 on a click,
  // while showing nothing would leave a signed-in owner with an empty rail and
  // no way to navigate out of it.
  for (const d of DESTINATIONS) assert.equal(visibleFor(d, undefined), true, d.label)
})

test('a principal with no permissions still keeps the ungated destinations', () => {
  const none = perms([], [])
  assert.equal(visibleFor(at('/settings'), none), true, 'own account is always reachable')
  assert.equal(visibleFor(at('/break-glass'), none), true, 'elevation is self-service')
  assert.equal(visibleFor(at('/'), none), true)
  assert.equal(visibleFor(at('/projects'), none), false)
  assert.equal(visibleFor(at('/transit'), none), false)
})

test('instance-scoped gates ignore project-scoped reach', () => {
  // The exact false positive the instance/anywhere split exists for: a project
  // viewer holds transit:read INSIDE their project, but the transit endpoints
  // authorize at instance scope, so showing Transit would just 403.
  const projectViewer = perms([], ['secret:read', 'config:read', 'project:read', 'member:read', 'transit:read'])
  assert.equal(visibleFor(at('/transit'), projectViewer), false, 'Transit is instance-scoped')
  assert.equal(visibleFor(at('/groups'), projectViewer), false, 'the group catalog is instance-scoped')
  assert.equal(visibleFor(at('/projects'), projectViewer), true)
  assert.equal(visibleFor(at('/compare'), projectViewer), true)
  assert.equal(visibleFor(at('/members'), projectViewer), true)
})

test('the access review follows member:read, not instance', () => {
  // The endpoints behind it authorize member:read PER SCOPE, so a caller bound
  // to one project gets a real (if partial) answer. Gating it on `instance`
  // would hide the cross-scope view from exactly the project admins who have to
  // run an access review on their own team — and gating it looser than
  // member:read would show a screen that 403s.
  assert.equal(visibleFor(at('/access'), perms([], ['member:read'])), true)
  assert.equal(visibleFor(at('/access'), perms([], ['secret:read', 'project:read'])), false)
  assert.equal(visibleFor(at('/access'), perms([], [])), false)
})

test('an instance admin sees the instance-scoped screens', () => {
  const admin = perms(['transit:read', 'group:manage', 'notification:manage', 'audit:read'], [])
  assert.equal(visibleFor(at('/transit'), admin), true)
  assert.equal(visibleFor(at('/groups'), admin), true)
  assert.equal(visibleFor(at('/notifications'), admin), true)
  assert.equal(visibleFor(at('/audit'), admin), true)
})

test('an any-of gate needs only one of its permissions', () => {
  const syncOnly = perms([], ['sync:manage'])
  assert.equal(visibleFor(at('/operations'), syncOnly), true, 'sync alone justifies Operations')
  const issueOnly = perms([], ['dynamic:issue'])
  assert.equal(visibleFor(at('/operations'), issueOnly), true, 'issuing leases does too')
  assert.equal(visibleFor(at('/operations'), perms([], ['secret:read'])), false)
})

test('sections with nothing visible are dropped entirely', () => {
  // A heading over an empty list reads as a broken page, not a hidden feature.
  const sections = sectionsFor(perms([], []))
  for (const s of sections) assert.ok(s.items.length > 0, `${s.title} rendered empty`)
  assert.ok(!sections.some(s => s.title === 'Registry' && s.items.some(i => i.href === '/projects')))
})

test('chords never point at a hidden destination', () => {
  // The g-chords are a third way into every screen. If they were not filtered
  // with the rail, the gating would be decorative.
  const projectViewer = perms([], ['project:read', 'secret:read'])
  const reachable = new Set(chordsFor(projectViewer).map(c => c.to))
  assert.ok(!reachable.has('/transit'), 'g-t must not jump to a hidden Transit')
  assert.ok(!reachable.has('/groups'), 'g-g must not jump to a hidden Groups')
  assert.ok(reachable.has('/projects'))
  for (const c of chordsFor(projectViewer)) {
    assert.equal(visibleFor(at(c.to), projectViewer), true, `chord ${c.keys} → hidden ${c.to}`)
  }
})

test('the destination table itself is well formed', () => {
  const hrefs = new Set<string>()
  const codes = new Set<string>()
  const chords = new Set<string>()
  for (const d of DESTINATIONS) {
    assert.ok(!hrefs.has(d.href), `duplicate href ${d.href}`)
    assert.ok(!codes.has(d.code), `duplicate rail code ${d.code}`)
    hrefs.add(d.href)
    codes.add(d.code)
    if (d.chord) {
      assert.ok(!chords.has(d.chord), `duplicate chord g-${d.chord}`)
      chords.add(d.chord)
    }
  }
})
