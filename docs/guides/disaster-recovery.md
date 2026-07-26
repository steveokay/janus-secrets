# Disaster recovery: locked out of your own instance

The bad morning: the only account with the `owner` role can't log in. The
password is gone, or the phone with the authenticator app is gone, or both. The
data is *fine* — every secret is sitting in Postgres, wrapped exactly as it
always was — but nobody can open the front door.

Janus fixes this on the **server console**. You stand on the host (SSH, `docker
exec`, `kubectl exec`, whatever gets you a shell next to the database), prove
you hold the instance's seal material, and mint a new password for the account:

```sh
janus admin reset-password --email owner@corp.io
```

There is **no HTTP endpoint** behind this. No "forgot password" link, no reset
token in an email, nothing to phish or misconfigure. The only way in is a shell
on the box *plus* the unseal shares (or the cloud KMS key). That is deliberate:
a secrets manager should not ship a remotely reachable credential-reset path.

## Before you start

You need three things:

1. **A shell on the server host**, with the same `JANUS_DATABASE_URL` the server
   uses. The command talks straight to Postgres, exactly like `janus migrate`.
   The Janus server does **not** need to be running or unsealed.
2. **The `janus` binary.** It is the same binary that runs the server, so in a
   container deployment you already have it — `docker compose exec janus janus
   admin reset-password …`.
3. **The instance's seal material** — an unseal quorum for a `shamir` seal, or
   access to the configured cloud KMS key. See the next section.

## Why it asks for the seal material

Argon2id password hashing needs no key material at all. Nothing this command
writes touches an encrypted column. Requiring the master key anyway is a
**control, not a technical dependency**:

- It proves the operator holds the instance's root of trust, not merely a
  database connection.
- It keeps the ceremony shaped like `janus unseal`, so the same custodians and
  the same runbook apply — recovery is not a second, weaker path.
- It stops incidental Postgres access — a stolen dump, a leaked DSN, a
  misconfigured read replica — from being escalated into an owner takeover.

Row **A7** of [the threat model](../threat-model.md) already places root and DBA
inside the trust boundary, so this grants nobody new power. What it does is turn
a total-loss event into an inconvenience.

The key is reconstructed, verified against the key check value, and wiped
immediately. It is never used to encrypt or decrypt anything here.

## The ceremony, by seal type

Run `janus seal-status` if you're not sure which seal this instance uses.

### Shamir (`shamir`)

The command prompts for a quorum of unseal shares — the same count `janus
unseal` needs, read from the stored seal configuration. **Echo is off**: shares
are typed (or pasted) blind and never land in your scrollback.

```
$ janus admin reset-password --email owner@corp.io
Reset the password for owner@corp.io and revoke all of their sessions? [y/N]: y
Unseal share 1 of 3
Share:
Unseal share 2 of 3
Share:
Unseal share 3 of 3
Share:

Password reset — the new credential is shown once.
It WILL NOT BE SHOWN AGAIN.
  Email:    owner@corp.io
  Password: 8Qv2m0rXe7cLp3NfTk9aZwYs1dHu4Bj6

2 session(s) revoked; sign in and change this password immediately.
```

A mistyped share is rejected before anything changes — the key check value
catches a wrong reconstruction, and the command exits with `could not obtain the
master key`. Rerun it; nothing was written.

Shares can also be piped in, one per line, for a scripted recovery:

```sh
printf '%s\n%s\n%s\n' "$SHARE1" "$SHARE2" "$SHARE3" \
  | janus admin reset-password --email owner@corp.io --yes
```

`--yes` is **mandatory** when stdin is not a terminal — a scripted run that
silently invalidates every session should be explicit.

### Cloud KMS (`awskms`, `gcpkms`, `azurekv`)

Nothing to type. The command unwraps the master key through the same ambient
cloud credentials the server itself uses (the AWS default credential chain, GCP
application-default credentials, Azure `DefaultAzureCredential`) and the same
key configuration (`JANUS_AWS_KMS_KEY_ARN`, `JANUS_GCP_KMS_KEY`,
`JANUS_AZURE_KEYVAULT_URL` + `JANUS_AZURE_KEY_NAME`). Export the same
environment the server runs with and it just works:

```
$ janus admin reset-password --email owner@corp.io --yes
unwrapping the master key via awskms...

Password reset — the new credential is shown once.
...
```

If the host has no permission to `Decrypt` with that key, the command fails and
changes nothing. Authority over the KMS key *is* the authority to recover.

## What the reset does

- **Generates a new random password** (24 random bytes, 32 base64url
  characters) and prints it **exactly once**, in the same style as the
  initial-admin credential from `janus init`. Janus stores only its Argon2id
  hash. Nothing writes it to a file, a log, or the audit trail. Copy it, use it,
  change it.
