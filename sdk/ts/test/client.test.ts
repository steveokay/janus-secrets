import assert from "node:assert/strict";
import { test } from "node:test";

import {
  JanusClient,
  JanusError,
  JanusForbiddenError,
  JanusNotFoundError,
  JanusSealedError,
  JanusUnauthorizedError,
  isForbidden,
  isNotFound,
  isSealed,
  isUnauthorized,
} from "../src/index.js";
import {
  TEST_CONFIG_ID,
  TEST_TOKEN,
  errorResponse,
  fakeFetch,
  jsonResponse,
} from "./helpers.js";

const BASE = "https://janus.example.test";
const REVEAL_ROUTE = `GET /v1/configs/${TEST_CONFIG_ID}/secrets?reveal=true`;

function revealRoutes(secrets: Record<string, string>, counter?: { n: number }) {
  return {
    [REVEAL_ROUTE]: () => {
      if (counter) counter.n += 1;
      return jsonResponse(200, { version: 3, secrets });
    },
  };
}

test("getSecrets sends the bearer header and parses the reveal body", async () => {
  const { fetch, requests } = fakeFetch(
    revealRoutes({ DATABASE_URL: "postgres://fake", API_KEY: "abc-123-fake" }),
  );
  const client = new JanusClient({ baseUrl: BASE, token: TEST_TOKEN, fetch });

  const got = await client.getSecrets(TEST_CONFIG_ID);

  assert.equal(got.DATABASE_URL, "postgres://fake");
  assert.equal(got.API_KEY, "abc-123-fake");
  assert.equal(requests[0].authorization, `Bearer ${TEST_TOKEN}`);
  assert.match(requests[0].url, /reveal=true$/);
});

test("getSecret returns a single value and rejects a missing key", async () => {
  const { fetch } = fakeFetch(revealRoutes({ KEY_A: "val-a-fake" }));
  const client = new JanusClient({ baseUrl: BASE, token: TEST_TOKEN, fetch });

  assert.equal(await client.getSecret(TEST_CONFIG_ID, "KEY_A"), "val-a-fake");

  await assert.rejects(
    () => client.getSecret(TEST_CONFIG_ID, "MISSING"),
    (err: unknown) => isNotFound(err) && (err as JanusNotFoundError).status === 404,
  );
});

test("cache: hit within TTL, miss after expiry (controllable clock)", async () => {
  const counter = { n: 0 };
  const { fetch } = fakeFetch(revealRoutes({ K: "v-fake" }, counter));
  let now = 1_000_000;
  const client = new JanusClient({
    baseUrl: BASE,
    token: TEST_TOKEN,
    fetch,
    cacheTtlMs: 30_000,
    now: () => now,
  });

  await client.getSecrets(TEST_CONFIG_ID); // miss
  now += 29_000;
  await client.getSecrets(TEST_CONFIG_ID); // hit
  assert.equal(counter.n, 1, "expected 1 server hit within TTL");

  now += 2_000;
  await client.getSecrets(TEST_CONFIG_ID); // miss (TTL elapsed)
  assert.equal(counter.n, 2, "expected 2 server hits after TTL");
});

test("cache: cacheTtlMs=0 disables caching (every read hits)", async () => {
  const counter = { n: 0 };
  const { fetch } = fakeFetch(revealRoutes({ K: "v-fake" }, counter));
  const client = new JanusClient({
    baseUrl: BASE,
    token: TEST_TOKEN,
    fetch,
    cacheTtlMs: 0,
  });

  for (let i = 0; i < 3; i++) await client.getSecrets(TEST_CONFIG_ID);
  assert.equal(counter.n, 3);
});

test("refresh(configId) evicts one entry; refresh() clears all", async () => {
  const counter = { n: 0 };
  const { fetch } = fakeFetch(revealRoutes({ K: "v-fake" }, counter));
  const client = new JanusClient({
    baseUrl: BASE,
    token: TEST_TOKEN,
    fetch,
    cacheTtlMs: 3_600_000,
  });

  await client.getSecrets(TEST_CONFIG_ID);
  client.refresh(TEST_CONFIG_ID);
  await client.getSecrets(TEST_CONFIG_ID);
  assert.equal(counter.n, 2, "expected re-fetch after refresh(configId)");

  client.refresh();
  await client.getSecrets(TEST_CONFIG_ID);
  assert.equal(counter.n, 3, "expected re-fetch after refresh()");
});

