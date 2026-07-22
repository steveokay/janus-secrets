# Account lockout / progressive backoff — design

**Date:** 2026-07-22
**Status:** approved (all recommendations accepted 2026-07-22)

## Problem

Janus has a per-IP token-bucket limiter (`internal/api/ratelimit.go`, 10/min
sustained, burst 5) on the login/TOTP/password endpoints, and a manual admin
disable (`users.disabled_at`). Nothing tracks *per-account* failed logins, so a
determined attacker can target one known email from many IPs (or under the
per-IP budget) indefinitely. This closes the last auth-hardening gap after
session management and TOTP.

## Model (decided)

**Progressive temporary lockout**, persisted in Postgres.

- After `threshold` consecutive failed attempts for an existing, enabled user,
  the account is locked for a window that escalates with each successive
  lockout, capped at a maximum. The lock **auto-expires**; no admin action is
  required to recover.
- The failure counter and escalation level **reset to zero on a successful
  login**.
- While an account is already locked, further attempts are rejected **without
  extending** the window — so an attacker who knows a victim's email cannot
  weaponise lockout into an indefinite denial of service. (This is why we chose
  progressive-temporary over hard-lock-until-admin.)
- An admin can unlock early.

### Defaults (env-configurable)

| Env | Default | Meaning |
|---|---|---|
| `JANUS_LOCKOUT_ENABLED` | `true` | Master switch. |
| `JANUS_LOCKOUT_THRESHOLD` | `5` | Consecutive failures before the first lock. |
| `JANUS_LOCKOUT_BASE` | `1m` | First lockout window. |
| `JANUS_LOCKOUT_MAX` | `1h` | Cap on the window. |

Escalation schedule: window(level) = min(MAX, BASE × 5^(level−1)), i.e. with
defaults 1m → 5m → 25m → 1h (capped) → 1h … `level` increments on each lock and
resets on a successful login.

## Response behaviour (decided — no enumeration oracle)

Lockout state is revealed **only to a caller who supplies the correct
password**, mirroring the TOTP-required pattern:

- **Wrong password while locked** → the normal, byte-identical
  `invalid_credentials` (attacker cannot distinguish a locked account from a
  wrong password; account existence is not leaked).
- **Correct password while locked** → a distinct `account_locked` response
  (`429 Too Many Requests`, `Retry-After: <seconds>` header, message names the
  remaining window). Only the password-holder — almost certainly the real
  owner — learns the lock state.

The constant-time dummy-hash path for unknown/disabled users is preserved.

## Counting rules

For an existing, enabled user, an attempt is counted a **failure** (increments
the counter; may trigger a lock) when:

- the password is wrong, **or**
- the password is correct but a required TOTP/recovery code is wrong.

An attempt is **not** counted when:

- the password is correct and no TOTP code was supplied but TOTP is required
  (this returns `totp_required` — a challenge, not a failure), or
- the user is unknown or disabled (no row to track; the per-IP limiter and
  dummy-hash path cover these).

A **successful** login (password + second factor if any) resets the counter and
escalation level to zero.

While the account is already locked, the Login path verifies the password only
to choose the response message and does **not** increment or extend the lock.

## Data model

Migration `000026_account_lockout` adds to `users`:

```sql
ALTER TABLE users
  ADD COLUMN failed_login_count   int         NOT NULL DEFAULT 0,
  ADD COLUMN lockout_level        int         NOT NULL DEFAULT 0,
  ADD COLUMN locked_until         timestamptz,
  ADD COLUMN last_failed_login_at timestamptz;
```

1:1 with the user, low write volume. `userCols`/`scanUser`/`User` extended.

## Components

- **store (`internal/store/users.go`)** — `RecordFailedLogin(id) (locked bool,
  lockedUntil, error)` (atomic increment + conditional lock in one UPDATE),
  `ResetLoginFailures(id)`, and lock state on `Get`/`GetByEmail`/list.
  `AdminUnlock(id)` clears counter/level/locked_until. Lock evaluation uses
  `now()` in SQL to avoid clock skew.
- **policy (`internal/auth`)** — a `LockoutPolicy{Enabled, Threshold, Base,
  Max}` value on the `Service`, populated from env at construction (follow the
  existing `JANUS_*` parsing pattern, e.g. `JANUS_HTTP_*`/`JANUS_DYNAMIC_TICK`).
- **auth (`internal/auth/sessions.go`)** — `Login` gains the lock check
  (before granting), the reveal-on-correct-password logic, and failure
  counting/reset. New sentinel `ErrAccountLocked` (carrying the retry window).
  `Service.AdminUnlock(ctx, id)`.
- **api** — `POST /v1/users/{id}/unlock` → `handleUserUnlock` (authz
  `UserManage` on `Instance`, cannot-target-self mirror of disable, audit
  `user.unlock`); `handleLogin` maps `ErrAccountLocked` → `429 account_locked`
  + `Retry-After`; `handleUserList` surfaces `locked` / `locked_until`. Audit
  `auth.lockout` (value-free) emitted when a lock trips.
- **web** — Members page: a "Locked" badge + an Unlock action (admin-gated,
  DialogHost confirm) reusing the existing members query keys; `Login.svelte`
  shows the `account_locked` message with the retry window.

## Testing

- **store** — increment→lock at threshold; escalation window grows; reset on
  success; admin unlock; auto-expiry (locked_until in the past reads as
  unlocked); concurrent failures don't over/under count.
- **auth** — wrong password N× locks; correct password while locked →
  `ErrAccountLocked`; wrong password while locked → `ErrInvalidCredentials`
  (no reveal, no extend); success resets; correct-password+wrong-TOTP counts;
  `totp_required` challenge doesn't count; disabled/unknown users never tracked;
  `Enabled=false` disables the whole mechanism.
- **api e2e** — login lock trips at threshold and returns 429 + `Retry-After`
  only for the correct password; unlock endpoint authz (admin only, not self);
  lock state in the user list; leak test (no password/hash in audit or errors).

## Non-goals

- No distributed/shared lockout state (single-node topology, matching the IP
  limiter).
- No CLI (there is no `janus user` admin command group today; API + web only).
- No per-IP-scoped account lockout (the per-IP limiter already covers spray;
  account lockout is deliberately IP-independent so it can't be bypassed by
  rotating IPs).
