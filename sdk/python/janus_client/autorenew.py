"""Background lease auto-renewal for the Janus Python SDK.

Nothing here starts on its own: a renew thread exists only after an explicit
:meth:`janus_client.lease.Lease.start_auto_renew` (or
:meth:`janus_client.client.Client.dynamic_lease`, which calls it for you).
Every renew — success, retryable failure, and the single terminal event — is
reported through the ``on_event`` callback; the SDK itself logs nothing, and no
event ever carries the lease password.

Standard library only (``threading``, ``datetime``, ``random``).
"""

from __future__ import annotations

import random as _random
import threading
from datetime import datetime, timezone
from typing import TYPE_CHECKING, Callable, Optional

from .errors import JanusError, LeaseExpired, MaxTTLReached

if TYPE_CHECKING:  # avoid a runtime import cycle with lease.py
    from .lease import Lease


# Renewal policy: "renew at ~2/3 of the remaining TTL, with jitter". After each
# successful renew the loop recomputes the time left before the lease expires
# and waits this fraction of it before renewing again, leaving roughly a third
# of the TTL as headroom for retries if the server is briefly unreachable.
DEFAULT_RENEW_FRACTION = 2.0 / 3.0

# Relative jitter applied to each computed wait (0.1 == +/-10%), so a fleet of
# processes started together does not renew in lockstep.
DEFAULT_RENEW_JITTER = 0.1

# Floor (seconds) on the computed wait, so a very short or already-elapsed TTL
# cannot turn the renewer into a hot loop.
DEFAULT_MIN_RENEW_INTERVAL = 1.0


# Stop reasons, reported on the terminal event and via ``LeaseRenewer.reason``.
STOP_STOPPED = "stopped"  # stop() was called; not an error
STOP_MAX_TTL = "max_ttl"  # server will not extend the lease any further
STOP_LEASE_GONE = "lease_gone"  # 404 / 409: revoked or expired server-side
STOP_UNAUTHORIZED = "unauthorized"  # 401
STOP_FORBIDDEN = "forbidden"  # 403
STOP_REJECTED = "rejected"  # some other non-retryable 4xx
STOP_EXPIRED = "expired"  # expiry passed before a renew succeeded
STOP_ERROR = "error"  # an unexpected local failure ended the loop
STOP_REVOKE_FAILED = "revoke_failed"  # only from Client.dynamic_lease


class RenewEvent:
    """One step of a renew loop.

    Value-free: carries the lease id and timings only, never the password.

    Attributes:
        lease_id: The lease this event concerns.
        renewed: ``True`` when the event reports a successful renew.
        expires_at: The lease expiry known at the time of the event.
        error: Set when a renew attempt failed. A non-terminal ``error`` is a
            retryable failure (network error, 5xx, 408, 429); the loop will try
            again while TTL headroom remains.
        terminal: ``True`` on the final event; the loop has stopped.
        reason: One of the ``STOP_*`` constants, set on the terminal event.
    """

    __slots__ = ("lease_id", "renewed", "expires_at", "error", "terminal", "reason")

    def __init__(
        self,
        lease_id: str,
        renewed: bool = False,
        expires_at: Optional[datetime] = None,
        error: Optional[BaseException] = None,
        terminal: bool = False,
        reason: Optional[str] = None,
    ) -> None:
        self.lease_id = lease_id
        self.renewed = renewed
        self.expires_at = expires_at
        self.error = error
        self.terminal = terminal
        self.reason = reason

    def __repr__(self) -> str:
        return (
            "RenewEvent(lease_id=%r, renewed=%r, expires_at=%r, error=%r, "
            "terminal=%r, reason=%r)"
            % (
                self.lease_id,
                self.renewed,
                self.expires_at,
                self.error,
                self.terminal,
                self.reason,
            )
        )


def _utcnow() -> datetime:
    return datetime.now(timezone.utc)


