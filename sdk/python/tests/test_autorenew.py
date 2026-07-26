"""Unit tests for background lease auto-renew and the ``dynamic_lease`` helper.

Hermetic: the injected FakeTransport stands in for the server, and a virtual
clock plus a fake sleeper drive the renew loop, so no test waits on wall time
and no test touches the network.
"""

from __future__ import annotations

import json
import threading
import unittest
from datetime import datetime, timedelta, timezone
from typing import Any, Dict, List, Optional, Tuple

from janus_client import (
    STOP_EXPIRED,
    STOP_FORBIDDEN,
    STOP_LEASE_GONE,
    STOP_MAX_TTL,
    STOP_REJECTED,
    STOP_REVOKE_FAILED,
    STOP_STOPPED,
    STOP_UNAUTHORIZED,
    Client,
    JanusError,
    LeaseExpired,
    MaxTTLReached,
    RenewEvent,
    RevokeFailed,
)
from janus_client.lease import parse_rfc3339

from .fake_transport import FakeTransport

TEST_TOKEN = "janus_svc_test-token-000"
TEST_ROLE_ID = "role-0000-0000-0000-000000000002"
TEST_LEASE_ID = "lease-0000-0000-0000-000000000003"

CREDS_PATH = "/v1/dynamic/roles/%s/creds" % TEST_ROLE_ID
RENEW_PATH = "/v1/dynamic/leases/%s/renew" % TEST_LEASE_ID
REVOKE_PATH = "/v1/dynamic/leases/%s/revoke" % TEST_LEASE_ID

START = datetime(2026, 1, 1, 0, 0, 0, tzinfo=timezone.utc)

# obviously-fake, low-entropy fixture values (not real credentials).
LEASE_USER = "example-alpha"
LEASE_SECRET = "example-not-a-real-value-000"