- **Revokes every session the account holds.** Any browser or `janus login`
  session that predates the reset is dead — including one an attacker may have
  established with the old password. Everyone else's sessions are untouched.
- **Leaves everything else alone.** Roles, project memberships, service tokens,
  passkeys, and secrets are unchanged. The account keeps exactly the access it
  had.

Sign in and change the password immediately — Settings → Account → Change
passphrase.

## MFA is *not* cleared by default

If the account has TOTP enabled, it still has TOTP enabled after the reset. You
will be asked for a code on the next login. That is the correct default: a
password reset must never be a silent 2FA bypass.

If the second-factor device is **also** lost, and no recovery code survives,
add the flag:

```sh
janus admin reset-password --email owner@corp.io --clear-mfa
```

This is the louder, more dangerous step. It removes the TOTP enrolment **and
every recovery code**, so the account drops to a single factor until it
re-enrols. It is audited under its own action (`admin.clear_mfa`), separate from
the password reset, precisely so it stands out in the log. Reach for a recovery
code first; reach for `--clear-mfa` only when there is nothing left to reach
for.

Re-enrol a second factor as soon as you are back in — see
[two-factor-auth.md](./two-factor-auth.md).

Passkeys are not touched by either flag. If an account has a working passkey,
signing in with it is a better route than this command; see
[passkeys.md](./passkeys.md).

## What gets audited

A recovery that leaves no trace would be a hole in an append-only log, so both
operations write into the hash chain:

| Action | When | Detail |
|---|---|---|
| `admin.reset_password` | every successful reset | `seal=<type> sessions_revoked=<n>` |
| `admin.clear_mfa` | only with `--clear-mfa`, and only when an enrolment existed | `seal=<type>` |

Both carry:

- **actor kind `local_console`**, actor name `local-console`, and a **NULL actor
  id** — nobody was logged in; this was the console.
- **IP `local`** — there was no request and no peer.
- **resource `users/<id>`** — the account that was reset. Never the address's
  password, never any secret value.

The events are ordinary links in the chain, so `GET /v1/audit/verify` (and the
"chain verified" badge in the UI) still reports a valid chain afterwards. If the
audit append fails for any reason, the password change is **rolled back** —
Janus would rather leave you locked out than leave an unaudited credential
change in the database.

Subscribe a notification channel to these actions if you want a recovery to page
someone; see [notifications.md](./notifications.md).

## What this does **not** recover

Be clear-eyed about the boundary. This command recovers an *account*. It does
not recover *keys*.

- **Lost unseal shares are still fatal.** If you cannot assemble a quorum, you
  cannot unseal the server, and you cannot run this command either. The
  ciphertext in Postgres stays ciphertext forever. Distribute shares to separate
  custodians and rehearse the ceremony — this is the one failure mode with no
  back door, by design.
- **A lost or deleted cloud KMS key is unrecoverable.** Same reason. Protect the
  key with a deletion window and appropriate IAM.
- **A lost master key cannot be reconstructed.** There is no escrow, no vendor
  copy, no recovery key. That is the property the whole system is built on.
- **It cannot decrypt anything for you.** It changes a password hash; it never
  reads a secret.
- **It does not create accounts.** The email must already exist. An instance
  that was never initialized (`janus init` never ran) is refused outright —
  there is nothing there to recover.
- **It does not restore data.** For a corrupted or lost database, that is
  [backup-and-restore.md](./backup-and-restore.md).

## Refusals you may hit

| Message | Meaning |
|---|---|
| `JANUS_DATABASE_URL is not set` | Export the same DSN the server uses. |
| `this instance is not initialized (no seal configuration)` | `janus init` never ran; there are no accounts. |
| `no user with email "…"` | Checked *before* the ceremony, so a typo costs a retry rather than a share collection. |
| `refusing to reset without confirmation: pass --yes` | Non-interactive stdin with no `--yes`. |
| `unseal share N is not valid hex` | A share was mistyped or truncated. The offending input is never echoed back. |
| `could not obtain the master key: key check value mismatch` | A wrong (but well-formed) share. Nothing was changed; rerun. |

## Related

- [getting-started.md](./getting-started.md) — first login and the bootstrap
  admin credential.
- [master-key-and-backup.md](./master-key-and-backup.md) — rotating the master
  key and rekeying Shamir shares.
- [two-factor-auth.md](./two-factor-auth.md) — TOTP enrolment and recovery
  codes (use a recovery code before `--clear-mfa`).
- [backup-and-restore.md](./backup-and-restore.md) — recovering *data*, not
  accounts.
- [break-glass.md](./break-glass.md) — the in-band path for "I need more access
  right now", for when you *can* still log in.
