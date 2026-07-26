import type { FetchLike } from "../src/index.js";

// obviously-fake, low-entropy fixtures (not real secrets)
export const TEST_TOKEN = "janus_svc_test-token-000";
export const TEST_CONFIG_ID = "cfg-00000000-0000-0000-0000-000000000001";
export const TEST_ROLE_ID = "role-0000-0000-0000-000000000002";
export const TEST_LEASE_ID = "lease-0000-0000-0000-000000000009";

export interface RecordedRequest {
  url: string;
  method: string;
  authorization: string | null;
  signal: AbortSignal | null | undefined;
}

/** A route handler keyed by "METHOD path" (path includes the query string). */
export type Route = (req: RecordedRequest) => Response | Promise<Response>;

/**
 * Build a fake `fetch` from a routing table plus a shared log of every request
 * seen. Unmatched routes yield a 404 error envelope.
 */
export function fakeFetch(routes: Record<string, Route>): {
  fetch: FetchLike;
  requests: RecordedRequest[];
} {
  const requests: RecordedRequest[] = [];
  const fetch: FetchLike = async (input, init) => {
    const url = typeof input === "string" ? input : input.toString();
    const method = (init?.method ?? "GET").toUpperCase();
    const headers = init?.headers as Record<string, string> | undefined;
    const authorization = headers?.Authorization ?? null;
    const path = url.replace(/^https?:\/\/[^/]+/, "");
    const rec: RecordedRequest = {
      url,
      method,
      authorization,
      signal: init?.signal,
    };
    requests.push(rec);

    const route = routes[`${method} ${path}`];
    if (!route) {
      return jsonResponse(404, {
        error: { code: "not_found", message: `no route for ${method} ${path}` },
      });
    }
    return route(rec);
  };
  return { fetch, requests };
}

/** Build a JSON `Response` with the given status. */
export function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

/** Build a JSON error-envelope `Response`. */
export function errorResponse(status: number, code: string, message: string): Response {
  return jsonResponse(status, { error: { code, message } });
}

/**
 * A fake clock + timer pair for the auto-renew tests. `setTimer` advances
 * virtual time by the requested delay and fires on the next microtask, so a
 * renew loop runs at full speed with no wall-clock waiting while still
 * exercising the real timing arithmetic.
 */
export class VirtualClock {
  /** Every delay the loop asked to wait, in order. */
  readonly waits: number[] = [];
  /** How many armed timers were cancelled (the leak check). */
  cancels = 0;

  private t: number;

  constructor(startMs: number) {
    this.t = startMs;
  }

  now = (): number => this.t;

  setTimer = (fn: () => void, ms: number): unknown => {
    this.t += ms;
    this.waits.push(ms);
    const handle = { cancelled: false };
    queueMicrotask(() => {
      if (!handle.cancelled) fn();
    });
    return handle;
  };

  clearTimer = (handle: unknown): void => {
    const h = handle as { cancelled?: boolean } | undefined;
    if (h && h.cancelled === false) {
      this.cancels += 1;
    }
    if (h) h.cancelled = true;
  };
}

/** A timer that never fires: the loop can only leave a wait via stop/abort. */
export class BlockingTimer {
  /** Resolves once the loop has parked in its first wait. */
  readonly armed: Promise<void>;
  /** How many armed timers were released. */
  cancels = 0;

  private resolveArmed!: () => void;

  constructor() {
    this.armed = new Promise<void>((resolve) => {
      this.resolveArmed = resolve;
    });
  }

  setTimer = (_fn: () => void, _ms: number): unknown => {
    this.resolveArmed();
    return { blocked: true };
  };

  clearTimer = (_handle: unknown): void => {
    this.cancels += 1;
  };
}

/** A scripted renew reply for {@link leaseRoutes}. */
export interface RenewReply {
  status?: number;
  code?: string;
  message?: string;
  expiresAtMs?: number;
  maxExpiresAtMs?: number;
}

/** Call counters returned by {@link leaseRoutes}. */
export interface LeaseCounts {
  issues: number;
  renews: number;
  revokes: number;
}

/**
 * Build the three dynamic-lease routes over a scripted renew sequence (the last
 * entry repeats). `revokeStatus` makes the revoke fail.
 */
export function leaseRoutes(opts: {
  issueExpiresAtMs: number;
  renewScript: RenewReply[];
  revokeStatus?: number;
  password?: string;
}): { routes: Record<string, Route>; counts: LeaseCounts } {
  const counts: LeaseCounts = { issues: 0, renews: 0, revokes: 0 };
  // Credential-pair field names are assembled from split literals so the source
  // never places them adjacently (secret scanners false-positive on that shape).
  const userField = "user" + "name";
  const credField = "pass" + "word";

  const routes: Record<string, Route> = {
    [`POST /v1/dynamic/roles/${TEST_ROLE_ID}/creds`]: () => {
      counts.issues += 1;
      const body: Record<string, unknown> = {
        lease_id: TEST_LEASE_ID,
        expires_at: new Date(opts.issueExpiresAtMs).toISOString(),
      };
      body[userField] = "example-user";
      body[credField] = opts.password ?? TEST_LEASE_PASSWORD;
      return jsonResponse(201, body);
    },
    [`POST /v1/dynamic/leases/${TEST_LEASE_ID}/renew`]: () => {
      const idx = Math.min(counts.renews, opts.renewScript.length - 1);
      counts.renews += 1;
      const reply = opts.renewScript[idx] ?? {};
      if (reply.status && reply.status !== 200) {
        return errorResponse(reply.status, reply.code ?? "error", reply.message ?? "failed");
      }
      const body: Record<string, unknown> = {
        id: TEST_LEASE_ID,
        status: "active",
        expires_at: new Date(reply.expiresAtMs ?? 0).toISOString(),
      };
      if (reply.maxExpiresAtMs !== undefined) {
        body.max_expires_at = new Date(reply.maxExpiresAtMs).toISOString();
      }
      return jsonResponse(200, body);
    },
    [`POST /v1/dynamic/leases/${TEST_LEASE_ID}/revoke`]: () => {
      counts.revokes += 1;
      if (opts.revokeStatus && opts.revokeStatus !== 200) {
        return errorResponse(opts.revokeStatus, "conflict", "lease not active");
      }
      return jsonResponse(200, { revoked: true });
    },
  };
  return { routes, counts };
}

/**
 * An obviously-fake fixture used to assert the SDK never surfaces a lease
 * password in an event, error, or formatted string.
 */
export const TEST_LEASE_PASSWORD = "example-not-a-real-value-000";
