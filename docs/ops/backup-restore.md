# Backup & restore (disaster recovery)

Janus backups are **key-preserving logical dumps**: every row exactly as
stored — wrapped KEKs, wrapped DEKs, ciphertexts, password hashes, token
HMACs. A backup file contains **no plaintext secrets** and is useless without
the original unseal material. Corollary: **your unseal shares (or KMS key)
are part of your DR plan** — a backup cannot be opened without them.

## Taking a backup

    janus backup --out janus-backup.jsonl          # stored session
    janus backup --token $JANUS_TOKEN > b.jsonl    # service token / CI

Requires an admin (`sys:backup`). Each backup writes a `sys.backup` audit
event. Cron it and ship the file offsite; it is safe at rest like any
ciphertext, but treat it as sensitive metadata (names, paths, actors).

## Restoring

1. Fresh Postgres + the **same janus version** that wrote the backup
   (restore checks the schema version and refuses a mismatch).
2. Start the server (it auto-migrates). Boot it with the **same
   `JANUS_SEAL_TYPE`** as the origin instance — the unsealer is built at
   boot from that env var, so a shamir backup restored onto a server booted
   for KMS (or vice-versa) needs a restart with the matching type. Do **not**
   run `janus init`.
3. `janus restore janus-backup.jsonl`
4. `janus unseal` with the ORIGINAL shares (or start with the same KMS key).
5. Verify: `janus seal-status`, `GET /v1/audit/verify` (chain includes a
   `sys.restore` event), spot-read a secret.

Restore only works on an empty instance (no seal config, users, or
projects) — it will never overwrite live data. A failed or truncated restore
rolls back completely; the instance stays empty and restorable.

Sessions are not backed up: everyone logs in again after a restore.
