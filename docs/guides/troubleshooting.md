# Troubleshooting

Most Janus problems are configuration, not code — and the worst of them are
configurations that are *internally valid*. They parse, they pass startup
validation, the server boots and serves happily, and something still fails
somewhere else, usually in a way that looks like an application bug.

This guide starts with **`janus doctor`**, which exists to find exactly that
class of problem, then works through the symptoms operators actually hit. For
the configuration reference behind every variable named here, see
[production-deployment.md](./production-deployment.md).

## Start with `janus doctor`

`janus doctor` runs a series of named checks over the `JANUS_*` environment, the
database, and — when one answers — the running server, and prints one
`PASS` / `WARN` / `FAIL` / `SKIP` line per check with the concrete fix for
anything wrong.

```sh
janus doctor
```

```
janus doctor — v0.2.0

  PASS  env.unknown       no unrecognised JANUS_* variables
  PASS  config.parse      the JANUS_* environment parses; `janus server` would accept it
  PASS  db.dsn            postgres://janus:[redacted]@db.internal:5432/janus?sslmode=require
  PASS  db.sslmode        sslmode=require
  PASS  db.pool           JANUS_DB_* unset — pgx defaults apply
  PASS  db.connect        connected to postgres://janus:[redacted]@db.internal:5432/janus?sslmode=require
  PASS  db.migrations     schema is current (version 44)
  PASS  seal.type         seal type "shamir"
  PASS  webauthn.config   rp_id=janus.example.com, origins=https://janus.example.com
  PASS  webauthn.origins  every configured passkey origin matches how this server is reached
  PASS  tls               https (static-cert)
  PASS  outbound.ssrf     outbound integration calls use the connect-time resolved-IP guard
  PASS  metrics           /metrics is enabled and bearer-token gated
  PASS  logging           level=info format=text
  PASS  http.limits       HTTP timeouts and body cap are at safe values
  PASS  audit.shipping    audit shipping is active
  PASS  backup.schedule   scheduled S3 backups are enabled
  PASS  server.status     https://127.0.0.1:8200 is running and unsealed

18 passed, 0 warning(s), 0 failed, 0 skipped
```

**Run it where the server runs**, with the same environment the server gets —
`doctor` reads the configuration through the exact same parser `janus server`
uses at boot, so what it reports is what the server would do. In a container:

```sh
docker compose exec janus janus doctor
```

### Flags

| Flag | Effect |
| --- | --- |
| `--json` | Emit the report as JSON (`{"version":…,"checks":[…],"summary":…,"ok":…}`) for machine consumption. |
| `--strict` | Exit non-zero on warnings as well as failures. Use this in CI. |
| `--offline` | Skip the database connection and every network probe. Pure configuration inspection. |
| `--address` | Base URL of the running server to probe. Defaults to one derived from `JANUS_LISTEN_ADDR`. |
| `--timeout` | Per-probe timeout for the database and HTTP checks (default `5s`). |

### Exit status

`0` when nothing failed, non-zero when any check is `FAIL` (or, with
`--strict`, any `WARN`). That makes it usable as a deploy gate:

```sh
janus doctor --strict --offline || exit 1
```

`SKIP` never affects the exit status. A check skips when it genuinely cannot
answer — no database configured, passkeys disabled, or a question a container
cannot answer about its own host.

### What `doctor` will never print

No secret value, on any path. DSN passwords, `JANUS_METRICS_TOKEN`, the audit
webhook HMAC key, S3 credentials, proxy URLs with embedded credentials, and
service tokens are all redacted — including where they appear inside a
third-party error string, which is the usual way a connection URL escapes.

### It does not need a running server

Every check except `server.status` works against configuration and the database
alone. `doctor` is meant to be run **before** you start a server, in the same
shell (or the same container spec) the server will get.

---

## Symptom → cause

