"""Dynamic-credential leases for the Janus Python SDK.

Mirrors the Go SDK's ``Lease``: a dynamic database credential issued by Janus
whose one-time password is returned exactly once at issue time. The server
never persists or audits the password in plaintext; the SDK likewise holds it
only in memory and never logs it.
"""

from __future__ import annotations

import threading
from datetime import datetime, timezone
from typing import TYPE_CHECKING, Any, Callable, Dict, Optional
import urllib.parse

from .autorenew import LeaseRenewer, RenewEvent

if TYPE_CHECKING:  # avoid a runtime import cycle with client.py
    from .client import Client


def parse_rfc3339(value: Optional[str]) -> Optional[datetime]:
    """Parse an RFC 3339 timestamp into an aware datetime, or ``None``.

    Written against the stdlib only and tolerant of the shapes the Janus API
    emits (``Z`` suffix, optional fractional seconds). Returns ``None`` rather
    than raising when the input is missing or unparsable, so a surprising server
    response degrades into "expiry unknown" instead of an exception.
    """
    if not value:
        return None
    text = value.strip()
    if text.endswith(("Z", "z")):
        text = text[:-1] + "+00:00"
    # datetime.fromisoformat before 3.11 accepts only 3 or 6 fractional digits.
    if "." in text:
        head, _, tail = text.partition(".")
        digits = ""
        idx = 0
        while idx < len(tail) and tail[idx].isdigit():
            digits += tail[idx]
            idx += 1
        if digits:
            text = head + "." + digits[:6].ljust(6, "0") + tail[idx:]
    try:
        parsed = datetime.fromisoformat(text)
    except ValueError:
        return None
    if parsed.tzinfo is None:
        parsed = parsed.replace(tzinfo=timezone.utc)
    return parsed


