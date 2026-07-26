import type { JanusClient, RequestOptions } from "./client.js";
import { LeaseRenewer, type AutoRenewOptions } from "./autorenew.js";

/**
 * Raw JSON shape returned by the issue endpoint. The password is returned
 * exactly once, at issue time, and is never persisted or audited in plaintext
 * by the server; the SDK likewise holds it only in memory and never logs it.
 */
export interface LeaseData {
  lease_id: string;
  username: string;
  password: string;
  expires_at: string;
}

/** Metadata shape (no password) returned by the renew endpoint. */
interface LeaseView {
  id?: string;
  role_id?: string;
  status?: string;
  db_username?: string;
  expires_at?: string;
  max_expires_at?: string;
}

/** The timestamps parsed out of a renew response. */
export interface ParsedLeaseView {
  /** The lease's new expiry, when the server reported one. */
  expiresAt?: Date;
  /** The lease's hard ceiling, when the server reported one. */
  maxExpiresAt?: Date;
}

function toDate(value: string | undefined): Date | undefined {
  if (!value) {
    return undefined;
  }
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? undefined : d;
}

/**
 * A dynamic database credential lease issued by Janus.
 *
 * The {@link Lease.password} is returned exactly once, at issue time, and is
 * never persisted or audited in plaintext. This object holds it in memory only
 * and never logs it. {@link Lease.renew} and {@link Lease.revoke} operate on
 * this lease's ID.
 */
export class Lease {
  /** The lease ID (server-assigned). */
  readonly id: string;
  /** The generated database username. */
  readonly username: string;
  /** The one-time generated password. Held in memory only; never log it. */
  readonly password: string;
  /** Current expiry. Updated in place by {@link Lease.renew}. */
  expiresAt: Date;

  /**
   * The hard ceiling past which the server will not extend this lease, or
   * `undefined` while it is unknown. Janus reports it only on a renew response,
   * so it stays `undefined` until the first successful renew.
   */
  maxExpiresAt?: Date;

  private readonly client: JanusClient;

  /** @internal — construct leases via {@link JanusClient.issueDynamic}. */
  constructor(client: JanusClient, data: LeaseData) {
    this.client = client;
    this.id = data.lease_id;
    this.username = data.username;
    this.password = data.password;
    this.expiresAt = new Date(data.expires_at);
  }

  /**
   * Extend the lease's expiry (capped server-side at the role's max TTL) and
   * update {@link Lease.expiresAt} in place. Does not change the password.
   * Rejects with a {@link import("./errors.js").JanusError} on failure (e.g. 409
   * when the lease is no longer active).
   */
  async renew(options: RequestOptions = {}): Promise<void> {
    await this.renewView(options);
  }

  /**
   * Renew and return the parsed server view, so callers that care (the
   * background renewer) can see `max_expires_at`. Updates {@link Lease.expiresAt}
   * and {@link Lease.maxExpiresAt} in place.
   *
   * @internal
   */
  async renewView(options: RequestOptions = {}): Promise<ParsedLeaseView> {
    if (!this.id) {
      throw new Error("janus: lease has no ID");
    }
    const path = `/v1/dynamic/leases/${encodeURIComponent(this.id)}/renew`;
    const view = await this.client.request<LeaseView>("POST", path, options);
    const parsed: ParsedLeaseView = {};
    const next = toDate(view?.expires_at);
    if (next) {
      parsed.expiresAt = next;
      this.expiresAt = next;
    }
    const max = toDate(view?.max_expires_at);
    if (max) {
      parsed.maxExpiresAt = max;
      this.maxExpiresAt = max;
    }
    return parsed;
  }

  /**
   * Start a background loop that keeps this lease renewed, and return the
   * {@link LeaseRenewer} controlling it.
   *
   * Auto-renew is strictly opt-in: nothing in this SDK schedules a timer unless
   * you call this (or {@link JanusClient.withDynamic}, which calls it for you).
   * The caller owns the renewer and **must** `await renewer.stop()`, or the
   * timer lives until the lease reaches a terminal state.
   *
   * Policy: after each successful renew the loop waits ~2/3 of the remaining
   * TTL (± 10% jitter, floored at 1s) and renews again. See
   * {@link AutoRenewOptions} to tune it.
   *
   * The loop ends — permanently, emitting one terminal event — when `stop()` is
   * called, the `signal` aborts, the server will not extend the lease further
   * (max TTL, `"max_ttl"`), the server rejects the renew non-retryably (401 /
   * 403 / 404 / 409 / other 4xx), or the expiry passes before a renew succeeds
   * (`"expired"`). Retryable failures — network errors, 5xx (including a sealed
   * server), 408, 429 — are reported as non-terminal events and retried while
   * TTL headroom remains. Nothing is swallowed: every failure reaches
   * `onEvent`, and the terminal one is also on `renewer.error`.
   */
  startAutoRenew(options?: AutoRenewOptions): LeaseRenewer {
    if (!this.id) {
      throw new Error("janus: lease has no ID");
    }
    return new LeaseRenewer(this, options);
  }

  /**
   * Revoke the lease immediately (drops the underlying database role). After a
   * successful revoke the credentials are no longer valid.
   */
  async revoke(options: RequestOptions = {}): Promise<void> {
    if (!this.id) {
      throw new Error("janus: lease has no ID");
    }
    const path = `/v1/dynamic/leases/${encodeURIComponent(this.id)}/revoke`;
    await this.client.request<void>("POST", path, options);
  }
}