| Symptom | Likely cause | Check | Fix |
| --- | --- | --- | --- |
| Passkey sign-in or enrolment fails in the browser with no server-side error | `JANUS_WEBAUTHN_ORIGINS` does not describe how users actually reach the server (wrong port, wrong scheme) | `webauthn.origins` | [Passkeys fail like a bug](#passkey-ceremonies-fail-and-look-like-a-product-bug) |
| A variable you set has no effect at all | Typo — the server ignores unknown `JANUS_*` names silently | `env.unknown` | [A setting is ignored](#a-setting-you-changed-has-no-effect) |
| Every secret route returns `503 {"code":"sealed"}` | The server is running but sealed | `server.status` | [Sealed vs unsealed](#everything-returns-503-sealed) |
| Server exits at boot with `seal type mismatch` | `JANUS_SEAL_TYPE` disagrees with the stored seal | `seal.type` | [Seal type mismatch](#the-server-refuses-to-start-with-a-seal-type-mismatch) |
| Server exits at boot with a `store: ping` error | Wrong host/port/credentials, or Postgres not accepting connections | `db.connect` | [Database connectivity](#the-server-cannot-reach-the-database) |
| Browser shows a certificate error | Static certificate expired, or does not cover the hostname | `tls` | [TLS and ACME](#tls-and-acme-mistakes) |
| ACME never issues a certificate | Inbound `:80` blocked, or DNS does not point here | `tls` | [TLS and ACME](#tls-and-acme-mistakes) |
| Audit events never arrive at your SIEM | Destination configured but `JANUS_AUDIT_SHIP_MODE` left unset | `audit.shipping` | Set `JANUS_AUDIT_SHIP_MODE=webhook` or `=syslog`. |
| Scheduled backups never run | Bucket configured but `JANUS_BACKUP_TICK` unset | `backup.schedule` | Set `JANUS_BACKUP_TICK` (e.g. `6h`). |
| `/metrics` returns `404` | `JANUS_METRICS_TOKEN` is unset — the endpoint is disabled, not broken | `metrics` | Set the token; see [observability.md](./observability.md). |
| A large audit export truncates mid-stream | `JANUS_HTTP_WRITE_TIMEOUT` is set | `http.limits` | Unset it; the default is no write timeout, deliberately. |

---

## Passkey ceremonies fail and look like a product bug

**Symptom.** A user clicks *Sign in with a passkey*, the device prompts, they
confirm — and the ceremony fails. Nothing appears in the Janus logs, because
nothing reached Janus. The browser rejected the ceremony on its own.

**Cause.** WebAuthn binds a credential to a **Relying Party ID** and will only
release an assertion to an **origin** that matches the one configured. Janus
takes both from `JANUS_WEBAUTHN_RP_ID` and `JANUS_WEBAUTHN_ORIGINS` and
validates them at boot — but that validation can only check the pair against
*itself*: that each origin's host is the RP ID or a subdomain of it, that the
scheme is `https` (or `http` for loopback), that there is no path.

It cannot check the part that actually goes wrong: whether the origin describes
**how users reach this server**. This configuration is completely valid and
completely broken:

```sh
JANUS_LISTEN_ADDR=127.0.0.1:8212
JANUS_WEBAUTHN_ORIGINS=http://localhost:8210    # ← the server is on 8212
```

The server starts, serves the UI on `:8212`, and every passkey ceremony fails.
Worse, if some other Janus happens to be listening on `:8210` — a second
instance, an older stack you forgot to stop — the port answers, so nothing
about it looks wrong from the outside.

**Find it.**

```
  WARN  webauthn.origins  1 configured passkey origin(s) do not describe how this server is reached
                            http://localhost:8210 names port 8210, but this server listens on port 8212 —
                            a browser loading the UI from that origin is NOT reaching this instance;
                            a DIFFERENT Janus instance is answering on that port
                            fix: set JANUS_WEBAUTHN_ORIGINS to the exact scheme://host:port a browser
                            uses to load the Janus UI (and JANUS_LISTEN_ADDR to the port it really
                            serves on) — the browser refuses the assertion when they disagree, so the
                            ceremony fails with no server-side error and looks like an application bug
```

**Fix.** Set `JANUS_WEBAUTHN_ORIGINS` to the origin bar you see in the browser
when the Janus UI is loaded — scheme, host, and port, exactly as written —
then restart. If several are legitimate (a Vite dev server plus the API port,
say), list them comma-separated:

```sh
JANUS_WEBAUTHN_RP_ID=localhost
JANUS_WEBAUTHN_ORIGINS=http://localhost:5173,http://localhost:8212
```

**How the check decides.** For a **loopback** origin the verdict comes from
comparing the origin's port with `JANUS_LISTEN_ADDR`: on one host, `localhost:P`
reaches whatever binds `P`, so if that is not the port this server binds, the
browser is not reaching this process. A live probe of the origin is used only to
say *which* kind of wrong it is ("a different Janus is answering" versus
"nothing is listening"). Deliberate exceptions, so the check does not cry wolf:

- **Ports 80 and 443** are where a local reverse proxy terminates, so a loopback
  origin on one of them is reported as an assumption, not a problem.
- **Inside a container** the comparison is meaningless — the published host port
  is invisible from within the namespace — so loopback origins are `SKIP`ped.
  This is why `docker compose exec janus janus doctor` on the bundled dev stack
  (published `8210` → container `8200`) reports a skip rather than a warning.
  To verify origins for a containerised deployment, run `doctor` from the host.
- **Non-loopback origins** are judged only on DNS. A public hostname routinely
  fails to connect *from the server itself* (no hairpin NAT, split-horizon DNS),
  but a name that does not resolve at all is nearly always a typo.

> **Changing `JANUS_WEBAUTHN_RP_ID` retires every existing passkey.** Credentials
> are stored against the RP ID they were registered under. Fixing an *origin* is
> free; changing the *RP ID* means everyone re-enrols. See
> [passkeys.md](./passkeys.md).

## A setting you changed has no effect

**Symptom.** You set a variable, restarted, and nothing changed. No error, no
warning, no log line.

**Cause.** The server reads a fixed set of `JANUS_*` names. Anything outside it
is ignored in silence — there is nowhere for a misspelling to surface. The
singular/plural pair is the classic:

```sh
JANUS_WEBAUTHN_ORIGIN=https://janus.example.com    # ignored
JANUS_WEBAUTHN_ORIGINS=https://janus.example.com   # what the server reads
```

**Find it.**

```
  WARN  env.unknown       1 unrecognised JANUS_* variable(s) — the server ignores these silently
                            JANUS_WEBAUTHN_ORIGIN — did you mean JANUS_WEBAUTHN_ORIGINS?
                            fix: correct the spelling or unset the variable; a misspelled variable
                            never takes effect and never errors
```

`doctor` names the closest legitimate variable when the difference is small
enough to be a confident suggestion, and lists the name alone when it is not.
This check needs neither a database nor a server, so it works anywhere:

```sh
janus doctor --offline
```

## Everything returns `503 sealed`

**Symptom.** The UI shows the unseal screen, or every secret route returns
`503 {"error":{"code":"sealed"}}` — but `/v1/sys/live`, `/v1/sys/health` and
`/v1/sys/seal-status` all answer normally.

**Cause.** This is not a fault. **Janus always boots sealed**: the master key is
not in memory, so nothing that touches a secret can work until it is supplied.
Health and liveness deliberately keep answering while sealed, so orchestrators
can tell "process wedged" from "not ready" — which is why the deployment guide
points liveness at `/v1/sys/live` and readiness at `/v1/sys/ready`.

**Distinguish the three states.**

```
  WARN  server.status     http://127.0.0.1:8200 is running but the seal is NOT INITIALIZED
  WARN  server.status     http://127.0.0.1:8200 is running but SEALED — every secret operation returns 503
  PASS  server.status     http://127.0.0.1:8200 is running and unsealed
```

**Fix.**

- *Not initialized* — a brand-new database. Run `janus init` once. It prints the
  Shamir shares and the initial admin credential **exactly once**; store each
  share separately.
- *Sealed* — the normal state after any restart with a Shamir seal. Run
  `janus unseal` once per share until the threshold is met (the share is read
  from stdin with echo off).
- *Sealed with a KMS seal* — auto-unseal failed at boot and the server logged a
  warning. Check the KMS credentials and key policy, then retry with
  `janus unseal` (an empty-body retry for KMS seals). `doctor`'s `seal.type`
  check confirms the provider variables are present before you go looking
  further.

## The server refuses to start with a seal-type mismatch

**Symptom.** `janus server` exits immediately with
`seal type mismatch: JANUS_SEAL_TYPE="awskms" but stored seal is "shamir"`.

**Cause.** Once the seal is initialized, the **stored** type is authoritative —
it describes how the master key on disk is actually wrapped. `JANUS_SEAL_TYPE`
is only used to choose the type at `janus init` time; afterwards it must either
match the stored value or be left unset. A mismatch usually means a deployment
template was updated ahead of an actual seal migration, or a config was copied
between two instances.

**Find it** (before the restart, not after):

```
  FAIL  seal.type         seal type mismatch: JANUS_SEAL_TYPE="awskms" but the stored seal is "shamir" — the server refuses to boot
                            fix: set JANUS_SEAL_TYPE=shamir to match the stored seal, or unset it (the stored type is authoritative)
```

**Fix.** Set the variable to the stored type, or drop it from the deployment.
Actually *changing* seal types is a separate, deliberate operation — see the
master-key and seal sections of
[master-key-and-backup.md](./master-key-and-backup.md).

The same check also catches an incomplete auto-unseal configuration before it
becomes a boot failure:

```
  FAIL  seal.type         seal type "azurekv" is missing required configuration: JANUS_AZURE_KEY_NAME
```

## The server cannot reach the database

**Symptom.** `janus server` exits with a `store: ping` or `store: parse dsn`
error, or `/v1/sys/ready` returns `503 {"code":"db_unavailable"}`.

**Find it.** `doctor` splits this into four checks so the failure is specific:

| Check | Answers |
| --- | --- |
| `db.dsn` | Is `JANUS_DATABASE_URL` set and URL-shaped? (`golang-migrate` requires a URL, not a libpq keyword string.) |
| `db.sslmode` | Does the connection to a non-local database actually use TLS? |
| `db.pool` | Are the `JANUS_DB_*` pool knobs coherent — min below max, idle below lifetime? |
| `db.connect` | Does Postgres accept a connection from this host, with these credentials? |

```
  FAIL  db.connect        cannot reach the database: store: ping: failed to connect ...
                            target: postgres://janus:[redacted]@db.internal:5432/janus?sslmode=require
                            fix: check the host, port, credentials and that Postgres accepts connections from this host
```

**Common causes, in the order worth checking.**

1. **Wrong host from where the process runs.** Inside compose the database is
   `postgres:5432`; from the host it is `127.0.0.1:5433`. A DSN copied across
   that boundary parses fine and never connects.
2. **`pg_hba.conf` rejects the source address**, or requires a different auth
   method than the DSN offers.
3. **TLS mismatch.** `sslmode=require` against a server without TLS fails; the
   default (`prefer`) silently falls back to plaintext, which is why `doctor`
   warns when it is unset for a non-local host:

   ```
     WARN  db.sslmode      sslmode is unset for a non-local database (db.internal); the default "prefer" silently falls back to plaintext
                             fix: set ?sslmode=require (or verify-full) explicitly in JANUS_DATABASE_URL
   ```

### Schema problems

`db.migrations` compares the applied schema version with the set embedded in the
binary you are running:

- **`schema is current (version 44)`** — nothing to do.
- **`N migration(s) pending`** — the database is behind this binary. Migrations
  apply automatically at server boot; run `janus migrate` if you want them
  applied first.
- **`the database has no schema yet`** — a fresh Postgres. Expected before the
  first boot.
- **`the database was migrated by a NEWER janus`** — a rollback that left the
  schema ahead of the binary. Downgrading the schema is not supported: run the
  newer binary, or restore the database from a backup taken at this version.
- **`the schema_migrations table is DIRTY`** — a migration failed part-way. Do
  **not** back up or restore over a dirty schema; resolve the failed migration
  or restore from a known-good backup. See
  [backup-and-restore.md](./backup-and-restore.md).

## TLS and ACME mistakes

Janus serves plain HTTP by default and expects TLS to terminate at a reverse
proxy or ingress. The native listener is opt-in and has **two mutually exclusive
modes**: static certificates (`JANUS_TLS_CERT` + `JANUS_TLS_KEY`) or ACME
(`JANUS_TLS_ACME_DOMAINS`).

**Both halves, or neither.** Setting one of the static pair is a boot failure:

```
  FAIL  tls               TLS static certs require both JANUS_TLS_CERT and JANUS_TLS_KEY (only one was set)
```

**Variables that quietly do nothing.** `JANUS_TLS_ACME_EMAIL` and
`JANUS_TLS_ACME_CACHE` are only consulted in ACME mode;
`JANUS_TLS_REDIRECT_HTTP` only in the static-certificate path (ACME runs its own
`:80` handler). Setting them in the wrong mode is a no-op, so `doctor` says so
rather than let you believe a redirect is running:

```
  WARN  tls               JANUS_TLS_REDIRECT_HTTP set but ignored in the active TLS mode
```

**Expiry.** Static certificates do not renew themselves. `doctor` reads the leaf
and reports the window, fails on an expired one, and warns three weeks out:

```
  FAIL  tls               the TLS certificate EXPIRED on 2026-07-01T00:00:00Z
  WARN  tls               the TLS certificate expires in 12 day(s)
```

**Hostname coverage.** When passkeys are configured, `doctor` verifies the
certificate covers `JANUS_WEBAUTHN_RP_ID` — the one hostname a browser is
guaranteed to use:

```
  WARN  tls               the TLS certificate does not cover "janus.example.com" (the configured passkey RP ID)
```

**ACME that never issues.** Let's Encrypt's HTTP-01 challenge needs inbound
`:80` reaching this process and a public DNS record for every domain in
`JANUS_TLS_ACME_DOMAINS`. `doctor` reports the requirement; it cannot verify
inbound reachability from inside. Check the security group / firewall and that
nothing else holds `:80`.

## Outbound integrations fail or behave oddly

Notification webhooks, rotation webhooks, sync providers, SMTP, and OIDC
discovery all dial through a shared hardened dialer that validates the
**resolved** IP at connect time. Link-local and cloud-metadata ranges are always
rejected. Loopback and RFC1918/ULA are allowed by default, because a self-hosted
deployment legitimately talks to internal targets.

If an integration cannot reach an internal target, check whether someone set
`JANUS_OUTBOUND_BLOCK_PRIVATE=true` — `doctor`'s `outbound.ssrf` check reports
the effective policy in full. And if the proxy escape hatch is on, it says so
loudly, because it degrades the guard:

```
  WARN  outbound.ssrf     outbound calls go through a proxy: the resolved-IP guard cannot see the real
                          destination, so metadata/link-local blocking now applies only to literal-IP targets
                            proxy variables in effect: HTTPS_PROXY
                            fix: unset JANUS_OUTBOUND_ALLOW_PROXY to restore the full guard, or enforce
                            destination allowlisting on the proxy itself
```

## Using `doctor` in CI and healthchecks

**Deploy gate** — validate the environment before rolling out, without touching
the database:

```sh
janus doctor --offline --strict
```

**Post-deploy verification** — full checks, including the database and the
running server:

```sh
janus doctor --strict
```

**Machine consumption** — `--json` emits a stable document:

```sh
janus doctor --json | jq -r '.checks[] | select(.status != "PASS") | "\(.status) \(.name): \(.summary)"'
```

```json
{
  "version": "v0.2.0",
  "checks": [
    {
      "name": "webauthn.origins",
      "status": "WARN",
      "summary": "1 configured passkey origin(s) do not describe how this server is reached",
      "detail": ["http://localhost:8210 names port 8210, but this server listens on port 8212 — …"],
      "fix": "set JANUS_WEBAUTHN_ORIGINS to the exact scheme://host:port a browser uses to load the Janus UI — …"
    }
  ],
  "summary": { "pass": 17, "warn": 1, "fail": 0, "skip": 0 },
  "ok": true
}
```

## Still stuck

- Turn up the log level for one restart: `JANUS_LOG_LEVEL=debug`. Leave it at
  `info` afterwards.
- Check the health panel (**Settings → Health**, backed by `GET /v1/sys/status`)
  for scheduler ticks, audit-chain head, and pool state — see
  [observability.md](./observability.md).
- Verify the audit chain: `GET /v1/audit/verify`, surfaced in the UI as the
  "chain verified" badge.
- For a suspected security issue, follow [SECURITY.md](../../SECURITY.md)
  rather than opening a public issue.
