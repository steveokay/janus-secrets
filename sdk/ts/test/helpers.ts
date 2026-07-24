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