def classify_renew_error(exc: BaseException) -> Optional[str]:
    """Return a terminal stop reason for ``exc``, or ``None`` if it is retryable.

    4xx responses are terminal (the server will keep saying no), except 408 and
    429 which are transient. Everything else — network errors, 5xx, a sealed
    server — is retryable while TTL headroom remains.
    """
    if not isinstance(exc, JanusError):
        return None
    status = exc.status
    if status == 401:
        return STOP_UNAUTHORIZED
    if status == 403:
        return STOP_FORBIDDEN
    if status in (404, 409):
        return STOP_LEASE_GONE
    if status in (408, 429):
        return None
    if 400 <= status < 500:
        return STOP_REJECTED
    return None


class LeaseRenewer:
    """A running background renew loop for a single lease.

    Create one with :meth:`janus_client.lease.Lease.start_auto_renew`. The
    caller owns it and **must** :meth:`stop` it (the thread is a daemon, so it
    will not block interpreter exit, but it will keep renewing until then).

    Args:
        lease: The lease to keep renewed.
        fraction: Fraction of the remaining TTL to wait before the next renew
            attempt. Must be in ``(0, 1]``; anything else selects
            :data:`DEFAULT_RENEW_FRACTION`.
        jitter: Relative jitter applied to each wait. ``None`` selects
            :data:`DEFAULT_RENEW_JITTER`; ``0`` disables jitter.
        min_interval: Floor (seconds) on the computed wait.
        on_event: Called with each :class:`RenewEvent`, from the renewer's own
            thread. It must not block for long and must not call :meth:`stop`
            (which joins the very thread it runs on). This is the only place
            renew activity surfaces — the SDK logs nothing.
        clock: Callable returning an aware :class:`~datetime.datetime`.
            Injectable so tests need no wall-clock waits.
        sleeper: Callable ``(seconds) -> bool`` returning ``True`` if a stop was
            requested during the wait. Defaults to the internal stop event's
            ``wait``, which makes :meth:`stop` interrupt a pending sleep.
        rng: Uniform random source in ``[0, 1)`` for jitter.
    """

    def __init__(
        self,
        lease: "Lease",
        fraction: Optional[float] = None,
        jitter: Optional[float] = None,
        min_interval: Optional[float] = None,
        on_event: Optional[Callable[[RenewEvent], None]] = None,
        clock: Optional[Callable[[], datetime]] = None,
        sleeper: Optional[Callable[[float], bool]] = None,
        rng: Optional[Callable[[], float]] = None,
    ) -> None:
        self._lease = lease
        self._fraction = (
            fraction if (fraction is not None and 0 < fraction <= 1) else DEFAULT_RENEW_FRACTION
        )
        self._jitter = jitter if (jitter is not None and jitter >= 0) else DEFAULT_RENEW_JITTER
        self._min_interval = (
            min_interval
            if (min_interval is not None and min_interval > 0)
            else DEFAULT_MIN_RENEW_INTERVAL
        )
        self._on_event = on_event
        self._now = clock or _utcnow
        self._rng = rng or _random.random

        self._stop_event = threading.Event()
        self._done_event = threading.Event()
        self._sleep = sleeper or self._stop_event.wait
        self._error: Optional[BaseException] = None
        self._reason: Optional[str] = None

        self._thread = threading.Thread(
            target=self._run, name="janus-lease-renew", daemon=True
        )
        self._thread.start()

    # -- control ----------------------------------------------------------

    def stop(self, timeout: Optional[float] = None) -> None:
        """Stop the loop and wait for its thread to exit. Idempotent.

        A renew request already in flight cannot be cancelled (the stdlib
        transport is blocking), so this can take up to the client's request
        timeout. Pass ``timeout`` to bound the wait; the thread is a daemon, so
        an abandoned one never blocks interpreter exit.

        Never call it from an ``on_event`` handler — that handler runs on the
        thread this joins.
        """
        self._stop_event.set()
        self._thread.join(timeout)

    def wait(self, timeout: Optional[float] = None) -> bool:
        """Block until the loop has exited. Returns ``True`` if it has."""
        return self._done_event.wait(timeout)

    @property
    def done(self) -> bool:
        """``True`` once the loop has exited, however it ended."""
        return self._done_event.is_set()

    @property
    def error(self) -> Optional[BaseException]:
        """The terminal error, or ``None`` if the loop stopped cleanly.

        Read it after :meth:`wait` returns. Typical values:
        :class:`~janus_client.errors.MaxTTLReached`,
        :class:`~janus_client.errors.LeaseExpired`, or a
        :class:`~janus_client.errors.JanusError` subclass.
        """
        return self._error

    @property
    def reason(self) -> Optional[str]:
        """One of the ``STOP_*`` constants. Read it after :meth:`wait`."""
        return self._reason

    # -- internals --------------------------------------------------------

    def _emit(self, event: RenewEvent) -> None:
        if self._on_event is not None:
            self._on_event(event)

    def _finish(
        self, error: Optional[BaseException], reason: str, expires_at: Optional[datetime]
    ) -> None:
        self._error = error
        self._reason = reason
        self._emit(
            RenewEvent(
                self._lease.id,
                expires_at=expires_at,
                error=error,
                terminal=True,
                reason=reason,
            )
        )

    def wait_for(self, remaining: float, retry: bool = False) -> float:
        """Seconds to wait before the next attempt, given the TTL left.

        ``retry`` halves the fraction so a failed attempt is retried sooner than
        the normal cadence while still converging on the expiry instead of
        hot-looping.
        """
        frac = self._fraction / 2 if retry else self._fraction
        wait = remaining * frac
        if self._jitter > 0:
            wait *= 1 + self._jitter * (2 * self._rng() - 1)
        if wait < self._min_interval:
            wait = self._min_interval
        if wait > remaining:
            wait = remaining
        return wait if wait > 0 else 0.0

    def _run(self) -> None:
        try:
            self._loop()
        except BaseException as exc:  # never let the thread die silently
            self._finish(exc, STOP_ERROR, None)
        finally:
            self._done_event.set()

    def _loop(self) -> None:
        retry = False
        while True:
            expiry = self._lease.expiry()
            if expiry is None:
                self._finish(
                    ValueError("janus: lease has no parsable expiry"), STOP_ERROR, None
                )
                return
            remaining = (expiry - self._now()).total_seconds()
            if remaining <= 0:
                self._finish(LeaseExpired(self._lease.id), STOP_EXPIRED, expiry)
                return

            if self._sleep(self.wait_for(remaining, retry)):
                self._finish(None, STOP_STOPPED, expiry)
                return
            if self._stop_event.is_set():
                self._finish(None, STOP_STOPPED, expiry)
                return
            retry = False

            try:
                self._lease._renew_view()
            except (JanusError, OSError) as exc:
                # A stop request beats every other interpretation.
                if self._stop_event.is_set():
                    self._finish(None, STOP_STOPPED, expiry)
                    return
                reason = classify_renew_error(exc)
                if reason is not None:
                    self._finish(exc, reason, expiry)
                    return
                self._emit(
                    RenewEvent(self._lease.id, expires_at=expiry, error=exc)
                )
                retry = True
                continue

            nxt = self._lease.expiry()
            self._emit(RenewEvent(self._lease.id, renewed=True, expires_at=nxt))

            # Renewal is capped server-side. Two independent signals that the
            # cap has been reached: the server reports expires_at at/after
            # max_expires_at, or the renew did not move the expiry forward.
            max_exp = self._lease.max_expiry()
            at_max = nxt is not None and max_exp is not None and nxt >= max_exp
            if nxt is None or at_max or nxt <= expiry:
                self._finish(MaxTTLReached(self._lease.id), STOP_MAX_TTL, nxt)
                return


__all__ = [
    "LeaseRenewer",
    "RenewEvent",
    "classify_renew_error",
    "DEFAULT_RENEW_FRACTION",
    "DEFAULT_RENEW_JITTER",
    "DEFAULT_MIN_RENEW_INTERVAL",
    "STOP_STOPPED",
    "STOP_MAX_TTL",
    "STOP_LEASE_GONE",
    "STOP_UNAUTHORIZED",
    "STOP_FORBIDDEN",
    "STOP_REJECTED",
    "STOP_EXPIRED",
    "STOP_ERROR",
    "STOP_REVOKE_FAILED",
]
