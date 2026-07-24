import type { JanusClient, RequestOptions } from "./client.js";

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
    if (!this.id) {
      throw new Error("janus: lease has no ID");
    }
    const path = `/v1/dynamic/leases/${encodeURIComponent(this.id)}/renew`;
    const view = await this.client.request<LeaseView>("POST", path, options);
    if (view?.expires_at) {
      const next = new Date(view.expires_at);
      if (!Number.isNaN(next.getTime())) {
        this.expiresAt = next;
      }
    }
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
