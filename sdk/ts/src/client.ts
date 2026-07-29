import { JanusNotFoundError, JanusRevokeError, parseApiError } from "./errors.js";
import { Lease, type LeaseData } from "./dynamic.js";
import type { AutoRenewOptions } from "./autorenew.js";
import {
  groupCreateBody,
  parseGroup,
  parseGroupMember,
  validateGroupInput,
  type Group,
  type GroupInput,
  type GroupMember,
  type GroupMemberWire,
  type GroupWire,
} from "./groups.js";

/** The default cache TTL (30 seconds) when `cacheTtlMs` is not supplied. */
export const DEFAULT_CACHE_TTL_MS = 30_000;

/**
 * The `fetch` signature the client depends on. Node 18+ and modern runtimes
 * provide a global `fetch`; tests inject a fake implementing this shape.
 */
export type FetchLike = (
  input: string | URL,
  init?: RequestInit,
) => Promise<Response>;

/** Options for {@link JanusClient}. Mirrors the Go SDK's `With*` options. */
export interface JanusClientOptions {
  /** Base URL of the Janus server, e.g. `https://janus.example.com`. The `/v1` prefix is added automatically. */
  baseUrl: string;
  /** A `janus_svc_...` service token, sent as `Authorization: Bearer <token>`. */
  token?: string;
  /**
   * In-memory cache TTL for config reads, in milliseconds. Defaults to
   * {@link DEFAULT_CACHE_TTL_MS} (30s). Pass `0` (or a negative value) to
   * disable caching entirely — every read then hits the server.
   */
  cacheTtlMs?: number;
  /**
   * A custom `fetch` implementation (injectable for tests or to control
   * transport/TLS). Defaults to the global `fetch`.
   */
  fetch?: FetchLike;
  /**
   * Clock used for cache expiry, in epoch milliseconds. Overridable in tests to
   * make TTL behaviour deterministic. Defaults to `Date.now`.
   */
  now?: () => number;
}

interface CacheEntry {
  secrets: Record<string, string>;
  expiresAt: number;
}

/** Value-bearing shape of `GET /v1/configs/{cid}/secrets?reveal=true`. */
interface BatchRevealResponse {
  version?: number;
  secrets?: Record<string, string>;
}

/** Per-request options common to the read/dynamic methods. */
export interface RequestOptions {
  /** An {@link AbortSignal} to cancel the underlying HTTP request. */
  signal?: AbortSignal;
}

/**
 * Options for {@link JanusClient.withDynamic}. Everything from
 * {@link AutoRenewOptions} tunes the background renewal that runs for the
 * duration of the callback.
 */
export interface WithDynamicOptions extends AutoRenewOptions {
  /** Milliseconds allowed for the final revoke. Defaults to 30 000. */
  revokeTimeoutMs?: number;
}

/** Default budget for the final revoke in {@link JanusClient.withDynamic}. */
const DEFAULT_REVOKE_TIMEOUT_MS = 30_000;

/**
 * A typed client for the Janus secrets manager's `/v1` REST API.
 *
 * It reads secrets through an in-process, **memory-only** TTL cache and offers
 * optional dynamic-credential lease management. It never imports the Janus
 * server's internal packages, never writes secret values to disk, and no method
 * logs secret values. Reads go through the audited reveal endpoint, so every
 * {@link JanusClient.getSecret}/{@link JanusClient.getSecrets} on a cache miss
 * is recorded server-side as a `secret.reveal` event — that is intentional.
 *
 * @example
 * ```ts
 * const client = new JanusClient({
 *   baseUrl: "https://janus.example.com",
 *   token: process.env.JANUS_TOKEN,
 * });
 * const secrets = await client.getSecrets(configId);
 * // use secrets.DATABASE_URL — never log the value
 * ```
 */
export class JanusClient {
  private readonly baseUrl: string;
  private readonly token: string;
  private readonly cacheTtlMs: number;
  private readonly fetchImpl: FetchLike;
  private readonly now: () => number;
  private readonly cache = new Map<string, CacheEntry>();

  constructor(options: JanusClientOptions) {
    const baseUrl = options.baseUrl?.trim();
    if (!baseUrl) {
      throw new Error("janus: baseUrl is required");
    }
    // Validate the URL early so misconfiguration fails fast.
    // eslint-disable-next-line no-new
    new URL(baseUrl);

    const fetchImpl = options.fetch ?? (globalThis.fetch as FetchLike | undefined);
    if (!fetchImpl) {
      throw new Error(
        "janus: no fetch available — pass options.fetch or run on Node 18+ / a runtime with global fetch",
      );
    }

    this.baseUrl = baseUrl.replace(/\/+$/, "");
    this.token = options.token ?? "";
    this.cacheTtlMs = options.cacheTtlMs ?? DEFAULT_CACHE_TTL_MS;
    this.fetchImpl = fetchImpl;
    this.now = options.now ?? Date.now;
  }

