# How-to: master-key rotation, Shamir rekey, and backups (Settings)

**Settings** shows the instance facts (seal type, server version, master-key
version and last rotation) and holds the owner-level key operations. System
reference: [crypto.md](../crypto.md); backup/restore mechanics:
[ops/backup-restore.md](../ops/backup-restore.md).

## Rotate the master key

**Settings → Master key → Rotate master key.** Generates a new master key and
re-wraps every project KEK under it — **online**, secrets stay readable
throughout, nothing is re-encrypted at the data layer. Do this on a schedule
or after any suspected exposure of wrapped material. Owner-only; the new
version shows in the instance table.

Rotation does **not** change the unseal shares — the shares reconstruct the
key-encrypting secret, which is preserved across rotation.

## Rekey the Shamir shares

Use when a shareholder leaves or shares may have leaked.

1. **Rekey shares…** starts the ceremony (a nonce scopes it; nothing changes
   yet).
2. Present the **current** threshold of shares one by one — progress is shown.
3. On the last share, a **fresh set of shares appears exactly once**.
   Distribute them, then dismiss. Old shares are dead immediately.

Abort at any point before completion and nothing has changed. If the server
uses KMS auto-unseal, rekey does not apply — key custody lives with the KMS
key.

## Backups

**Download backup** streams the encrypted dump: wrapped keys and ciphertext
only, **no plaintext secrets** — but still sensitive (it plus enough shares
equals your vault). Restore is an operator action on a fresh server via
`POST /v1/sys/restore` / the CLI — see
[backup-restore.md](../ops/backup-restore.md). Remember the real backup
strategy is this dump **plus** Postgres backups **plus** shares stored
separately from both.

## Account

**Change passphrase** (current + new, minimum 12 chars) applies to your own
password login; sessions elsewhere stay valid until they expire.
