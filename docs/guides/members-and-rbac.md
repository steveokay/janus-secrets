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
union).

The **Role at \<scope\>** column shows what the server will actually allow,
including anything held through a group, and **Source** says where it came
from (`direct`, `via <group>`, or both). The dropdown and Remove only ever
touch the **direct** binding — a group-derived role is changed on the group,
so those rows link to Groups instead. Example: `nadia` as *developer* on `atlas-api` and nothing else —
she can work every atlas config but can't see other projects or touch
instance settings.

## Groups — one binding for a whole team

Repeating the same grant for every person on every project is what groups
remove. Bind a **group** at a scope and everyone in it holds that role,
including people who join later. Group bindings union with direct ones exactly
as two direct bindings do — no precedence, no deny rules.

Membership comes either from your identity provider's OIDC group claim
(`oidc` groups) or from a list you manage here (`local` groups, which also
cover password logins). A group can hold viewer, developer or admin — never
owner. Full detail in the [groups guide](groups.md).

## "Who can write prod?" — the Access review screen

Members answers *who is bound here*. That is the wrong shape for the question
an org actually asks, because permissions are the **union** of every applicable
binding with no deny rules: an environment with no binding of its own still has
people who can write it — through the project, through the instance, through a
group. Looking at one scope at a time hides that completely.

**Access review** (`g` `v`, or the rail) is a grid of people × scopes. A cell
is the *effective* role there, with a short note saying what produced it
(`instance`, `project`, `env`, `group`, or a combination); hover for the full
list of contributing bindings. The instance row, each project and each of that
project's environments are columns, in inheritance order.

It is deliberately gated on `member:read` — the same right as Members — because
the endpoints behind it authorize **per scope**. A project admin therefore gets
a real answer for their own project without being shown anything else.

**Every answer says how complete it is.** A caller who cannot read
instance-scoped bindings does not see them at all — a scope you cannot see is
not shown to you — so the screen states plainly that someone bound at instance
level reaches everything below and will not appear. Very large instances are
bounded rather than left to time out, and any bound that bites is named
(`projects`, `environments`, `bindings`, `people`, `cells`). Narrowing to one
project usually gives a complete answer for it. A partial access review that
reads as complete is worse than none, so the screen never implies one.

## Offboarding: what can this person reach, and revoking it

Click a person in the review to see every binding they hold across scopes, split
by source, plus any live break-glass elevation. **Revoke all direct bindings**
then clears them in one action instead of hunting through instance, project and
environment rows.

It is honest about its limits, and the result lists each one:

- It removes **direct bindings only**, and only at scopes you may manage.
- It **cannot remove group-derived access** — that grant lives on the group
  binding, or in your identity provider. For an IdP-fed group, removing the
  person there is the single action that clears it everywhere.
- It **does not revoke an active break-glass grant**. Those are time-boxed and
  revoked from [Break-glass](break-glass.md).
- It **does not disable the account** — that is *Members → Disable*
  (`POST /v1/users/{id}/disable`), a separate authority.
- It refuses outright, removing nothing, if it would take the **last instance
  owner**.
- A binding at a role **above your own durable role** is left in place and
  reported, measured against your bound role rather than your effective one —
  so a break-glass elevation cannot be spent sweeping away bindings above the
  role you actually hold.

The result is marked *complete* only when nothing was skipped, nothing
group-derived remains, no break-glass grant is live, and nothing was hidden
from you. Each revocation writes an ordinary `member.revoke` audit event at its
exact scope — so a bulk offboarding reads in the ledger exactly like the manual
revocations it replaces — plus one `member.revoke_all` summary.

API: `GET /v1/access/matrix`, `GET /v1/access/users/{uid}`,
`POST /v1/access/users/{uid}/revoke-all` (all accept `?project=<id>`).

## Guardrails the server enforces

- **Delegation ceiling** — you can't grant a role above your own. Measured
  against your *durable* role, so a break-glass elevation can never be turned
  into a lasting binding — for users and for groups alike.
- **A group can never be granted owner** — refused by the API and by the
  database, so every instance owner stays a direct binding.
- **Never-lock-out** — the last instance owner can't be removed, demoted, or
  disabled (marked in the UI).
- **Four-eyes on promotions** — approving your own promotion request is
  refused regardless of role.
- Denied requests return a generic 403 and are recorded in the audit ledger
  (visible in the Overview in-tray and `/audit` with the `denied` filter).

Service tokens are separate from members: they get least-privilege
config/environment/transit scopes and can never perform management actions —
see [Service tokens](service-tokens.md).

## What each person sees

The web UI hides what your account cannot use. The navigation rail, the command
palette (`ctrl`+`K`) and the `g`-chord shortcuts all render from the same list,
filtered by the permissions `GET /v1/auth/me` reports — so a developer with no
instance-level rights does not see Transit, Groups or Notification channels, and
someone bound to no project does not see Projects.

That filtering is **presentation only**. The server authorizes every request
independently and is the sole authority: a hidden screen is still reachable by
typing its URL, where it behaves exactly as it always did — usually a 403. The
point is not to add a control, it is to stop people discovering their
permissions by collecting refusals.

The response splits permissions in two, because *where* a permission holds
decides what it justifies showing:

- `instance` — what you hold against the instance itself. This gates
  instance-scoped screens: Transit, the group catalog, user management,
  notification channels.
- `anywhere` — what you hold at instance scope **or** on any single project or
  environment you are bound to. This gates project-shaped screens: Projects,
  Audit, Members, Approvals, Trash.

The distinction is not cosmetic. A project viewer holds `secret:read` and
`transit:read` *inside their project*, but the transit endpoints authorize at
instance scope — so gating Transit on `anywhere` would show a screen that
immediately refuses. An active [break-glass](break-glass.md) grant is included
while it lasts, so an elevation makes the screens it unlocked appear.

Two screens are never hidden: **Settings** (your own password, TOTP, passkeys
and sessions) and **Break-glass** (elevation is self-service; the server decides
whether the role you ask for is above the one you hold).

## Last login

Every user carries a `last_login_at` timestamp: the most recent successful login
(password or OIDC session). It is **value-free** (a timestamp only) and shown as
a *Last login* column on the Members page — relative (e.g. "2d ago"), or
**never** for a member who has been invited but has not yet signed in. The field
is exposed on `GET /v1/users` as `last_login_at` (`null` until the first login).
Use it to spot dormant accounts that may be candidates for disabling.

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