  /**
   * Return a config's resolved secrets as a key/value object. References are
   * resolved server-side. Results are cached in memory for the configured TTL;
   * within the TTL, repeated calls return the cached object without hitting the
   * server. This is an audited reveal (`secret.reveal`) on cache miss.
   *
   * The returned object is a fresh copy; mutating it does not affect the cache.
   */
  async getSecrets(
    configId: string,
    options: RequestOptions = {},
  ): Promise<Record<string, string>> {
    if (!configId) {
      throw new Error("janus: configId is required");
    }

    if (this.cacheTtlMs > 0) {
      const entry = this.cache.get(configId);
      if (entry && this.now() < entry.expiresAt) {
        return { ...entry.secrets };
      }
    }

    const secrets = await this.fetchSecrets(configId, options);

    if (this.cacheTtlMs > 0) {
      this.cache.set(configId, {
        secrets: { ...secrets },
        expiresAt: this.now() + this.cacheTtlMs,
      });
    }
    return { ...secrets };
  }

  /**
   * Return a single resolved secret value from a config. When caching is enabled
   * and the config is already cached (and fresh), the value is served from the
   * cached batch; otherwise the config is fetched (and cached) via the batch
   * reveal. A missing key rejects with {@link JanusNotFoundError}.
   */
  async getSecret(
    configId: string,
    key: string,
    options: RequestOptions = {},
  ): Promise<string> {
    if (!configId) {
      throw new Error("janus: configId is required");
    }
    if (!key) {
      throw new Error("janus: key is required");
    }
    const secrets = await this.getSecrets(configId, options);
    if (!Object.prototype.hasOwnProperty.call(secrets, key)) {
      throw new JanusNotFoundError(404, "not_found", "secret key not found");
    }
    return secrets[key];
  }

  /**
   * Evict the cached secrets for a config so the next read re-fetches from the
   * server. Call with no argument to clear the entire cache.
   */
  refresh(configId?: string): void {
    if (configId === undefined || configId === "") {
      this.cache.clear();
      return;
    }
    this.cache.delete(configId);
  }

  /**
   * Issue a new dynamic credential lease for the given dynamic role ID
   * (`POST /v1/dynamic/roles/{id}/creds`). The returned {@link Lease} carries the
   * one-time password; hold it in memory only.
   *
   * Note: `roleId` identifies a dynamic role, not a config. Dynamic roles are
   * authored via the admin API; see `docs/guides/typescript-sdk.md`.
   */
  async issueDynamic(
    roleId: string,
    options: RequestOptions = {},
  ): Promise<Lease> {
    if (!roleId) {
      throw new Error("janus: roleId is required");
    }
    const path = `/v1/dynamic/roles/${encodeURIComponent(roleId)}/creds`;
    const data = await this.request<LeaseData>("POST", path, options);
    return new Lease(this, data);
  }

  /**
   * Issue a dynamic credential lease, keep it renewed in the background for as
   * long as `fn` runs, and revoke it on the way out — on success, on a thrown
   * error, and on an early return. It is the recommended way to use dynamic
   * credentials: no lease is left dangling and nothing has to remember to renew.
   *
   * @example
   * ```ts
   * const rows = await client.withDynamic(roleId, async (lease, signal) => {
   *   const pool = new Pool({ user: lease.username, password: lease.password });
   *   try {
   *     return await query(pool, signal);
   *   } finally {
   *     await pool.end();
   *   }
   * });
   * ```
   *
   * `fn` receives an {@link AbortSignal} that is aborted when auto-renew
   * terminates — the lease hit its max TTL, was revoked out from under us, or
   * the token lost access — so long-running work can wind down before the
   * credentials stop working. Pass `onEvent` to see why.
   *
   * Error contract: the error `fn` throws is never replaced. If the final
   * revoke also fails, that failure is reported to `onEvent` with reason
   * `"revoke_failed"` and attached to the re-thrown error as a `revokeError`
   * property; `fn`'s error is what propagates. If `fn` succeeded and only the
   * revoke failed, a {@link JanusRevokeError} is thrown.
   */
  async withDynamic<T>(
    roleId: string,
    fn: (lease: Lease, signal: AbortSignal) => Promise<T> | T,
    options: WithDynamicOptions = {},
  ): Promise<T> {
    if (typeof fn !== "function") {
      throw new Error("janus: fn is required");
    }
    const lease = await this.issueDynamic(roleId, { signal: options.signal });

    // From here the lease exists server-side, so every exit path must revoke it.
    const renewer = lease.startAutoRenew(options);
    const fnController = new AbortController();
    void renewer.done.then(() => fnController.abort());

    let result: T | undefined;
    let failure: { error: unknown } | undefined;
    try {
      result = await fn(lease, fnController.signal);
    } catch (error) {
      failure = { error };
    } finally {
      fnController.abort();
      await renewer.stop();
    }

    let revokeError: unknown;
    try {
      await this.revokeWithTimeout(lease, options.revokeTimeoutMs);
    } catch (error) {
      revokeError = error;
    }

    if (revokeError !== undefined) {
      const wrapped = new JanusRevokeError(lease.id, revokeError);
      if (failure) {
        options.onEvent?.({
          leaseId: lease.id,
          renewed: false,
          expiresAt: lease.expiresAt,
          error: wrapped,
          terminal: true,
          reason: "revoke_failed",
        });
        // Surface the revoke failure without replacing the caller's error.
        if (typeof failure.error === "object" && failure.error !== null) {
          Object.defineProperty(failure.error, "revokeError", {
            value: wrapped,
            enumerable: false,
            configurable: true,
            writable: true,
          });
        }
      } else {
        throw wrapped;
      }
    }

    if (failure) {
      throw failure.error;
    }
    return result as T;
  }

