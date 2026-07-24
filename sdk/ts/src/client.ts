import { JanusNotFoundError, parseApiError } from "./errors.js";
import { Lease, type LeaseData } from "./dynamic.js";

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

  private async fetchSecrets(
    configId: string,
    options: RequestOptions,
  ): Promise<Record<string, string>> {
    const path = `/v1/configs/${encodeURIComponent(configId)}/secrets?reveal=true`;
    const body = await this.request<BatchRevealResponse>("GET", path, options);
    return body.secrets ?? {};
  }

  /**
   * Perform an HTTP request against the Janus API: add the bearer token, parse a
   * JSON success body into `T`, and translate non-2xx responses into typed
   * errors. Used by the read and dynamic methods.
   *
   * @internal
   */
  async request<T>(
    method: string,
    path: string,
    options: RequestOptions = {},
  ): Promise<T> {
    const headers: Record<string, string> = { Accept: "application/json" };
    if (this.token) {
      headers.Authorization = `Bearer ${this.token}`;
    }

    const res = await this.fetchImpl(this.baseUrl + path, {
      method,
      headers,
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