test("cache: the returned object is a copy (mutation does not leak)", async () => {
  const { fetch } = fakeFetch(revealRoutes({ K: "v-fake" }));
  const client = new JanusClient({
    baseUrl: BASE,
    token: TEST_TOKEN,
    fetch,
    cacheTtlMs: 3_600_000,
  });

  const m1 = await client.getSecrets(TEST_CONFIG_ID);
  m1.K = "mutated";
  m1.EXTRA = "x";
  const m2 = await client.getSecrets(TEST_CONFIG_ID);

  assert.equal(m2.K, "v-fake");
  assert.equal(Object.keys(m2).length, 1);
});

test("error envelope maps to typed errors (401/403/404/503)", async () => {
  const cases: Array<{
    status: number;
    code: string;
    guard: (e: unknown) => boolean;
    ctor: new (...a: never[]) => JanusError;
  }> = [
    { status: 401, code: "unauthorized", guard: isUnauthorized, ctor: JanusUnauthorizedError },
    { status: 403, code: "forbidden", guard: isForbidden, ctor: JanusForbiddenError },
    { status: 404, code: "not_found", guard: isNotFound, ctor: JanusNotFoundError },
    { status: 503, code: "sealed", guard: isSealed, ctor: JanusSealedError },
  ];

  for (const tc of cases) {
    const { fetch } = fakeFetch({
      [REVEAL_ROUTE]: () => errorResponse(tc.status, tc.code, "fake message"),
    });
    const client = new JanusClient({ baseUrl: BASE, token: TEST_TOKEN, fetch });

    await assert.rejects(
      () => client.getSecrets(TEST_CONFIG_ID),
      (err: unknown) => {
        assert.ok(err instanceof tc.ctor, `expected ${tc.ctor.name}`);
        assert.ok(err instanceof JanusError);
        assert.ok(tc.guard(err));
        const je = err as JanusError;
        assert.equal(je.status, tc.status);
        assert.equal(je.code, tc.code);
        assert.match(je.message, /fake message/);
        return true;
      },
    );
  }
});

test("a 'sealed' code on a non-503 status still maps to JanusSealedError", async () => {
  const { fetch } = fakeFetch({
    [REVEAL_ROUTE]: () => errorResponse(500, "sealed", "server is sealed"),
  });
  const client = new JanusClient({ baseUrl: BASE, token: TEST_TOKEN, fetch });
  await assert.rejects(
    () => client.getSecrets(TEST_CONFIG_ID),
    (err: unknown) => isSealed(err),
  );
});

test("constructor validation and argument validation", async () => {
  assert.throws(() => new JanusClient({ baseUrl: "" }), /baseUrl is required/);
  assert.throws(() => new JanusClient({ baseUrl: "not a url" }));

  const { fetch } = fakeFetch(revealRoutes({ K: "v-fake" }));
  const client = new JanusClient({ baseUrl: BASE, token: TEST_TOKEN, fetch });
  await assert.rejects(() => client.getSecrets(""), /configId is required/);
  await assert.rejects(() => client.getSecret(TEST_CONFIG_ID, ""), /key is required/);
});

test("AbortSignal is forwarded and an aborted request rejects", async () => {
  const { fetch, requests } = fakeFetch({
    [REVEAL_ROUTE]: (req) => {
      if (req.signal?.aborted) {
        return Promise.reject(new DOMException("aborted", "AbortError"));
      }
      return jsonResponse(200, { secrets: { K: "v-fake" } });
    },
  });
  const client = new JanusClient({ baseUrl: BASE, token: TEST_TOKEN, fetch });

  const controller = new AbortController();
  controller.abort();
  await assert.rejects(() =>
    client.getSecrets(TEST_CONFIG_ID, { signal: controller.signal }),
  );
  assert.ok(requests[0].signal, "signal should be forwarded to fetch");
});

test("no token: Authorization header is omitted", async () => {
  const { fetch, requests } = fakeFetch(revealRoutes({ K: "v-fake" }));
  const client = new JanusClient({ baseUrl: BASE, fetch });
  await client.getSecrets(TEST_CONFIG_ID);
  assert.equal(requests[0].authorization, null);
});