  private async revokeWithTimeout(lease: Lease, timeoutMs?: number): Promise<void> {
    const budget = timeoutMs && timeoutMs > 0 ? timeoutMs : DEFAULT_REVOKE_TIMEOUT_MS;
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), budget) as unknown as {
      unref?: () => void;
    };
    if (typeof timer?.unref === "function") {
      timer.unref();
    }
    try {
      await lease.revoke({ signal: controller.signal });
    } finally {
      clearTimeout(timer as unknown as ReturnType<typeof setTimeout>);
    }
  }

  // ---- group catalog -----------------------------------------------------
  //
  // Everything below needs instance-scoped `group:manage` (admin or owner) —
  // except myGroups, which is authenticated-only. A config- or
  // environment-scoped read token gets a JanusForbiddenError.
  //
  // Group BINDINGS (granting a group a role at a scope) are deliberately absent:
  // they are a different authority (`member:manage` at that scope, capped by
  // your own bound role), and a durable grant of access belongs in something
  // that plans and diffs. Use Terraform's `janus_group_binding`, `janus group
  // bind`, or the UI.

  /**
   * List every group in the catalog, following cursor pagination.
   *
   * Needs instance `group:manage`.
   */
  async listGroups(options: RequestOptions = {}): Promise<Group[]> {
    const out: Group[] = [];
    let cursor = "";
    for (;;) {
      let path = "/v1/groups?limit=100";
      if (cursor) path += `&cursor=${encodeURIComponent(cursor)}`;
      const page = await this.request<{
        groups?: GroupWire[];
        next_cursor?: string | null;
      }>("GET", path, options);
      for (const g of page.groups ?? []) out.push(parseGroup(g));
      if (!page.next_cursor) return out;
      cursor = page.next_cursor;
    }
  }

  /**
   * Fetch one group. Needs instance `group:manage`.
   */
  async getGroup(groupId: string, options: RequestOptions = {}): Promise<Group> {
    if (!groupId) {
      throw new Error("janus: groupId is required");
    }
    const body = await this.request<{ group?: GroupWire }>(
      "GET",
      `/v1/groups/${encodeURIComponent(groupId)}`,
      options,
    );
    return parseGroup(body.group ?? {});
  }

  /**
   * Create a group. The kind/claim pairing is checked locally first, so an
   * `oidc` group with no claim value (or a `local` one with a claim) fails
   * without a round trip. Needs instance `group:manage`.
   */
  async createGroup(input: GroupInput, options: RequestOptions = {}): Promise<Group> {
    validateGroupInput(input);
    const body = await this.request<GroupWire>(
      "POST",
      "/v1/groups",
      options,
      groupCreateBody(input),
    );
    return parseGroup(body ?? {});
  }

  /**
   * Delete a group. Membership and every binding it conferred cascade, so the
   * access is gone on the next request — Janus resolves permissions per request
   * and never freezes them into a session. Needs instance `group:manage`.
   */
  async deleteGroup(groupId: string, options: RequestOptions = {}): Promise<void> {
    if (!groupId) {
      throw new Error("janus: groupId is required");
    }
    await this.request<void>("DELETE", `/v1/groups/${encodeURIComponent(groupId)}`, options);
  }

  /**
   * Toggle a group's delegated project-creation capability.
   * Needs instance `group:manage`.
   */
  async setGroupProjectCreation(
    groupId: string,
    allowed: boolean,
    options: RequestOptions = {},
  ): Promise<void> {
    if (!groupId) {
      throw new Error("janus: groupId is required");
    }
    await this.request<void>(
      "PUT",
      `/v1/groups/${encodeURIComponent(groupId)}/capabilities`,
      options,
      { can_create_projects: allowed },
    );
  }

  /**
   * List a group's recorded members, following cursor pagination.
   * Needs instance `group:manage`.
   *
   * For an `oidc` group this is the login-time snapshot: it covers only users
   * who have signed in, so treat it as "members seen at sign-in", never as the
   * group's membership. The identity provider is the record for those groups.
   */
  async listGroupMembers(
    groupId: string,
    options: RequestOptions = {},
  ): Promise<GroupMember[]> {
    if (!groupId) {
      throw new Error("janus: groupId is required");
    }
    const out: GroupMember[] = [];
    let cursor = "";
    for (;;) {
      let path = `/v1/groups/${encodeURIComponent(groupId)}/members?limit=100`;
      if (cursor) path += `&cursor=${encodeURIComponent(cursor)}`;
      const page = await this.request<{
        members?: GroupMemberWire[];
        next_cursor?: string | null;
      }>("GET", path, options);
      for (const m of page.members ?? []) out.push(parseGroupMember(m));
      if (!page.next_cursor) return out;
      cursor = page.next_cursor;
    }
  }

  /**
   * Add a user to a **local** group. Needs instance `group:manage`.
   *
   * An `oidc` group is refused with HTTP 409: its membership comes from the
   * identity provider and is refreshed at each sign-in, and the database schema
   * makes a hand-added row unrepresentable. Check `kind` first if you want to
   * fail before the request.
   */
  async addGroupMember(
    groupId: string,
    userId: string,
    options: RequestOptions = {},
  ): Promise<void> {
    if (!groupId || !userId) {
      throw new Error("janus: groupId and userId are required");
    }
    await this.request<void>("PUT", groupMemberPath(groupId, userId), options);
  }

  /**
   * Remove a user from a local group. Effective on that user's next request.
   * Needs instance `group:manage`.
   */
  async removeGroupMember(
    groupId: string,
    userId: string,
    options: RequestOptions = {},
  ): Promise<void> {
    if (!groupId || !userId) {
      throw new Error("janus: groupId and userId are required");
    }
    await this.request<void>("DELETE", groupMemberPath(groupId, userId), options);
  }

  /**
   * The groups the **caller** belongs to.
   *
   * Unlike the rest of the group methods this needs no special authority — it is
   * authenticated-only, because it reveals nothing but the caller's own
   * memberships and never the catalog. A service token belongs to no groups and
   * gets an empty array rather than an error, so it is safe to call
   * unconditionally.
   */
  async myGroups(options: RequestOptions = {}): Promise<Group[]> {
    const body = await this.request<{ groups?: GroupWire[] }>(
      "GET",
      "/v1/auth/me/groups",
      options,
    );
    return (body.groups ?? []).map(parseGroup);
  }

  private async fetchSecrets(
    configId: string,
    options: RequestOptions,
  ): Promise<Record<string, string>> {
    const path = `/v1/configs/${encodeURIComponent(configId)}/secrets?reveal=true`;
    const body = await this.request<BatchRevealResponse>("GET", path, options);
    return body.secrets ?? {};
  }

  /**
   * Perform an HTTP request against the Janus API: add the bearer token,
   * JSON-encode `body` when present, parse a JSON success body into `T`, and
   * translate non-2xx responses into typed errors. Used by the read, dynamic and
   * group methods.
   *
   * @internal
   */
  async request<T>(
    method: string,
    path: string,
    options: RequestOptions = {},
    body?: unknown,
  ): Promise<T> {
    const headers: Record<string, string> = { Accept: "application/json" };
    if (this.token) {
      headers.Authorization = `Bearer ${this.token}`;
    }
    let payload: string | undefined;
    if (body !== undefined) {
      headers["Content-Type"] = "application/json";
      payload = JSON.stringify(body);
    }

    const res = await this.fetchImpl(this.baseUrl + path, {
      method,
      headers,
      body: payload,
      signal: options.signal,
    });

    if (!res.ok) {
      throw await parseApiError(res);
    }

    // 204 / empty body: nothing to decode.
    if (res.status === 204) {
      return undefined as T;
    }
    const text = await res.text();
    if (!text) {
      return undefined as T;
    }
    return JSON.parse(text) as T;
  }
}

/** Path of one membership row: `/v1/groups/{gid}/members/{uid}`. */
function groupMemberPath(groupId: string, userId: string): string {
  return `/v1/groups/${encodeURIComponent(groupId)}/members/${encodeURIComponent(userId)}`;
}
