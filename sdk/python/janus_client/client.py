"""Typed Python client for the Janus secrets manager's ``/v1`` REST API.

Mirrors the Janus Go SDK (``sdk/go``): programmatic secret reads with an
in-process, memory-only TTL cache and (optional) dynamic-credential lease
management.

The SDK talks to Janus over HTTP using a scoped service token
(``janus_svc_...``). It never writes secret values to disk — the cache is
memory-only — and no method logs secret values. Reads go through the audited
reveal endpoint, so every :meth:`Client.get_secrets` / :meth:`Client.get_secret`
is recorded server-side as a ``secret.reveal`` event; that is expected and
intentional.

Basic usage::

    from janus_client import Client

    client = Client("https://janus.example.com", token=os.environ["JANUS_TOKEN"])
    secrets = client.get_secrets(config_id)
    # use secrets["DATABASE_URL"] — never log the value.
"""

from __future__ import annotations

import contextlib
import json
import threading
import time
import urllib.parse
import urllib.request
from datetime import datetime
from typing import Callable, Dict, Iterator, Optional

from ._transport import MAX_ERROR_BODY, Transport, UrllibTransport
from .autorenew import STOP_REVOKE_FAILED, LeaseRenewer, RenewEvent
from .errors import JanusError, NotFound, RevokeFailed, error_for
from .lease import Lease

# Default time-to-live (seconds) for cached config reads when no cache_ttl is
# supplied. Mirrors the Go SDK's DefaultCacheTTL of 30s.
DEFAULT_CACHE_TTL = 30.0

# Default per-request timeout (seconds) for the built-in transport, matching
# the Go SDK's 30s http.Client timeout.
DEFAULT_TIMEOUT = 30.0


class _CacheEntry:
    __slots__ = ("secrets", "expires_at")

    def __init__(self, secrets: Dict[str, str], expires_at: float) -> None:
        self.secrets = secrets
        self.expires_at = expires_at


