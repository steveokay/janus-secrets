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

import json
import threading
import time
import urllib.parse
import urllib.request
from typing import Callable, Dict, Optional

from ._transport import MAX_ERROR_BODY, Transport, UrllibTransport
from .errors import JanusError, NotFound, error_for
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
