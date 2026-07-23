# How-to: Postgres backup & restore (data-layer)

Janus keeps **all durable state in PostgreSQL** — projects, environments,
configs, secret ciphertexts, wrapped project KEKs, the wrapped master key /
seal config, users, role bindings, service-token HMACs, audit events, and
scheduler/lease state. Backing up that database is therefore the foundation
of your disaster-recovery plan. This guide covers the **Postgres data
layer**; for the application-level `janus backup` (a logical, key-preserving
export) see [ops/backup-restore.md](../ops/backup-restore.md).

## What each backup covers

Janus has **two independent backup mechanisms** that protect different things.
Run **both**.

| | `janus backup` (app-level) | Postgres backup (this guide) |
|---|---|---|
| **Format** | Logical JSONL export of Janus rows | Full database dump / physical base backup + WAL |
| **Covers** | Wrapped KEKs, wrapped DEKs, secret ciphertext, password hashes, token HMACs, seal config, audit chain | **Everything in the database** — the same rows plus anything not modeled by the app export |
| **Does NOT cover** | Anything outside the modeled tables; not point-in-time | Nothing at the DB level, but it's still just ciphertext + wrapped keys |
| **Plaintext secrets?** | **No** — ciphertext only | **No** — ciphertext only |
| **Restore target** | A fresh Janus server (`janus restore`, same version) | A fresh/empty Postgres cluster |
| **PITR (arbitrary point in time)?** | No — restores to the moment of the dump | **Yes**, with WAL archiving |

> **Neither backup contains the unseal material.** The master key is never
> persisted in plaintext; it is reconstructed from your Shamir shares (or
> unwrapped by your cloud KMS key) at unseal time. A database dump plus enough
> shares equals your vault — store them separately, and treat your shares (or
> KMS key + its IAM access) as a first-class part of the DR plan. A restored
> database is **useless** without them.

## Logical dump with `pg_dump` / `pg_restore`

A logical dump is the simplest option and is version-portable. For the
docker-compose stack (Postgres published on `127.0.0.1:5433`, database
`janus`, user `janus`):

```sh
# Custom-format dump (compressed, restorable selectively) — recommended.
pg_dump \
  --format=custom \
  --host=127.0.0.1 --port=5433 --username=janus \
  --dbname=janus \
  --file=janus-db-$(date +%F).dump

# Or against the container directly (no host client needed):
docker compose exec -T postgres \
  pg_dump --format=custom --username=janus --dbname=janus \
  > janus-db-$(date +%F).dump
```

`pg_dump` prompts for the password (or read it from `PGPASSWORD` /
`~/.pgpass`; in the dev stack it is `janus-dev`). The dump is safe at rest
like any ciphertext, but treat it as sensitive metadata (secret **names**,
paths, actors, timestamps are all present) — store it encrypted and offsite.

Restore into a **fresh, empty** database:

```sh
# Recreate an empty database, then restore into it.
createdb --host=127.0.0.1 --port=5433 --username=janus janus
pg_restore \
  --host=127.0.0.1 --port=5433 --username=janus \
  --dbname=janus --no-owner \
  janus-db-2026-07-23.dump
```

Then start the Janus server (it auto-migrates on boot), boot it with the
**same `JANUS_SEAL_TYPE`** as the origin, and unseal with the **original**
shares (or the same KMS key). Do **not** run `janus init` — the seal config
came back with the dump.

## WAL archiving / point-in-time recovery (PITR)

A `pg_dump` restores only to the instant the dump ran. For
restore-to-any-second recovery, use a **physical base backup plus continuous
WAL archiving**:

1. Take a base backup of the data directory:

   ```sh
   pg_basebackup \
     --host=127.0.0.1 --port=5433 --username=janus \
     --pgdata=/backups/janus-base --format=tar --wal-method=stream --gzip
   ```

2. Enable continuous WAL archiving on the Postgres server so every WAL
   segment is shipped offsite as it fills. On the compose Postgres you set
   this by mounting a `postgresql.conf` (or passing `-c` flags via
   `command:`) with, for example:

   ```conf
   wal_level = replica
   archive_mode = on
   archive_command = 'test ! -f /wal-archive/%f && cp %p /wal-archive/%f'
   ```

   Mount a durable, offsite-shipped volume at `/wal-archive`. (In production
   many operators instead point Janus at a **managed Postgres** — RDS, Cloud
   SQL, Neon, etc. — and use that provider's built-in PITR / automated
   backups, which is the least-effort path.)