class Client:
    """A Janus API client. Safe for concurrent use by multiple threads.

    Args:
        base_url: Base URL of the Janus server, e.g.
            ``"https://janus.example.com"``. The ``/v1`` prefix is added
            automatically.
        token: A ``janus_svc_...`` service token, sent as
            ``Authorization: Bearer <token>`` on every request. May be omitted
            for unauthenticated calls (they will typically 401).
        cache_ttl: In-process cache TTL in seconds for config reads. Default is
            ``30.0``; pass ``0`` (or a negative value) to disable caching
            entirely (every read hits the server).
        timeout: Per-request timeout in seconds for the default transport.
        opener: Optional :class:`urllib.request.OpenerDirector` for the default
            transport (TLS, proxies, redirects). Ignored if ``transport`` is set.
        transport: Optional injectable HTTP transport (see
            :data:`janus_client._transport.Transport`). Used by tests to avoid a
            live network; takes precedence over ``opener``.
        clock: Optional monotonic-ish clock callable returning seconds as a
            float, overridable in tests to make cache expiry deterministic.
            Defaults to :func:`time.monotonic`.
    """

    def __init__(
        self,
        base_url: str,
        token: Optional[str] = None,
        cache_ttl: float = DEFAULT_CACHE_TTL,
        timeout: float = DEFAULT_TIMEOUT,
        opener: Optional[urllib.request.OpenerDirector] = None,
        transport: Optional[Transport] = None,
        clock: Optional[Callable[[], float]] = None,
    ) -> None:
        if not base_url or not base_url.strip():
            raise ValueError("janus: base_url is required")

        self._base_url = base_url.rstrip("/")
        self._token = token
        self._cache_ttl = cache_ttl
        self._timeout = timeout
        self._transport: Transport = transport or UrllibTransport(opener)
        self._now: Callable[[], float] = clock or time.monotonic

        self._lock = threading.Lock()
        self._cache: Dict[str, _CacheEntry] = {}

    # -- reads -------------------------------------------------------------

    def get_secrets(self, config_id: str) -> Dict[str, str]:
        """Return a config's resolved secrets as a ``{key: value}`` dict.

        References are resolved server-side. Results are cached in memory for
        the configured TTL; within the TTL, repeated calls return the cached
        map without hitting the server. On a cache miss this is an audited
        reveal (``secret.reveal``).

        The returned dict is a copy; mutating it does not affect the cache.
        """
        if not config_id:
            raise ValueError("janus: config_id is required")

        if self._cache_ttl > 0:
            with self._lock:
                entry = self._cache.get(config_id)
                if entry is not None and self._now() < entry.expires_at:
                    return dict(entry.secrets)

        secrets = self._fetch_secrets(config_id)

        if self._cache_ttl > 0:
            with self._lock:
                self._cache[config_id] = _CacheEntry(
                    dict(secrets), self._now() + self._cache_ttl
                )
        return dict(secrets)

    def get_secret(self, config_id: str, key: str) -> str:
        """Return a single resolved secret value from a config.

        When caching is enabled and the config is already cached (and fresh),
        the value is served from the cached batch; otherwise the config is
        fetched (and cached) via the batch reveal. A missing key raises
        :class:`~janus_client.errors.NotFound`.
        """
        if not config_id:
            raise ValueError("janus: config_id is required")
        if not key:
            raise ValueError("janus: key is required")
        secrets = self.get_secrets(config_id)
        try:
            return secrets[key]
        except KeyError:
            raise NotFound(404, "not_found", "secret key not found") from None

    def refresh(self, config_id: Optional[str] = None) -> None:
        """Evict cached secrets so the next read re-fetches from the server.

        If ``config_id`` is ``None`` (or empty), the entire cache is cleared.
        """
        with self._lock:
            if not config_id:
                self._cache = {}
            else:
                self._cache.pop(config_id, None)

    def _fetch_secrets(self, config_id: str) -> Dict[str, str]:
        path = "/v1/configs/%s/secrets?reveal=true" % urllib.parse.quote(
            config_id, safe=""
        )
        resp = self._do("GET", path)
        secrets = resp.get("secrets") if isinstance(resp, dict) else None
        if not isinstance(secrets, dict):
            return {}
        # Values are strings; coerce defensively without logging them.
        return {str(k): str(v) for k, v in secrets.items()}

    # -- dynamic credentials ----------------------------------------------

    def issue_dynamic(self, role_id: str) -> Lease:
        """Issue a new dynamic credential lease for a dynamic role ID
        (``POST /v1/dynamic/roles/{id}/creds``).

        The returned :class:`~janus_client.lease.Lease` carries the one-time
        password; store it in memory only. Note ``role_id`` identifies a
        dynamic role, not a config.
        """
        if not role_id:
            raise ValueError("janus: role_id is required")
        path = "/v1/dynamic/roles/%s/creds" % urllib.parse.quote(role_id, safe="")
        resp = self._do("POST", path)
        return Lease._from_response(self, resp if isinstance(resp, dict) else {})

    @contextlib.contextmanager
    def dynamic_lease(
        self,
        role_id: str,
        auto_renew: bool = True,
        fraction: Optional[float] = None,
        jitter: Optional[float] = None,
        min_interval: Optional[float] = None,
        on_event: Optional[Callable[[RenewEvent], None]] = None,
        clock: Optional[Callable[[], datetime]] = None,
        sleeper: Optional[Callable[[float], bool]] = None,
        rng: Optional[Callable[[], float]] = None,
        stop_timeout: Optional[float] = None,
    ) -> Iterator[Lease]:
        """Issue a dynamic credential lease, keep it renewed for the duration of
        the ``with`` block, and revoke it on the way out — on success, on an
        exception, and on an early ``return`` or ``break``.

        This is the recommended way to use dynamic credentials: no lease is left
        dangling and nothing has to remember to renew::

            with client.dynamic_lease(role_id) as lease:
                conn = psycopg.connect(user=lease.username, password=lease.password)
                ...

        Auto-renew follows the policy documented on
        :meth:`janus_client.lease.Lease.start_auto_renew` (renew at ~2/3 of the
        remaining TTL, +/-10% jitter). Pass ``auto_renew=False`` to skip it and
        renew by hand. Pass ``on_event`` to observe renewals and learn why they
        stopped — for instance to wind the block down when the lease hits its
        max TTL, since the credentials will stop working shortly after.

        Error contract: the exception raised inside the ``with`` body is never
        replaced. If the final revoke also fails, that failure is reported to
        ``on_event`` with reason ``"revoke_failed"`` and attached to the
        propagating exception as its ``janus_revoke_error`` attribute. If the
        body succeeded and only the revoke failed,
        :class:`~janus_client.errors.RevokeFailed` is raised.

        There is no async variant: this SDK's transport is the blocking stdlib
        ``urllib``, so an ``async with`` would only be a thread wrapper.
        """
        lease = self.issue_dynamic(role_id)
        renewer: Optional[LeaseRenewer] = None
        if auto_renew:
            try:
                renewer = lease.start_auto_renew(
                    fraction=fraction,
                    jitter=jitter,
                    min_interval=min_interval,
                    on_event=on_event,
                    clock=clock,
                    sleeper=sleeper,
                    rng=rng,
                )
            except BaseException:
                # The lease already exists server-side; do not leak it.
                with contextlib.suppress(Exception):
                    lease.revoke()
                raise

        try:
            yield lease
        except BaseException as body_exc:
            revoke_exc = self._teardown_lease(lease, renewer, stop_timeout)
            if revoke_exc is not None:
                wrapped = RevokeFailed(lease.id, revoke_exc)
                if on_event is not None:
                    with contextlib.suppress(Exception):
                        on_event(
                            RenewEvent(
                                lease.id,
                                error=wrapped,
                                terminal=True,
                                reason=STOP_REVOKE_FAILED,
                            )
                        )
                # Surface the revoke failure without replacing the caller's
                # exception, which is what propagates.
                with contextlib.suppress(Exception):
                    setattr(body_exc, "janus_revoke_error", wrapped)
            raise
        else:
            revoke_exc = self._teardown_lease(lease, renewer, stop_timeout)
            if revoke_exc is not None:
                raise RevokeFailed(lease.id, revoke_exc) from revoke_exc

    @staticmethod
    def _teardown_lease(
        lease: Lease, renewer: Optional[LeaseRenewer], stop_timeout: Optional[float]
    ) -> Optional[BaseException]:
        """Stop the renewer and revoke the lease. Returns the revoke failure,
        if any, instead of raising, so callers decide how to surface it.
        """
        if renewer is not None:
            with contextlib.suppress(Exception):
                renewer.stop(stop_timeout)
        try:
            lease.revoke()
        except Exception as exc:  # noqa: BLE001 - reported, not swallowed
            return exc
        return None

    # -- HTTP plumbing -----------------------------------------------------

    def _do(self, method: str, path: str, body: Optional[object] = None) -> object:
        """Perform an HTTP request, returning the decoded JSON (or ``None``).

        Adds the bearer token, JSON-encodes ``body`` when present, and raises a
        typed :class:`~janus_client.errors.JanusError` for non-2xx responses.
        """
        data: Optional[bytes] = None
        headers: Dict[str, str] = {"Accept": "application/json"}
        if self._token:
            headers["Authorization"] = "Bearer " + self._token
        if body is not None:
            data = json.dumps(body).encode("utf-8")
            headers["Content-Type"] = "application/json"

        status, raw = self._transport(
            method, self._base_url + path, data, headers, self._timeout
        )

        if status < 200 or status >= 300:
            raise self._parse_error(status, raw)

        if not raw:
            return None
        try:
            return json.loads(raw)
        except (ValueError, UnicodeDecodeError) as exc:
            raise JanusError(status, "decode_error", "invalid JSON response") from exc

    @staticmethod
    def _parse_error(status: int, raw: bytes) -> JanusError:
        code = ""
        message = ""
        if raw:
            try:
                env = json.loads(raw[:MAX_ERROR_BODY])
                err = env.get("error") if isinstance(env, dict) else None
                if isinstance(err, dict):
                    code = str(err.get("code", "") or "")
                    message = str(err.get("message", "") or "")
            except (ValueError, UnicodeDecodeError):
                pass
        return error_for(status, code, message)


__all__ = ["Client", "DEFAULT_CACHE_TTL", "DEFAULT_TIMEOUT"]