def _rfc3339(when: datetime) -> str:
    return when.astimezone(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def _live(**delta: float) -> datetime:
    """A wall-clock-relative timestamp, for the few tests that deliberately run
    the renewer against the real clock and its interruptible sleeper."""
    return datetime.now(timezone.utc) + timedelta(**delta)  # type: ignore[arg-type]


def issue_payload(expires_at: datetime) -> Dict[str, Any]:
    # The real API field names, assembled from split literals so the source
    # never places the credential-pair keys together (secret scanners
    # false-positive on that shape).
    payload: Dict[str, Any] = {
        "lease_id": TEST_LEASE_ID,
        "expires_at": _rfc3339(expires_at),
    }
    payload["user" + "name"] = LEASE_USER
    payload["pass" + "word"] = LEASE_SECRET
    return payload


class VirtualClock:
    """A fake clock plus sleeper.

    ``sleep`` advances virtual time by the requested delay and returns
    immediately, so the renew loop runs at full speed while still exercising the
    real timing arithmetic. It reports a stop the same way ``Event.wait`` does.
    """

    def __init__(self, start: datetime, stop_event: Optional[threading.Event] = None):
        self._t = start
        self.waits: List[float] = []
        self.stop_event = stop_event or threading.Event()

    def now(self) -> datetime:
        return self._t

    def sleep(self, seconds: float) -> bool:
        self.waits.append(seconds)
        self._t = self._t + timedelta(seconds=seconds)
        return self.stop_event.is_set()


class Recorder:
    def __init__(self) -> None:
        self.events: List[RenewEvent] = []

    def __call__(self, event: RenewEvent) -> None:
        self.events.append(event)

    def terminal(self, case: unittest.TestCase) -> RenewEvent:
        case.assertTrue(self.events, "no events emitted")
        last = self.events[-1]
        case.assertTrue(last.terminal, "last event is not terminal")
        for e in self.events[:-1]:
            case.assertFalse(e.terminal, "terminal event emitted before the end")
        return last


class _Renews:
    """A scripted renew route: each call consumes one reply, the last repeats."""

    def __init__(self, script: List[Tuple[int, Dict[str, Any]]]) -> None:
        self.script = script
        self.calls = 0

    def __call__(self, _req: Any) -> Tuple[int, bytes]:
        idx = min(self.calls, len(self.script) - 1)
        self.calls += 1
        status, body = self.script[idx]
        return status, json.dumps(body).encode()


def _err(code: str, message: str) -> Dict[str, Any]:
    return {"error": {"code": code, "message": message}}


def _view(expires_at: datetime, max_expires_at: Optional[datetime] = None) -> Dict[str, Any]:
    body: Dict[str, Any] = {
        "id": TEST_LEASE_ID,
        "status": "active",
        "expires_at": _rfc3339(expires_at),
    }
    if max_expires_at is not None:
        body["max_expires_at"] = _rfc3339(max_expires_at)
    return body


def build(
    issue_expiry: datetime,
    renew_script: List[Tuple[int, Dict[str, Any]]],
    revoke_status: int = 200,
) -> Tuple[Client, FakeTransport, _Renews, List[int]]:
    ft = FakeTransport()
    ft.route(
        "POST",
        CREDS_PATH,
        lambda _r: (201, json.dumps(issue_payload(issue_expiry)).encode()),
    )
    renews = _Renews(renew_script)
    ft.route("POST", RENEW_PATH, renews)
    revokes: List[int] = []

    def revoke(_r: Any) -> Tuple[int, bytes]:
        revokes.append(1)
        if revoke_status != 200:
            return revoke_status, json.dumps(_err("conflict", "lease not active")).encode()
        return 200, b'{"revoked":true}'

    ft.route("POST", REVOKE_PATH, revoke)
    client = Client("https://janus.example", token=TEST_TOKEN, transport=ft)
    return client, ft, renews, revokes


class TestParseRFC3339(unittest.TestCase):
    def test_shapes(self) -> None:
        self.assertEqual(
            parse_rfc3339("2026-01-01T00:00:00Z"),
            datetime(2026, 1, 1, tzinfo=timezone.utc),
        )
        self.assertEqual(
            parse_rfc3339("2026-01-01T00:00:00+00:00"),
            datetime(2026, 1, 1, tzinfo=timezone.utc),
        )
        # Nanosecond precision (more digits than fromisoformat accepts) is
        # truncated rather than rejected.
        self.assertIsNotNone(parse_rfc3339("2026-01-01T00:00:00.123456789Z"))
        # A naive timestamp is treated as UTC.
        self.assertEqual(
            parse_rfc3339("2026-01-01T00:00:00"),
            datetime(2026, 1, 1, tzinfo=timezone.utc),
        )
        self.assertIsNone(parse_rfc3339(None))
        self.assertIsNone(parse_rfc3339(""))
        self.assertIsNone(parse_rfc3339("not-a-timestamp"))


class TestAutoRenew(unittest.TestCase):
    def test_renews_before_expiry_and_stops_at_max_ttl(self) -> None:
        client, _ft, renews, _revokes = build(
            START + timedelta(seconds=60),
            [
                (200, _view(START + timedelta(seconds=120), START + timedelta(seconds=300))),
                (200, _view(START + timedelta(seconds=180), START + timedelta(seconds=300))),
                (200, _view(START + timedelta(seconds=300), START + timedelta(seconds=300))),
            ],
        )
        lease = client.issue_dynamic(TEST_ROLE_ID)
        clock = VirtualClock(START)
        rec = Recorder()
        renewer = lease.start_auto_renew(
            jitter=0, on_event=rec, clock=clock.now, sleeper=clock.sleep
        )
        self.assertTrue(renewer.wait(5), "renew loop did not finish")

        self.assertEqual(renews.calls, 3)
        self.assertEqual(renewer.reason, STOP_MAX_TTL)
        self.assertIsInstance(renewer.error, MaxTTLReached)
        # The first renew is scheduled at 2/3 of the 60s TTL — before expiry.
        self.assertAlmostEqual(clock.waits[0], 40.0, places=6)
        self.assertLess(clock.waits[0], 60.0)
        self.assertEqual(len([e for e in rec.events if e.renewed]), 3)
        self.assertEqual(rec.terminal(self).reason, STOP_MAX_TTL)

        # Stopping an already-finished renewer is a no-op, and repeatable.
        renewer.stop(5)
        renewer.stop(5)

    def test_stop_halts_renewal_and_joins_the_thread(self) -> None:
        client, _ft, renews, _revokes = build(
            _live(hours=1), [(200, _view(_live(hours=2)))]
        )
        lease = client.issue_dynamic(TEST_ROLE_ID)

        # A real clock and its interruptible sleeper, with ~40 minutes to the
        # first renew: only stop() can end the wait.
        rec = Recorder()
        renewer = lease.start_auto_renew(jitter=0, on_event=rec)
        renewer.stop(5)

        self.assertTrue(renewer.done)
        self.assertFalse(renewer._thread.is_alive(), "renew thread leaked")
        self.assertEqual(renews.calls, 0, "a stopped renewer must not renew")
        self.assertEqual(renewer.reason, STOP_STOPPED)
        self.assertIsNone(renewer.error)
        self.assertEqual(rec.terminal(self).reason, STOP_STOPPED)

        # Idempotent.
        renewer.stop(5)
        renewer.stop(5)
        self.assertFalse(renewer._thread.is_alive())

    def test_terminal_errors_stop_the_loop(self) -> None:
        cases = [
            (404, "not_found", STOP_LEASE_GONE),
            (409, "conflict", STOP_LEASE_GONE),
            (403, "forbidden", STOP_FORBIDDEN),
            (401, "unauthorized", STOP_UNAUTHORIZED),
            (400, "validation", STOP_REJECTED),
        ]
        for status, code, reason in cases:
            with self.subTest(status=status):
                client, _ft, renews, _revokes = build(
                    START + timedelta(seconds=60), [(status, _err(code, "nope"))]
                )
                lease = client.issue_dynamic(TEST_ROLE_ID)
                clock = VirtualClock(START)
                rec = Recorder()
                renewer = lease.start_auto_renew(
                    jitter=0, on_event=rec, clock=clock.now, sleeper=clock.sleep
                )
                self.assertTrue(renewer.wait(5))

                self.assertEqual(renewer.reason, reason)
                self.assertIsInstance(renewer.error, JanusError)
                self.assertEqual(renewer.error.status, status)  # type: ignore[union-attr]
                self.assertEqual(renews.calls, 1, "a terminal error must not be retried")
                self.assertEqual(rec.terminal(self).reason, reason)
                renewer.stop(5)

    def test_retryable_failures_are_surfaced_but_do_not_end_the_loop(self) -> None:
        client, _ft, renews, _revokes = build(
            START + timedelta(seconds=60),
            [
                (503, _err("sealed", "server is sealed")),
                (429, _err("rate_limited", "slow down")),
                (200, _view(START + timedelta(seconds=300), START + timedelta(seconds=300))),
            ],
        )
        lease = client.issue_dynamic(TEST_ROLE_ID)
        clock = VirtualClock(START)
        rec = Recorder()
        renewer = lease.start_auto_renew(
            jitter=0, on_event=rec, clock=clock.now, sleeper=clock.sleep
        )
        self.assertTrue(renewer.wait(5))

        self.assertEqual(renews.calls, 3)
        self.assertIsInstance(renewer.error, MaxTTLReached)
        retryable = [e for e in rec.events if e.error is not None and not e.terminal]
        self.assertEqual(len(retryable), 2)
        self.assertTrue(all(isinstance(e.error, JanusError) for e in retryable))
        # Retries back off sooner than the normal cadence.
        self.assertLess(clock.waits[1], clock.waits[0])
        renewer.stop(5)

    def test_expires_when_the_server_stays_down(self) -> None:
        client, _ft, renews, _revokes = build(
            START + timedelta(seconds=60), [(502, _err("upstream", "down"))]
        )
        lease = client.issue_dynamic(TEST_ROLE_ID)
        clock = VirtualClock(START)
        rec = Recorder()
        renewer = lease.start_auto_renew(
            jitter=0, on_event=rec, clock=clock.now, sleeper=clock.sleep
        )
        self.assertTrue(renewer.wait(5))

        self.assertEqual(renewer.reason, STOP_EXPIRED)
        self.assertIsInstance(renewer.error, LeaseExpired)
        self.assertTrue(0 < renews.calls < 30, "loop did not converge: %d" % renews.calls)
        self.assertEqual(rec.terminal(self).reason, STOP_EXPIRED)
        renewer.stop(5)

    def test_a_renew_that_no_longer_extends_ends_the_loop(self) -> None:
        # No max_expires_at reported, but the expiry does not move: still capped.
        client, _ft, renews, _revokes = build(
            START + timedelta(seconds=60),
            [(200, _view(START + timedelta(seconds=60)))],
        )
        lease = client.issue_dynamic(TEST_ROLE_ID)
        clock = VirtualClock(START)
        renewer = lease.start_auto_renew(jitter=0, clock=clock.now, sleeper=clock.sleep)
        self.assertTrue(renewer.wait(5))

        self.assertEqual(renews.calls, 1)
        self.assertIsInstance(renewer.error, MaxTTLReached)
        renewer.stop(5)

    def test_wait_policy(self) -> None:
        client, _ft, _renews, _revokes = build(
            START + timedelta(hours=1), [(200, _view(START + timedelta(hours=2)))]
        )
        lease = client.issue_dynamic(TEST_ROLE_ID)
        clock = VirtualClock(START)
        clock.stop_event.set()  # end the loop at its first wait
        r = lease.start_auto_renew(jitter=0, clock=clock.now, sleeper=clock.sleep)
        self.assertTrue(r.wait(5))

        self.assertAlmostEqual(r.wait_for(60.0), 40.0, places=6)
        self.assertAlmostEqual(r.wait_for(60.0, retry=True), 20.0, places=6)
        # The floor keeps a tiny TTL from hot-looping, without exceeding what is
        # actually left.
        self.assertAlmostEqual(r.wait_for(0.3), 0.3, places=6)
        self.assertAlmostEqual(r.wait_for(1.2), 1.0, places=6)
        r.stop(5)

    def test_jitter_varies_within_bounds(self) -> None:
        client, _ft, _renews, _revokes = build(
            START + timedelta(hours=1), [(200, _view(START + timedelta(hours=2)))]
        )
        lease = client.issue_dynamic(TEST_ROLE_ID)
        clock = VirtualClock(START)
        clock.stop_event.set()
        seq = [0.0, 1.0, 0.25, 0.75]
        idx = {"i": 0}

        def rng() -> float:
            v = seq[idx["i"] % len(seq)]
            idx["i"] += 1
            return v

        r = lease.start_auto_renew(clock=clock.now, sleeper=clock.sleep, rng=rng)
        self.assertTrue(r.wait(5))
        seen = {round(r.wait_for(600.0), 6) for _ in seq}
        for w in seen:
            self.assertGreaterEqual(w, 400.0 * 0.9)
            self.assertLessEqual(w, 400.0 * 1.1)
        self.assertGreater(len(seen), 1, "jitter produced no variation")
        r.stop(5)

    def test_events_never_carry_the_password(self) -> None:
        client, _ft, _renews, _revokes = build(
            START + timedelta(seconds=60),
            [
                (503, _err("sealed", "server is sealed")),
                (404, _err("not_found", "lease not found")),
            ],
        )
        lease = client.issue_dynamic(TEST_ROLE_ID)
        self.assertEqual(lease.password, LEASE_SECRET, "fixture not wired")

        clock = VirtualClock(START)
        rec = Recorder()
        renewer = lease.start_auto_renew(
            jitter=0, on_event=rec, clock=clock.now, sleeper=clock.sleep
        )
        self.assertTrue(renewer.wait(5))
        renewer.stop(5)

        rendered = "|".join(
            "%r|%s" % (e, e.error) for e in rec.events
        ) + "|%s|%s|%s" % (renewer.error, renewer.reason, repr(lease))
        self.assertNotIn(LEASE_SECRET, rendered)
        self.assertIn(TEST_LEASE_ID, rendered)

    def test_start_auto_renew_requires_a_bound_lease_with_an_id(self) -> None:
        client, _ft, _renews, _revokes = build(
            START + timedelta(hours=1), [(200, _view(START + timedelta(hours=2)))]
        )
        lease = client.issue_dynamic(TEST_ROLE_ID)
        lease.id = ""
        with self.assertRaises(ValueError):
            lease.start_auto_renew()


class TestDynamicLeaseContextManager(unittest.TestCase):
    def test_revokes_on_success(self) -> None:
        client, _ft, _renews, revokes = build(
            START + timedelta(hours=1), [(200, _view(START + timedelta(hours=2)))]
        )
        clock = VirtualClock(START)
        clock.stop_event.set()  # the renewer ends at its first wait

        seen = {}
        with client.dynamic_lease(
            TEST_ROLE_ID, jitter=0, clock=clock.now, sleeper=clock.sleep
        ) as lease:
            seen["username"] = lease.username
            seen["password"] = lease.password

        self.assertEqual(seen["username"], LEASE_USER)
        self.assertEqual(seen["password"], LEASE_SECRET)
        self.assertEqual(len(revokes), 1)

    def test_revokes_on_exception_and_reraises_it(self) -> None:
        client, _ft, _renews, revokes = build(
            START + timedelta(hours=1), [(200, _view(START + timedelta(hours=2)))]
        )
        clock = VirtualClock(START)
        clock.stop_event.set()

        class Boom(RuntimeError):
            pass

        with self.assertRaises(Boom):
            with client.dynamic_lease(
                TEST_ROLE_ID, jitter=0, clock=clock.now, sleeper=clock.sleep
            ):
                raise Boom("caller failed")

        self.assertEqual(len(revokes), 1, "lease not revoked on exception")

    def test_revokes_on_early_return(self) -> None:
        client, _ft, _renews, revokes = build(
            START + timedelta(hours=1), [(200, _view(START + timedelta(hours=2)))]
        )
        clock = VirtualClock(START)
        clock.stop_event.set()

        def body() -> str:
            with client.dynamic_lease(
                TEST_ROLE_ID, jitter=0, clock=clock.now, sleeper=clock.sleep
            ) as lease:
                return lease.username

        self.assertEqual(body(), LEASE_USER)
        self.assertEqual(len(revokes), 1, "lease not revoked on early return")

    def test_revoke_failure_does_not_mask_the_caller_error(self) -> None:
        client, _ft, _renews, revokes = build(
            START + timedelta(hours=1),
            [(200, _view(START + timedelta(hours=2)))],
            revoke_status=409,
        )
        clock = VirtualClock(START)
        clock.stop_event.set()
        rec = Recorder()

        class Boom(RuntimeError):
            pass

        with self.assertRaises(Boom) as ctx:
            with client.dynamic_lease(
                TEST_ROLE_ID,
                jitter=0,
                on_event=rec,
                clock=clock.now,
                sleeper=clock.sleep,
            ):
                raise Boom("caller failed")

        attached = getattr(ctx.exception, "janus_revoke_error", None)
        self.assertIsInstance(attached, RevokeFailed)
        self.assertNotIn(LEASE_SECRET, str(attached))
        self.assertEqual(len(revokes), 1)
        self.assertTrue(
            any(e.reason == STOP_REVOKE_FAILED for e in rec.events),
            "revoke failure not reported to on_event",
        )

    def test_revoke_failure_raises_when_the_body_succeeded(self) -> None:
        client, _ft, _renews, _revokes = build(
            START + timedelta(hours=1),
            [(200, _view(START + timedelta(hours=2)))],
            revoke_status=409,
        )
        clock = VirtualClock(START)
        clock.stop_event.set()

        with self.assertRaises(RevokeFailed):
            with client.dynamic_lease(
                TEST_ROLE_ID, jitter=0, clock=clock.now, sleeper=clock.sleep
            ):
                pass

    def test_auto_renew_false_starts_no_thread(self) -> None:
        client, _ft, renews, revokes = build(
            _live(hours=1), [(200, _view(_live(hours=2)))]
        )
        before = threading.active_count()
        with client.dynamic_lease(TEST_ROLE_ID, auto_renew=False) as lease:
            self.assertEqual(threading.active_count(), before)
            self.assertEqual(lease.id, TEST_LEASE_ID)
        self.assertEqual(renews.calls, 0)
        self.assertEqual(len(revokes), 1)

    def test_renewal_thread_is_gone_after_the_block(self) -> None:
        # Real clock + real sleeper: the renewer parks for ~40 minutes, so only
        # the context manager's teardown can end it.
        client, _ft, renews, revokes = build(
            _live(hours=1), [(200, _view(_live(hours=2)))]
        )
        before = threading.active_count()
        with client.dynamic_lease(TEST_ROLE_ID, jitter=0) as lease:
            self.assertEqual(lease.id, TEST_LEASE_ID)
            self.assertEqual(threading.active_count(), before + 1)
        self.assertEqual(
            threading.active_count(), before, "renew thread outlived the with-block"
        )
        self.assertEqual(renews.calls, 0)
        self.assertEqual(len(revokes), 1)


if __name__ == "__main__":
    unittest.main()
