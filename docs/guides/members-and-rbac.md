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
