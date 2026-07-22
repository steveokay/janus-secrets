# How-to: members and role bindings in the web UI

Reference for the model itself: deny-by-default RBAC with four roles
(viewer ⊂ developer ⊂ admin ⊂ owner), bindings at **instance**, **project**,
or **environment** scope, top-down inheritance, most-permissive union.

## Invite someone

**Members → + Invite member** → email. The account is created with a
**one-time password shown exactly once** — hand it over out of band; they
should change it under Settings → Account on first login.

A fresh user has **no access** (deny by default) until you bind a role.

## Bind roles at the right scope

The scope switcher at the top of Members selects where bindings apply:

- **instance** — applies everywhere (owners/admins live here)
- **project** — pick a project; the binding covers all its environments and
  configs
- **environment** — pick project + environment; the narrowest human scope

Choose a role in the row's dropdown to bind; **Remove** deletes that
binding (the user keeps bindings at other scopes — effective access is the
union). Example: `nadia` as *developer* on `atlas-api` and nothing else —
she can work every atlas config but can't see other projects or touch
instance settings.

## Guardrails the server enforces

- **Delegation ceiling** — you can't grant a role above your own.
- **Never-lock-out** — the last instance owner can't be removed, demoted, or
  disabled (marked in the UI).
- **Four-eyes on promotions** — approving your own promotion request is
  refused regardless of role.
- Denied requests return a generic 403 and are recorded in the audit ledger
  (visible in the Overview in-tray and `/audit` with the `denied` filter).

Service tokens are separate from members: they get least-privilege
config/environment/transit scopes and can never perform management actions —
see [Service tokens](service-tokens.md).

## Account lockout and unlocking

To blunt password brute-forcing, Janus locks an account after a run of failed
password logins (`JANUS_LOCKOUT_THRESHOLD`, default 5) and holds it locked for a
window that escalates with each successive lockout (`1m → 5m → 25m → 1h`,
capped). The lock **auto-expires** — no action is needed to recover — and the
counter resets on the next successful login. A locked account is shown with a
**Locked** badge on the Members page. See the `JANUS_LOCKOUT_*` variables in
[production deployment](production-deployment.md) to tune or disable this.

To clear a lock early (a locked-out colleague who can't wait for the window), an
admin unlocks from the Members page (the **Unlock** action on the row) or via
`POST /v1/users/{id}/unlock`. Unlocking requires the `user:manage` permission at
instance scope — the same right as disabling a user — you cannot unlock
yourself, and every unlock is recorded in the audit ledger.

Lockout complements the per-IP login rate limit and the manual **disable** (a
deliberate, admin-set block that does not auto-expire): lockout is automatic and
temporary; disable is manual and sticky. This is distinct from
[two-factor authentication](two-factor-auth.md), which adds a second credential
rather than throttling a first one.
