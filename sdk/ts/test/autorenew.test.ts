import assert from "node:assert/strict";
import { test } from "node:test";

import {
  JanusClient,
  JanusError,
  JanusLeaseExpiredError,
  JanusMaxTtlReachedError,
  type RenewEvent,
} from "../src/index.js";
import {
  BlockingTimer,
  TEST_LEASE_ID,
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

function collector() {
  const events: RenewEvent[] = [];
  return { events, onEvent: (e: RenewEvent) => void events.push(e) };
}

function assertOneTerminalEventLast(events: RenewEvent[]): RenewEvent {
  assert.ok(events.length > 0, "no events emitted");
  const last = events[events.length - 1];
  assert.equal(last.terminal, true, "last event is not terminal");
  for (const e of events.slice(0, -1)) {
    assert.equal(e.terminal, false, "terminal event emitted before the end");
  }
  return last;
}

test("auto-renew renews well before expiry and stops at the server's max TTL", async () => {
  const { routes, counts } = leaseRoutes({
    issueExpiresAtMs: START + 60 * SEC,
    renewScript: [
      { expiresAtMs: START + 120 * SEC, maxExpiresAtMs: START + 300 * SEC },
      { expiresAtMs: START + 180 * SEC, maxExpiresAtMs: START + 300 * SEC },
      { expiresAtMs: START + 300 * SEC, maxExpiresAtMs: START + 300 * SEC },
    ],
  });
  const { fetch } = fakeFetch(routes);
  const client = new JanusClient({ baseUrl: BASE, token: TEST_TOKEN, fetch });
  const lease = await client.issueDynamic(TEST_ROLE_ID);

  const clock = new VirtualClock(START);
  const col = collector();
  const renewer = lease.startAutoRenew({
    onEvent: col.onEvent,
    jitter: 0,
    now: clock.now,
    setTimer: clock.setTimer,
    clearTimer: clock.clearTimer,
  });
  await renewer.done;

  assert.equal(counts.renews, 3);
  assert.equal(renewer.reason, "max_ttl");
  assert.ok(renewer.error instanceof JanusMaxTtlReachedError);

  // The first renew is scheduled at 2/3 of the 60s TTL — comfortably before it.
  assert.equal(clock.waits[0], 40 * SEC);
  assert.ok(clock.waits[0] < 60 * SEC);

  assert.equal(col.events.filter((e) => e.renewed).length, 3);
  const terminal = assertOneTerminalEventLast(col.events);
  assert.equal(terminal.reason, "max_ttl");
  assert.equal(terminal.leaseId, TEST_LEASE_ID);

  // Stopping an already-finished renewer is a no-op, and repeatable.
  await renewer.stop();
  await renewer.stop();
});

test("stop halts renewal, releases the timer, and is idempotent", async () => {
  const { routes, counts } = leaseRoutes({
    issueExpiresAtMs: START + 3600 * SEC,
    renewScript: [{ expiresAtMs: START + 7200 * SEC }],
  });
  const { fetch } = fakeFetch(routes);
  const client = new JanusClient({ baseUrl: BASE, token: TEST_TOKEN, fetch });
  const lease = await client.issueDynamic(TEST_ROLE_ID);

  const timer = new BlockingTimer();
  const col = collector();
  const renewer = lease.startAutoRenew({
    onEvent: col.onEvent,
    jitter: 0,
    now: () => START,
    setTimer: timer.setTimer,
    clearTimer: timer.clearTimer,
  });
  await timer.armed; // the loop is parked in its wait

  await renewer.stop();

  assert.equal(counts.renews, 0, "a stopped renewer must not renew");
  assert.equal(renewer.reason, "stopped");
  assert.equal(renewer.error, undefined);
  assert.equal(timer.cancels, 1, "the armed timer was not released");
  const terminal = assertOneTerminalEventLast(col.events);
  assert.equal(terminal.reason, "stopped");

  await renewer.stop();
  await renewer.stop();
  assert.equal(timer.cancels, 1, "stop released the timer more than once");
});

test("aborting the supplied signal stops the loop", async () => {
  const { routes, counts } = leaseRoutes({
    issueExpiresAtMs: START + 3600 * SEC,
    renewScript: [{ expiresAtMs: START + 7200 * SEC }],
  });
  const { fetch } = fakeFetch(routes);
  const client = new JanusClient({ baseUrl: BASE, token: TEST_TOKEN, fetch });
  const lease = await client.issueDynamic(TEST_ROLE_ID);

  const controller = new AbortController();
  const timer = new BlockingTimer();
  const renewer = lease.startAutoRenew({
    jitter: 0,
    signal: controller.signal,
    now: () => START,
    setTimer: timer.setTimer,
    clearTimer: timer.clearTimer,
  });
  await timer.armed;
  controller.abort();
  await renewer.done;

  assert.equal(renewer.reason, "aborted");
  assert.equal(counts.renews, 0);
  await renewer.stop();
});

for (const tc of [
  { name: "404 (lease gone)", status: 404, code: "not_found", reason: "lease_gone" },
  { name: "409 (not active)", status: 409, code: "conflict", reason: "lease_gone" },
  { name: "403 (forbidden)", status: 403, code: "forbidden", reason: "forbidden" },
  { name: "401 (unauthorized)", status: 401, code: "unauthorized", reason: "unauthorized" },
  { name: "400 (rejected)", status: 400, code: "validation", reason: "rejected" },
]) {
  test(`a terminal ${tc.name} stops the loop and is surfaced`, async () => {
    const { routes, counts } = leaseRoutes({
      issueExpiresAtMs: START + 60 * SEC,
      renewScript: [{ status: tc.status, code: tc.code, message: "nope" }],
    });
    const { fetch } = fakeFetch(routes);
    const client = new JanusClient({ baseUrl: BASE, token: TEST_TOKEN, fetch });
    const lease = await client.issueDynamic(TEST_ROLE_ID);

    const clock = new VirtualClock(START);
    const col = collector();
    const renewer = lease.startAutoRenew({
      onEvent: col.onEvent,
      jitter: 0,
      now: clock.now,
      setTimer: clock.setTimer,
      clearTimer: clock.clearTimer,
    });
    await renewer.done;

    assert.equal(renewer.reason, tc.reason);
    assert.ok(renewer.error instanceof JanusError);
    assert.equal((renewer.error as JanusError).status, tc.status);
    assert.equal(counts.renews, 1, "a terminal error must not be retried");
    assert.equal(assertOneTerminalEventLast(col.events).reason, tc.reason);
    await renewer.stop();
  });
}

test("retryable failures are surfaced but do not end the loop", async () => {
  const { routes, counts } = leaseRoutes({
    issueExpiresAtMs: START + 60 * SEC,
    renewScript: [
      { status: 503, code: "sealed", message: "server is sealed" },
      { status: 429, code: "rate_limited", message: "slow down" },
      { expiresAtMs: START + 300 * SEC, maxExpiresAtMs: START + 300 * SEC },
    ],
  });
  const { fetch } = fakeFetch(routes);
  const client = new JanusClient({ baseUrl: BASE, token: TEST_TOKEN, fetch });
  const lease = await client.issueDynamic(TEST_ROLE_ID);

  const clock = new VirtualClock(START);
  const col = collector();
  const renewer = lease.startAutoRenew({
    onEvent: col.onEvent,
    jitter: 0,
    now: clock.now,
    setTimer: clock.setTimer,
    clearTimer: clock.clearTimer,
  });
  await renewer.done;

  assert.equal(counts.renews, 3);
  assert.ok(renewer.error instanceof JanusMaxTtlReachedError);
  const retryable = col.events.filter((e) => !e.terminal && e.error !== undefined);
  assert.equal(retryable.length, 2);
  assert.ok(retryable.every((e) => e.error instanceof JanusError));
  // The retry waits are shorter than the normal cadence.
  assert.ok(clock.waits[1] < clock.waits[0], `waits: ${clock.waits.join(",")}`);
  await renewer.stop();
});

test("the loop ends cleanly when the lease expires before a renew succeeds", async () => {
  const { routes, counts } = leaseRoutes({
    issueExpiresAtMs: START + 60 * SEC,
    renewScript: [{ status: 502, code: "upstream", message: "down" }],
  });
  const { fetch } = fakeFetch(routes);
  const client = new JanusClient({ baseUrl: BASE, token: TEST_TOKEN, fetch });
  const lease = await client.issueDynamic(TEST_ROLE_ID);

  const clock = new VirtualClock(START);
  const col = collector();
  const renewer = lease.startAutoRenew({
    onEvent: col.onEvent,
    jitter: 0,
    now: clock.now,
    setTimer: clock.setTimer,
    clearTimer: clock.clearTimer,
  });
  await renewer.done;

  assert.equal(renewer.reason, "expired");
  assert.ok(renewer.error instanceof JanusLeaseExpiredError);
  assert.ok(counts.renews > 0 && counts.renews < 30, `retry count ${counts.renews}`);
  assert.equal(assertOneTerminalEventLast(col.events).reason, "expired");
  await renewer.stop();
});

test("a renew that no longer extends the expiry ends the loop", async () => {
  const { routes, counts } = leaseRoutes({
    issueExpiresAtMs: START + 60 * SEC,
    // No max_expires_at reported, and the expiry does not move: still capped.
    renewScript: [{ expiresAtMs: START + 60 * SEC }],
  });
  const { fetch } = fakeFetch(routes);
  const client = new JanusClient({ baseUrl: BASE, token: TEST_TOKEN, fetch });
  const lease = await client.issueDynamic(TEST_ROLE_ID);

  const clock = new VirtualClock(START);
  const renewer = lease.startAutoRenew({
    jitter: 0,
    now: clock.now,
    setTimer: clock.setTimer,
    clearTimer: clock.clearTimer,
  });
  await renewer.done;

  assert.equal(counts.renews, 1);
  assert.ok(renewer.error instanceof JanusMaxTtlReachedError);
  await renewer.stop();
});

test("renew events never carry the lease password", async () => {
  const { routes } = leaseRoutes({
    issueExpiresAtMs: START + 60 * SEC,
    renewScript: [
      { status: 503, code: "sealed", message: "server is sealed" },
      { status: 404, code: "not_found", message: "lease not found" },
    ],
  });
  const { fetch } = fakeFetch(routes);
  const client = new JanusClient({ baseUrl: BASE, token: TEST_TOKEN, fetch });
  const lease = await client.issueDynamic(TEST_ROLE_ID);
  assert.equal(lease.password, TEST_LEASE_PASSWORD, "fixture not wired");

  const clock = new VirtualClock(START);
  const col = collector();
  const renewer = lease.startAutoRenew({
    onEvent: col.onEvent,
    jitter: 0,
    now: clock.now,
    setTimer: clock.setTimer,
    clearTimer: clock.clearTimer,
  });
  await renewer.done;
  await renewer.stop();

  const rendered = col.events
    .map((e) => `${JSON.stringify({ ...e, error: undefined })}|${String(e.error)}`)
    .join("|")
    .concat(`|${String(renewer.error)}|${String(renewer.reason)}`);
  assert.ok(!rendered.includes(TEST_LEASE_PASSWORD), `password leaked: ${rendered}`);
  assert.ok(rendered.includes(TEST_LEASE_ID), "sanity check on the fixture");
});

test("startAutoRenew requires a lease ID", async () => {
  const { routes } = leaseRoutes({ issueExpiresAtMs: START, renewScript: [] });
  const { fetch } = fakeFetch(routes);
  const client = new JanusClient({ baseUrl: BASE, token: TEST_TOKEN, fetch });
  const lease = await client.issueDynamic(TEST_ROLE_ID);
  (lease as unknown as { id: string }).id = "";
  assert.throws(() => lease.startAutoRenew(), /lease has no ID/);
});
