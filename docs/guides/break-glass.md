# Break-glass access (emergency role elevation)

**Break-glass** is the paved, loud path for emergencies: when you need more
access *right now* than your standing role gives you, you elevate your own role
on a scope for a bounded time — instead of keeping standing admin, or worse,
sharing a root credential. Every activation is stamped into the audit chain and
forwarded to your notification channels, so nobody breaks the glass quietly.

The design goal is the opposite of a back door. Break-glass is:

- **Guarded, not a bypass.** You can only elevate on a scope you *already hold a
  role on*, and only to a *higher* role. It never grants access to something you
  couldn't already see.
- **Time-boxed.** Every grant carries a TTL, clamped to a server maximum, and
  auto-expires. There is no "forgot to turn it off" — expiry is enforced by
  timestamp on every request.
- **Loud.** Activation emits the loudest audit event in the system and alerts
  humans through notifications.

## When to use it

Reach for break-glass when a real incident needs access your day-to-day role
doesn't have — a developer who must manage members during an outage, an admin
who needs owner-only project actions to unblock a fix, and so on. It is *not* a
substitute for correct standing RBAC. If you find yourself breaking the glass
routinely for the same task, that is a signal to adjust the underlying role
binding instead.

## The guard rules

Activation is deny-by-default and enforced at the API boundary:

1. **You must already hold a role on the exact scope.** If you have no role
   binding on the instance / project / environment you name, activation is
   rejected with `403`. Break-glass raises an existing role; it never conjures
   one from nothing.
2. **The target role must be strictly higher than your held role** on that
   scope, and no higher than `owner`. Elevating to a role you already have (or
   below) is a no-op and rejected with `400`.
3. **A reason is mandatory** — a non-empty, operator-entered justification. It
   is stored and included in the audit event and the notification payload. The
   reason is plain text describing *why*; it is never a secret value.
4. **Service tokens cannot activate break-glass.** Only interactive user
   accounts (which hold role bindings) can elevate.

The role hierarchy is the usual one: `viewer` ⊂ `developer` ⊂ `admin` ⊂ `owner`.

## TTL and expiry

Each request carries a `ttl` (a Go duration such as `30m`). The server clamps it
to `JANUS_BREAKGLASS_MAX_TTL` (default `1h`); a request for longer is silently
capped, and an absent/invalid TTL uses the maximum. A grant stops applying the
instant it expires — the RBAC overlay checks the expiry timestamp on every
request, so no background job is required. A boot-time sweep additionally marks
grants that lapsed while the server was down and records a `breakglass.expire`
audit event for each.

## How the elevation works

Break-glass overlays your **effective role** for a scope: it is the *maximum* of
your bound role and any active, non-expired grant on that same scope. The grant
is scoped exactly like a role binding — a project grant never leaks to a sibling
environment or another project. When the grant expires or is revoked, your
effective role drops straight back to your bound role.

## Activating from the web UI

Open **Break-glass** from the sidebar (loud vermilion treatment — it is meant to
stand out). Choose the scope (instance, or a project / environment you hold a
role on), the role to elevate to, a TTL, and a reason. Break the glass. The
active-grants list below shows a live countdown to expiry and an **End** control
to revoke early. Active grants also surface at the top of the **Overview**
in-tray so the whole team can see when the glass is broken.

## Activating from the CLI

```
# Elevate to admin on a project for 30 minutes
janus break-glass activate \
  --scope project --project <project-id> \
  --role admin --reason "prod incident 1234" --ttl 30m

# List your active grants (admins see everyone's)
janus break-glass list

# End a grant early
janus break-glass revoke <grant-id>
```

## Revoking

A grant ends when its TTL expires, or earlier when someone revokes it. **The
grant's owner** can end their own grant; an **instance admin** (`member:manage`)
can end anyone's. Use the **End** button in the UI, `janus break-glass revoke`,
or `DELETE /v1/break-glass/{id}`. Revoking a grant that has already expired or
been revoked returns `409` (there is nothing live to end).

## Audit and notifications

Break-glass is deliberately the loudest thing in the audit log:

- `breakglass.activate` — on every activation, recording the actor, target
  scope, elevated role, `expires_at`, and the reason. The activate audit is
  **fail-closed**: if the audit write fails, the grant is rolled back so no
  elevation ever persists unaudited.
- `breakglass.revoke` — when a grant is ended early.
- `breakglass.expire` — emitted by the boot-time sweep for grants that lapsed
  while the server was down.

Subscribe a notification channel to the **`breakglass.activated`** event kind
(Notifications screen, or `janus notifications create --events breakglass.activated,…`)
so activations page a human immediately. Like every Janus notification, the
payload is derived from the value-free audit log and can never carry a secret.

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `JANUS_BREAKGLASS_MAX_TTL` | `1h` | Ceiling a grant's requested TTL is clamped to (Go duration, positive) |

## See also

- [members-and-rbac.md](./members-and-rbac.md) — standing role bindings and scopes.
- [notifications.md](./notifications.md) — wiring the `breakglass.activated` alert.
