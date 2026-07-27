# How-to: groups — one binding for a whole team

Bindings are per user. In an organisation with several product teams that
means the same grant repeated for every person on every project, and
offboarding becomes a hunt for individual rows across instance, project and
environment scopes.

A **group** is a subject a binding can target instead of a user. Bind
`Team Payments` as developer on a project once, and everyone in it holds that
role — including people who join later.

Groups do not change how permissions are decided. A group binding **unions**
with direct bindings exactly as two direct bindings do: no precedence, no deny
rules, deny-by-default unchanged. See
[members-and-rbac](members-and-rbac.md) for the model itself.

## Two kinds of group

A group is either IdP-fed or admin-curated, and never both.

| | `oidc` | `local` |
|---|---|---|
| Membership from | the IdP's group claim, refreshed at each sign-in | an explicit list you manage in Janus |
| Editable in Janus | no | yes |
| Works for password logins | no | yes |
| Use it when | you have Okta / Entra / Google and want the IdP to be the record of who has access | you have no IdP yet, or need to cover password logins |

The split is deliberate, and it buys one statement worth having:

> Access granted through an IdP group is **fully described by the IdP**.

So an access review run against Entra is *complete* for those bindings. If a
group could be IdP-fed **and** hand-edited, that review would come back clean
while a locally-added member still held production access — and an IdP outage
would tempt you into adding people "temporarily", a grant that then outlives
the incident forever, because a login sync only ever clears rows it owns. For
genuinely temporary access use [break-glass](break-glass.md), which is
TTL-clamped, loudly audited and expires by itself.

Group names are unique across both kinds, so an IdP group and a local group can
never quietly become the same group.

## A group can never be owner

A group binding holds `viewer`, `developer` or `admin`. `owner` is refused by
both the API and the database.

Owner rotates the master key, prunes the audit chain and hard-destroys secret
history. Group-deriving it would hand that tier to whoever administers the IdP
— usually the identity team rather than the Janus operator — who can add
themselves to a group silently, and whose membership list Janus cannot
authoritatively enumerate. It also keeps the never-lock-out guard honest: every
instance owner is a direct binding, so an IdP outage can never leave the
instance without one.

## Local groups (no IdP required)

**Groups → + New group**, kind **local**. Then select it and add members from
the picker.

```
janus group create "Team Payments" --kind local
janus group add-member "Team Payments" <user-id>
janus group bind "Team Payments" --role developer --project atlas-api
```

## IdP groups

1. **Point Janus at the claim.** Integrations → OIDC → **Groups claim**, or
   `groups_claim` on `PUT /v1/oidc/config`. Empty disables group sync
   entirely — you opt in. Common values:

   | Provider | Claim | Value shape |
   |---|---|---|
   | Okta | `groups` | group names |
   | Entra ID | `groups` | **object GUIDs** unless you configure the app to emit names |
   | Google Workspace | `groups` | group email addresses |
   | Keycloak | `groups` or `realm_access.roles` | names (dotted paths work) |

   A dotted path walks nested claims (`realm_access.roles`). If a path could
   be produced two different ways — a claim literally named `a.b` *and* a
   nested `{"a":{"b":…}}` — the token is rejected rather than resolved by
   precedence.

2. **Create a group per claim value.** **Groups → + New group**, kind **oidc**,
   and paste the exact value the IdP emits into **Claim value**. The group's
   **name** is yours and is what appears on bindings, so an Entra GUID still
   reads as "Team Payments".

   ```
   janus group create "Team Payments" --kind oidc --claim 8f14e45f-ceea-467a-9d0e-7f4b2a1c9c33
   ```

   A claim value with no matching group grants nothing — groups are never
   auto-created.

3. **Bind it** exactly like a local group.

Membership updates at each sign-in. Users who have never signed in do not
appear in the member list, because Janus has never seen a token for them.

## Who manages what

Two different authorities, on purpose:

- **The catalog** — creating groups, editing local membership, setting the
  claim — is instance-scoped `group:manage` (admin+).
- **Binding a group at a scope** is `member:manage` **at that scope**, capped
  by your own bound role.

So a project admin can grant a group access to their project — the same
authority they already have over users — but cannot add themselves to a group,
and therefore cannot reach a project they don't already administer.

The cap is measured against your *durable* role, never a break-glass elevation,
so an emergency elevation cannot be turned into a lasting group binding.

## How quickly a change takes effect

| Change | Effective |
|---|---|
| Unbind a group, delete it, or remove a local member | **next request** — permissions are resolved per request, never frozen into a session |
| Removed from a group **in the IdP** | that user's **next sign-in**, and at the latest when their session expires (24h, or sooner with `JANUS_SESSION_IDLE_TIMEOUT`) |

To cut IdP-derived access immediately, remove the binding or revoke the user's
sessions (Settings → Active sessions, or `janus session revoke`).

## Known limitation: Entra group overage

Entra stops emitting `groups` once a user is in roughly 200 groups, sending a
`_claim_names` pointer to Microsoft Graph instead. Janus treats that as
**membership unknown**, keeps the last good snapshot, and writes a
`group.sync` audit event with `status=overage`. It deliberately does not treat
it as "in no groups", which would clear every membership and look exactly like
a legitimate removal from all of them.

The consequence is that such a user's membership stops tracking the IdP until
you fix it upstream — assign the application a filtered group set in Entra so
the claim stays under the limit. Watch for `group.sync` events with
`status=overage` in the audit ledger.

## What is audited

Value-free, names and ids only: `group.create`, `group.delete`,
`group.member.add`, `group.member.remove`, `group.binding.grant`,
`group.binding.revoke`, and `group.sync`.

`group.sync` is written only when a login actually changed membership (or hit
the overage case), so routine sign-ins do not flood the ledger.

## CLI

```
janus group list
janus group show "Team Payments"          # members + every scope it reaches
janus group create <name> --kind local|oidc [--claim <value>]
janus group delete <name>
janus group add-member <group> <user-id>
janus group remove-member <group> <user-id>
janus group bind <group> --role viewer|developer|admin [--project P] [--env E]
janus group unbind <group> [--project P] [--env E]
janus group bindings [--project P] [--env E]
```

Omit `--project`/`--env` for an instance-wide binding; with only `--project`
the binding covers that project's environments and configs.
