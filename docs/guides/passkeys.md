# Passkeys (WebAuthn)

A **passkey** is a public-key credential held by your device — a laptop's secure
enclave, a phone, or a hardware security key. Signing in with one is a single
step: Janus sends a random challenge, your device asks you to confirm with its
PIN, fingerprint, or face, and returns a signature. Nothing that a phishing site
or a database dump could reuse ever crosses the wire.

Passkeys are **not a second factor bolted onto the password** — they are an
alternative front door that is already two factors on its own (possession of the
device + the PIN/biometric that unlocks it). See
[Passkeys and TOTP](#passkeys-and-totp) for exactly how the two interact, and
[two-factor-auth.md](./two-factor-auth.md) for the TOTP factor itself. The two
features are independent: you can enable either, both, or neither.

## Enabling passkeys on the server

Passkeys are **off by default**. An operator turns them on with two environment
variables, because the WebAuthn **Relying Party ID** cannot be guessed from the
request: a credential is permanently bound to the RP ID that created it, and a
browser silently refuses an assertion whose RP ID does not match. Deriving it
from the `Host` header would make passkeys break the moment a user reached the
server through a different hostname.

| Variable | Meaning |
| --- | --- |
| `JANUS_WEBAUTHN_RP_ID` | The Relying Party ID: a bare registrable domain — no scheme, no port, no path (e.g. `janus.example.com`). |
| `JANUS_WEBAUTHN_ORIGINS` | Comma-separated list of the fully-qualified origins the UI is served from (e.g. `https://janus.example.com`). |
| `JANUS_WEBAUTHN_RP_NAME` | Optional display name shown in your device's prompt. Defaults to `Janus`. |

```sh
JANUS_WEBAUTHN_RP_ID=janus.example.com
JANUS_WEBAUTHN_ORIGINS=https://janus.example.com
JANUS_WEBAUTHN_RP_NAME="Janus — Acme"
```

Janus validates the pair **at startup** and refuses to boot on a mismatch, so a
typo is a loud startup error rather than "passkeys mysteriously don't work":

- The RP ID must be a bare host — no `https://`, no `:8443`, no trailing path,
  no wildcard, lower-case, and not an IP address.
- Every origin must parse, use `http`/`https`, and carry no path, query, or
  credentials.
- `http://` is accepted **only** for `localhost` / loopback (the browser requires
  a secure context everywhere else).
- Every origin's host must be the RP ID itself or a **subdomain** of it. This is
  label-aware: `https://notexample.com` is not a subdomain of `example.com`.

Local development, with the Vite dev server on `:5173`:

```sh
JANUS_WEBAUTHN_RP_ID=localhost
JANUS_WEBAUTHN_ORIGINS=http://localhost:5173,http://localhost:8200
```

> **Changing `JANUS_WEBAUTHN_RP_ID` retires existing passkeys.** Credentials are
> stored against the RP ID they were registered under and are only offered under
> that same ID, so moving domains means everyone re-enrols. Plan it like a
> domain migration, and keep the password path available while you do.

## Registering a passkey

From **Settings → Passkeys**, click **Register a passkey**, give the device a
name you'll recognise, and follow your platform's prompt.

Under the hood: the server issues a **single-use, expiring challenge bound to
your session**, the browser calls `navigator.credentials.create()`, and the
server verifies the origin, the RP ID, the challenge, the attestation, and that
your device actually performed **user verification** before storing the
credential.

You can register several passkeys — a laptop, a phone, a backup security key —
each with its own name. Registering the *same* authenticator twice is refused;
your device will usually tell you it already has a passkey for this account.

### Discoverable credentials are required

Janus sets `residentKey: required` on enrolment, so every new passkey is a
**client-side discoverable credential** — one your device can offer with no
address typed first. That is what makes [passwordless
sign-in](#passwordless-sign-in-no-address-needed) work.

This is a deliberate trade. `preferred` (what Janus used before) lets an
authenticator quietly store a *non*-discoverable credential, and the user then
finds the passwordless button inexplicably ignores their passkey. Requiring it
makes the trade-off loud instead: an authenticator that cannot store a resident
key — typically a hardware security key whose slots are full — refuses enrolment
with a visible error. Your password path is untouched either way.

Janus also asks the browser for the `credProps` extension, which reports whether
a discoverable credential was actually created, and records the answer so
**Settings → Passkeys** can show it per device (see
[Managing passkeys](#managing-passkeys)).

### What gets stored

Only **public** material: the credential id, the COSE public key, attestation
metadata, the signature counter, your nickname, and timestamps. There is no
secret-equivalent value, so — unlike the TOTP secret — nothing here is wrapped
under the master key, and master-key rotation has nothing to re-wrap. The
private key never leaves your authenticator.

## Passwordless sign-in (no address needed)

On the login screen, choose **A passkey — no address needed**. You type nothing:
the browser offers whichever passkey it holds for this site, your device verifies
you, and you are signed in.

Where the browser supports it, the same thing happens through **autofill**: click
into the address field and your passkey is offered inline alongside saved
addresses ("conditional mediation"). This runs silently — if the browser doesn't
support it, or you ignore it, the explicit buttons behave exactly as before.

### How the account is determined, and why that is safe

The [email-identified flow](#signing-in-with-a-passkey-email-identified) takes
the account from the **challenge**, so a challenge minted for one account can
never be spent on another. A passwordless ceremony cannot work that way — at the
start there *is* no account — so identity necessarily comes from the assertion.

That inversion is only safe because the assertion is never believed on its own:

1. The presented **credential id** is looked up in Janus's own store. An id it
   never issued resolves to nothing and the ceremony ends.
2. The account is whatever **that stored row** says owns the credential. The
   `userHandle` the browser sends is *not* used to select the account.
3. The presented `userHandle` must **equal** the handle derived from that owner's
   account id. A credential belonging to one user, presented with another user's
   handle, is rejected outright — credential substitution cannot authenticate.
4. The signature is verified against **that stored credential's public key**, and
   the asserted credential must be present in that account's own credential set.

Everything the identified path enforces still holds: the challenge is single-use
and expiring, `userVerification: required` is re-asserted from the authenticator
flags, a signature counter that fails to advance is fatal, origin and RP ID are
verified, disabled accounts are refused, and lockout is honoured (revealed only
after a valid assertion).

The two ceremonies use **separate challenge pools**, so a challenge minted for
one can never be finished as the other.

> **Enumeration gets better here, not worse.** No address is sent at begin, so
> there is nothing to probe: the begin response is identical for every caller
> apart from the random challenge, and carries no credential list at all. Every
> possible failure at finish — unknown credential, mismatched handle, bad
> signature, replayed challenge, disabled account — returns the *same* 401, so
> "no such passkey" and "wrong signature" cannot be told apart.

### When passwordless does not work

The passwordless button needs a **discoverable** credential on the device. A
passkey enrolled before Janus started requiring one may not be discoverable, in
which case the browser reports it has nothing to offer. Check **Settings →
Passkeys**, which shows this per device, and use the address-identified button
(or re-enrol) in the meantime.

## Signing in with a passkey (email-identified)

On the login screen, type your email and choose **A passkey**. Your device
prompts; on success you are signed in. There is no password step and no
two-factor code step.

This path still exists because it works with passkeys whose discoverability is
unknown or absent, and because on a shared device it lets you say up front which
account you mean.

The login-begin endpoint answers **identically** whether the address is a real
account with passkeys, a real account without them, a disabled account, or a
complete stranger — a stable, unguessable decoy credential id is substituted so
the endpoint cannot be used to enumerate accounts. The ceremony simply fails in
the browser ("no matching passkey"), exactly as it would for a real account whose
device isn't present.

One residual: the decoy is a single credential, so an account with **two or
more** registered passkeys is distinguishable by the length of the returned
credential list — never by existence, and never in the common one-passkey case.
The per-IP rate limit bounds how far that can be sampled.

## Managing passkeys

**Settings → Passkeys** lists every registered device with when it was added,
when it was last used, and whether it can be used **passwordlessly**:

| Passwordless | Meaning |
| --- | --- |
| **Yes** | The device confirmed a discoverable credential at registration, or has since completed a passwordless sign-in. **A passkey — no address needed** will work. |
| **Address needed** | The client explicitly reported it did *not* store a discoverable credential. Use the address-identified button, or re-enrol. |
| **Unknown** | Registered before Janus recorded this, and not yet used passwordlessly. It may or may not work; one successful passwordless sign-in promotes it to **Yes**. |

You can **Rename** or **Remove** each one.

**Removing your last passkey cannot lock you out.** A passkey is an additional
way in, never a replacement for the password: your passphrase — and your TOTP
code, where enabled — keeps working throughout. The UI says so explicitly before
you confirm.

## Passkeys and TOTP

A passkey login is **complete on its own** and does *not* additionally prompt for
a TOTP code. That is deliberate, not an oversight:

- Janus sets `userVerification: required` on **every** passkey ceremony, both
  registration and login, and rejects an assertion whose authenticator did not
  perform it. So a passkey sign-in already proves two things — possession of the
  device, and the PIN/biometric that unlocks it.
- Requiring a TOTP code on top would add a *third* factor to the strongest path
  while leaving the weakest path (password + TOTP) unchanged, which nudges users
  back toward passwords. That is the wrong incentive.

If your policy is "an authenticator app code, always", do not enable passkeys.
The two features are independent, and [TOTP](./two-factor-auth.md) continues to
gate every password login regardless of how many passkeys an account has.

## Passkeys and account lockout

Progressive per-account lockout counts **password** failures. Passkeys interact
with it as follows:

- A locked account is locked for passkey login too. A lock is a lock.
- The lock is revealed **only after a valid assertion** — the same rule the
  password path applies to the password-holder. An invalid assertion against a
  locked account returns the ordinary invalid-credentials error, so the endpoint
  is not a lock oracle.
- A **failed** passkey assertion never increments the lockout counter. There is
  no guessable secret to brute-force, and counting failures would let anyone lock
  an account out by spamming the endpoint.
- A **successful** passkey login clears the failure counter, exactly like a
  successful password login.

## API reference

Management routes require a **user session** (service tokens are rejected). All
passkey routes share a per-client-IP rate limit of **60/min sustained, burst 24**
— note that a single visit to the login screen spends two of those (the status
probe plus the background conditional-mediation challenge), and the limit is
per IP, so a large office behind one NAT shares it. Ceremony payloads are the
standard WebAuthn JSON structures.

| Method & path | Auth | Purpose |
| --- | --- | --- |
| `GET /v1/auth/webauthn/status` | none | `{ enabled, rp_id }` — the login screen's probe. |
| `GET /v1/auth/webauthn` | session | List the caller's passkeys (value-free metadata). |
| `POST /v1/auth/webauthn/register/begin` | session | Issue a registration challenge. |
| `POST /v1/auth/webauthn/register/finish` | session | Verify and store; label via `X-Janus-Passkey-Name`. |
| `PATCH /v1/auth/webauthn/credentials/{id}` | session | Body `{ nickname }`; rename. |
| `DELETE /v1/auth/webauthn/credentials/{id}` | session | Remove a passkey. |
| `POST /v1/auth/webauthn/login/begin` | none | Body `{ email }`; issue an assertion challenge. |
| `POST /v1/auth/webauthn/login/finish` | none | Verify the assertion and set the session cookie. |
| `POST /v1/auth/webauthn/login/discoverable/begin` | none | Optional body `{ conditional }`; issue a **passwordless** challenge bound to no account. |
| `POST /v1/auth/webauthn/login/discoverable/finish` | none | Verify a passwordless assertion and set the session cookie. |

`GET /v1/auth/webauthn` returns a `discoverable` field per credential: `true`,
`false`, or `null` for unknown.

## Security notes

- **Challenges are single-use, random, expiring (5 minutes), and bound to an
  account.** A finish claims the challenge with an atomic delete, so a replay —
  or two concurrent finishes of the same challenge — cannot both succeed. A
  registration challenge additionally has to belong to the session that started
  it; an *email-identified* login challenge determines which account the
  assertion is spent on, so a challenge minted for one account can never be
  redeemed for another. A **passwordless** challenge is bound to no account by
  design, and lives in its own pool so it cannot be crossed with either of the
  other two — there, the binding is done by the credential lookup instead (see
  [How the account is determined](#how-the-account-is-determined-and-why-that-is-safe)).
- **Origin and RP ID are verified on both ceremonies**, against the
  operator-configured values.
- **The signature counter is enforced.** Every assertion must carry a counter
  strictly greater than the stored one, checked as a compare-and-swap in the
  database so concurrent replays cannot race past it. A counter that fails to
  advance means a cloned authenticator or a replayed assertion, and the login is
  refused. Authenticators that report a permanent `0` (they don't implement a
  counter) keep working — a counter that is always zero carries no clone signal.
- **Attestation conveyance is `none`.** Janus does not run a FIDO Metadata
  Service, so demanding a signed attestation statement would yield a blob it
  cannot evaluate while de-anonymising your authenticator model. The public key,
  RP ID hash, origin, challenge, and flags are verified regardless.
- **Enrol, sign in, rename, and delete each write a value-free audit event**
  recording the credential id (a public handle) and the outcome — never key
  material. A rejected assertion is audited as a denied login; a counter
  regression is recorded distinctly so a cloned authenticator is visible in the
  trail. Every passkey login also records which ceremony it was
  (`ceremony=identified` or `ceremony=discoverable`), so a passwordless sign-in
  is visible as such in the ledger.
- **The SPA loads no external script.** WebAuthn is a native browser API, so the
  strict `'self'` CSP is untouched.
- **Third-party crypto.** Passkeys use `github.com/go-webauthn/webauthn` for COSE
  key parsing and attestation/assertion verification — a recorded, approved
  exception to the stdlib-only crypto rule (see
  [`CLAUDE.md`](../../CLAUDE.md)). The envelope, transit, and unseal crypto
  remain stdlib + `x/crypto`.

## See also

- [Two-factor authentication (TOTP)](./two-factor-auth.md) — the code-based
  second factor for password logins.
- [Members & RBAC](./members-and-rbac.md) — what an account can do once it is
  signed in.
- [SSO & workload federation](./sso-and-federation.md) — OIDC login, where the
  identity provider owns the multi-factor policy.