class Lease:
    """A dynamic database credential lease.

    Attributes:
        id: The lease identifier (server field ``lease_id``).
        username: The issued database username.
        password: The one-time password, returned only at issue time. Held in
            memory only; never persisted or logged by the SDK.
        expires_at: The lease expiry as the raw server string (RFC 3339), or
            ``None`` if absent. Updated by :meth:`renew`.
        max_expires_at: The hard ceiling past which the server will not extend
            this lease, as the raw server string, or ``None`` while unknown.
            Janus reports it only on a renew response.

    Instances are created by :meth:`janus_client.client.Client.issue_dynamic`;
    do not construct one directly.

    ``id``, ``username`` and ``password`` are immutable after issue. The expiry
    fields are mutated by :meth:`renew` and by a background renewer, so read
    them through :meth:`expiry` / :meth:`max_expiry` (which take the lease's
    lock) whenever a renewer is running.
    """

    __slots__ = (
        "id",
        "username",
        "password",
        "expires_at",
        "max_expires_at",
        "_client",
        "_lock",
    )

    def __init__(
        self,
        client: "Client",
        id: str = "",
        username: str = "",
        password: str = "",
        expires_at: Optional[str] = None,
        max_expires_at: Optional[str] = None,
    ) -> None:
        self._client = client
        self._lock = threading.Lock()
        self.id = id
        self.username = username
        self.password = password
        self.expires_at = expires_at
        self.max_expires_at = max_expires_at

    @classmethod
    def _from_response(cls, client: "Client", data: Dict[str, Any]) -> "Lease":
        return cls(
            client,
            id=str(data.get("lease_id", "") or ""),
            username=str(data.get("username", "") or ""),
            password=str(data.get("password", "") or ""),
            expires_at=(str(data["expires_at"]) if data.get("expires_at") else None),
        )

    # -- expiry ------------------------------------------------------------

    def expiry(self) -> Optional[datetime]:
        """The current expiry as an aware datetime, or ``None`` if unknown.

        Safe to call while a background renewer is running.
        """
        with self._lock:
            raw = self.expires_at
        return parse_rfc3339(raw)

    def max_expiry(self) -> Optional[datetime]:
        """The lease's hard ceiling as an aware datetime, or ``None``.

        Janus reports it only on a renew response, so it stays ``None`` until
        the first successful renew.
        """
        with self._lock:
            raw = self.max_expires_at
        return parse_rfc3339(raw)

    # -- lifecycle ---------------------------------------------------------

    def renew(self) -> None:
        """Extend the lease's expiry (capped server-side at the role's max TTL)
        and update :attr:`expires_at`. Does not change the password.

        Raises a :class:`~janus_client.errors.JanusError` on failure (e.g. 409
        when the lease is no longer active).
        """
        self._renew_view()

    def _renew_view(self) -> Dict[str, Any]:
        """Renew and return the raw server view, so callers that care (the
        background renewer) can see ``max_expires_at``.
        """
        if self._client is None:
            raise ValueError("janus: lease not bound to a client")
        if not self.id:
            raise ValueError("janus: lease has no id")
        path = "/v1/dynamic/leases/%s/renew" % urllib.parse.quote(self.id, safe="")
        resp = self._client._do("POST", path)
        view: Dict[str, Any] = resp if isinstance(resp, dict) else {}
        new_expiry = view.get("expires_at")
        new_max = view.get("max_expires_at")
        with self._lock:
            if new_expiry:
                self.expires_at = str(new_expiry)
            if new_max:
                self.max_expires_at = str(new_max)
        return view

    def revoke(self) -> None:
        """Revoke the lease immediately (drops the underlying database role).

        After a successful revoke the credentials are no longer valid.
        """
        if self._client is None:
            raise ValueError("janus: lease not bound to a client")
        if not self.id:
            raise ValueError("janus: lease has no id")
        path = "/v1/dynamic/leases/%s/revoke" % urllib.parse.quote(self.id, safe="")
        self._client._do("POST", path)

    def start_auto_renew(
        self,
        fraction: Optional[float] = None,
        jitter: Optional[float] = None,
        min_interval: Optional[float] = None,
        on_event: Optional[Callable[[RenewEvent], None]] = None,
        clock: Optional[Callable[[], datetime]] = None,
        sleeper: Optional[Callable[[float], bool]] = None,
        rng: Optional[Callable[[], float]] = None,
    ) -> LeaseRenewer:
        """Start a background thread that keeps this lease renewed.

        Auto-renew is strictly **opt-in**: nothing in this SDK starts a thread
        unless you call this (or :meth:`janus_client.client.Client.dynamic_lease`,
        which calls it for you). The caller owns the returned
        :class:`~janus_client.autorenew.LeaseRenewer` and **must** ``stop()`` it.

        Policy: after each successful renew the loop waits ~2/3 of the remaining
        TTL (+/-10% jitter, floored at 1s) and renews again.

        The loop ends — permanently, emitting one terminal event — when
        ``stop()`` is called, the server will not extend the lease further (max
        TTL reached: :class:`~janus_client.errors.MaxTTLReached`), the server
        rejects the renew non-retryably (401 / 403 / 404 / 409 / other 4xx), or
        the expiry passes before a renew succeeds
        (:class:`~janus_client.errors.LeaseExpired`).

        Retryable failures — network errors, 5xx (including a sealed server),
        408, 429 — are reported as non-terminal events and retried while TTL
        headroom remains. Nothing is swallowed: every failure reaches
        ``on_event``, and the terminal one is also on ``renewer.error``.
        """
        if self._client is None:
            raise ValueError("janus: lease not bound to a client")
        if not self.id:
            raise ValueError("janus: lease has no id")
        return LeaseRenewer(
            self,
            fraction=fraction,
            jitter=jitter,
            min_interval=min_interval,
            on_event=on_event,
            clock=clock,
            sleeper=sleeper,
            rng=rng,
        )

    def __repr__(self) -> str:
        # Never include the password in the repr.
        return (
            "Lease(id=%r, username=%r, expires_at=%r)"
            % (self.id, self.username, self.expires_at)
        )


__all__ = ["Lease", "parse_rfc3339"]
