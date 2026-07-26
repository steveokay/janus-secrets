import assert from "node:assert/strict";
import { test } from "node:test";

import {
  JanusClient,
  JanusMaxTtlReachedError,
  JanusRevokeError,
  type Lease,
  type RenewEvent,
} from "../src/index.js";
import {
  TEST_LEASE_PASSWORD,
  TEST_ROLE_ID,
  TEST_TOKEN,
  VirtualClock,
  fakeFetch,
  leaseRoutes,
} from "./helpers.js";

const BASE = "https://janus.example.test";
const START = 1_700_000_000_000;
const SEC = 1000;

/** A lease whose TTL is long enough that the default cadence never fires. */
function longLivedRoutes(overrides: { revokeStatus?: number } = {}) {
  return leaseRoutes({
    issueExpiresAtMs: Date.now() + 3600 * SEC,
    renewScript: [{ expiresAtMs: Date.now() + 7200 * SEC }],
    revokeStatus: overrides.revokeStatus,
  });
}

test("withDynamic revokes the lease after a successful callback", async () => {
  const { routes, counts } = longLivedRoutes();
  const { fetch } = fakeFetch(routes);
  const client = new JanusClient({ baseUrl: BASE, token: TEST_TOKEN, fetch });

  let seen: Lease | undefined;
  const result = await client.withDynamic(TEST_ROLE_ID, async (lease, signal) => {
    seen = lease;
    assert.equal(signal.aborted, false);
    assert.equal(lease.username, "example-user");
    assert.equal(lease.password, TEST_LEASE_PASSWORD);
    return "done";
  });

  assert.equal(result, "done");
  assert.ok(seen);
  assert.equal(counts.issues, 1);
  assert.equal(counts.revokes, 1);
});

test("withDynamic revokes the lease when the callback throws, and rethrows it", async () => {
  const { routes, counts } = longLivedRoutes();
  const { fetch } = fakeFetch(routes);
  const client = new JanusClient({ baseUrl: BASE, token: TEST_TOKEN, fetch });
  const boom = new Error("caller failed");

  await assert.rejects(
    () =>
      client.withDynamic(TEST_ROLE_ID, async () => {
        throw boom;
      }),
    (err: unknown) => err === boom,
  );
  assert.equal(counts.revokes, 1, "lease not revoked on error");
});

test("withDynamic revokes the lease when the callback throws synchronously", async () => {
  const { routes, counts } = longLivedRoutes();
  const { fetch } = fakeFetch(routes);
  const client = new JanusClient({ baseUrl: BASE, token: TEST_TOKEN, fetch });
  const boom = new Error("sync failure");

  await assert.rejects(
    () =>
      client.withDynamic(TEST_ROLE_ID, () => {
        throw boom;
      }),
    (err: unknown) => err === boom,
  );
  assert.equal(counts.revokes, 1, "lease not revoked on a synchronous throw");
});

test("withDynamic accepts a synchronous callback and still revokes", async () => {
  const { routes, counts } = longLivedRoutes();
  const { fetch } = fakeFetch(routes);
  const client = new JanusClient({ baseUrl: BASE, token: TEST_TOKEN, fetch });

  const out = await client.withDynamic(TEST_ROLE_ID, () => 42);
  assert.equal(out, 42);
  assert.equal(counts.revokes, 1);
});

test("a failed revoke never masks the callback's own error", async () => {
  const { routes, counts } = longLivedRoutes({ revokeStatus: 409 });
  const { fetch } = fakeFetch(routes);
  const client = new JanusClient({ baseUrl: BASE, token: TEST_TOKEN, fetch });
  const boom = new Error("caller failed");
  const events: RenewEvent[] = [];

  await assert.rejects(
    () =>
      client.withDynamic(
        TEST_ROLE_ID,
        async () => {
          throw boom;
        },
        { onEvent: (e) => void events.push(e) },
      ),
    (err: unknown) => {
      // The caller's error propagates unchanged...
      assert.equal(err, boom);
      // ...with the revoke failure attached rather than replacing it.
      const attached = (err as { revokeError?: unknown }).revokeError;
      assert.ok(attached instanceof JanusRevokeError);
      assert.ok(!String(attached).includes(TEST_LEASE_PASSWORD));
      return true;
    },
  );
  assert.equal(counts.revokes, 1);
  assert.ok(
    events.some((e) => e.reason === "revoke_failed" && e.error !== undefined),
    "revoke failure not reported to onEvent",
  );
});

test("a failed revoke surfaces when the callback succeeded", async () => {
  const { routes } = longLivedRoutes({ revokeStatus: 409 });
  const { fetch } = fakeFetch(routes);
  const client = new JanusClient({ baseUrl: BASE, token: TEST_TOKEN, fetch });

  await assert.rejects(
    () => client.withDynamic(TEST_ROLE_ID, async () => "ok"),
    (err: unknown) => err instanceof JanusRevokeError,
  );
});

test("the callback's signal aborts when auto-renew terminates, and the lease is revoked", async () => {
  const { routes, counts } = leaseRoutes({
    issueExpiresAtMs: START + 60 * SEC,
    // Already at the ceiling: the first renew ends the loop.
    renewScript: [{ expiresAtMs: START + 60 * SEC, maxExpiresAtMs: START + 60 * SEC }],
  });
  const { fetch } = fakeFetch(routes);
  const client = new JanusClient({ baseUrl: BASE, token: TEST_TOKEN, fetch });

  const clock = new VirtualClock(START);
  let terminal: RenewEvent | undefined;

  await client.withDynamic(
    TEST_ROLE_ID,
    async (_lease, signal) => {
      await new Promise<void>((resolve) => {
        if (signal.aborted) {
          resolve();
          return;
        }
        signal.addEventListener("abort", () => resolve(), { once: true });
      });
    },
    {
      jitter: 0,
      now: clock.now,
      setTimer: clock.setTimer,
      clearTimer: clock.clearTimer,
      onEvent: (e) => {
        if (e.terminal) terminal = e;
      },
    },
  );

  if (!terminal) {
    throw new Error("no terminal event was emitted");
  }
  assert.equal(terminal.reason, "max_ttl");
  assert.ok(terminal.error instanceof JanusMaxTtlReachedError);
  assert.equal(counts.revokes, 1);
});

test("withDynamic rejects a missing callback and never issues a lease", async () => {
  const { routes, counts } = longLivedRoutes();
  const { fetch } = fakeFetch(routes);
  const client = new JanusClient({ baseUrl: BASE, token: TEST_TOKEN, fetch });

  await assert.rejects(
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    () => client.withDynamic(TEST_ROLE_ID, undefined as any),
    /fn is required/,
  );
  assert.equal(counts.issues, 0);
});

test("a failed issue propagates and never runs the callback", async () => {
  const { fetch } = fakeFetch({});
  const client = new JanusClient({ baseUrl: BASE, token: TEST_TOKEN, fetch });
  let ran = false;

  await assert.rejects(() =>
    client.withDynamic(TEST_ROLE_ID, async () => {
      ran = true;
    }),
  );
  assert.equal(ran, false);
});