3. To recover to a point in time, restore the base backup, drop the archived
   WAL alongside it, and set a `recovery_target_time` before starting
   Postgres. See the PostgreSQL docs on
   [continuous archiving and PITR](https://www.postgresql.org/docs/16/continuous-archiving.html).

## Scheduled encrypted backups to S3 (built-in)

Instead of (or alongside) an external cron running `janus backup`, the server can
**produce and upload the app-level backup on a schedule** to any
S3-compatible object store (AWS S3, MinIO, Cloudflare R2, Backblaze B2, …). It
uploads the **exact same sealed artifact** `GET /v1/sys/backup` streams — a
key-preserving logical dump in which wrapped KEKs and ciphertext stay wrapped.
**No plaintext secret and no unseal material ever leave the process**; the S3
object is useless without the origin's Shamir shares or KMS key.

> **Protect the bucket.** The object is ciphertext + wrapped keys + secret
> **metadata** (names, paths, actors, timestamps). Treat it like any sensitive
> backup: a private bucket, encryption-at-rest (SSE), least-privilege IAM
> (`s3:PutObject`/`ListBucket`/`GetObject`/`DeleteObject` on the prefix only),
> and — as always — your unseal shares stored **separately**.

### Enable it

The engine is **off by default**. Set a positive `JANUS_BACKUP_TICK` plus the S3
destination and static credentials:

```sh
JANUS_BACKUP_TICK=6h                          # interval; unset/0 = disabled
JANUS_BACKUP_RETENTION=14                      # keep the 14 most recent; 0 = keep all
JANUS_BACKUP_S3_BUCKET=my-janus-backups
JANUS_BACKUP_S3_PREFIX=prod/                   # optional key prefix
JANUS_BACKUP_S3_REGION=us-east-1
JANUS_BACKUP_S3_ACCESS_KEY_ID=AKIA...          # STATIC creds (never the host's ambient identity)
JANUS_BACKUP_S3_SECRET_ACCESS_KEY=...
# JANUS_BACKUP_S3_ENDPOINT=https://<account>.r2.cloudflarestorage.com  # optional: S3-compatible stores
```

Each tick uploads `s3://<bucket>/<prefix>/janus-backup-<UTC-timestamp>.jsonl`.
If `JANUS_BACKUP_TICK` is set but the bucket/region/credentials are incomplete,
the server **fails to boot** with a clear error rather than silently not backing
up. The dump uses a single consistent snapshot, so the artifact is FK-consistent.

Every attempt (success or failure) is recorded in a bounded `backup_runs` history
table (value-free: object path, byte count, and a **sanitized** error category —
never key material or credentials). The last attempt is surfaced value-free under
`GET /v1/sys/status` → `backup` (`enabled`, and `last` = status/age/object/size),
so the Settings health panel can show "last backup" at a glance.

### Retention (keep N / prune older)

After each successful upload the engine lists objects under the prefix, keeps the
`JANUS_BACKUP_RETENTION` most recent `janus-backup-*.jsonl` objects, and deletes
the rest. Objects that don't match that name shape are **never** pruned, so it is
safe to share the prefix. `JANUS_BACKUP_RETENTION=0` (or unset) keeps everything.

### Restore-rehearsal (verify a backup restores — without clobbering anything)

An untested backup is a hope, not a plan. `janus backup rehearse` downloads the
latest scheduled backup (or `--object-key <k>`) and **verifies it restores
without touching the live instance**: it validates the archive structure (a
version-1 header, only known tables, every row well-formed) and — best-effort —
that the wrapped project-KEK material **decrypts under the current unseal**, then
discards the download. It **never** writes to the database or to S3.

```sh
# Verify the most recent backup (server must be unsealed for the decryptability probe).
janus backup rehearse

# Verify a specific object.
janus backup rehearse --object-key prod/janus-backup-20260723T010000Z.jsonl
```

A `VERIFIED` line means the artifact parsed cleanly, matches the current schema
version, and its wrapped material decrypts here. `decryptable=false` with a note
means the backup was written by a **different** instance (a foreign backup won't
unwrap under this keyring) — expected when rehearsing against a fresh instance,
not corruption. The same check is available over the API at
`POST /v1/sys/backup/rehearse` (instance `sys:backup` authority; value-free
response).

## Recommended routine

- **Nightly** `pg_dump` (custom format), shipped offsite and encrypted.
- **Continuous** WAL archiving (or a managed-Postgres equivalent) for PITR.
- **Periodic** `janus backup` as a second, application-level copy that a
  fresh Janus server can ingest directly (handy for migrating instances) — or
  enable the built-in **scheduled encrypted S3 backup** (`JANUS_BACKUP_TICK`
  + `JANUS_BACKUP_S3_*`, with `JANUS_BACKUP_RETENTION`) to have the server do
  this for you, and periodically `janus backup rehearse` to prove it restores.
- **Separately stored** unseal shares (or documented KMS key + IAM access).
- **Test restores** on a throwaway instance regularly — an untested backup is
  a hope, not a plan.

See also: [production deployment §7](production-deployment.md#7-backups),
[ops/backup-restore.md](../ops/backup-restore.md) (app-level `janus backup`),
[master key & backup](master-key-and-backup.md).
