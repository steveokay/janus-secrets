"""HTTP transport for the Janus SDK.

The SDK depends only on the Python standard library at runtime: the default
transport is built on :mod:`urllib.request`. A transport is any callable
matching :class:`Transport` (see :data:`Transport`), which lets tests inject a
fake without a live network and lets callers supply a custom
:class:`urllib.request.OpenerDirector` (e.g. for TLS or proxy configuration)
via the ``opener`` argument to :class:`~janus_client.client.Client`.
"""

from __future__ import annotations

import urllib.error
import urllib.request
from typing import Callable, Mapping, Optional, Tuple

# A Transport takes (method, url, body, headers, timeout) and returns
# (status_code, response_body_bytes). It must NOT raise for non-2xx statuses —
# it returns the status and body so the client can parse the error envelope.
Transport = Callable[
    [str, str, Optional[bytes], Mapping[str, str], float],
    Tuple[int, bytes],
]

# maxErrorBody bounds how much of an error response body the SDK reads when
# parsing the error envelope, so a misbehaving endpoint can't force unbounded
# allocation. Mirrors the Go SDK's 64 KiB cap.
MAX_ERROR_BODY = 1 << 16


class UrllibTransport:
    """Default :data:`Transport` built on :mod:`urllib.request`.

    An optional :class:`urllib.request.OpenerDirector` may be supplied to
    control TLS, proxies, or redirect handling. When absent, the module-level
    default opener is used.
    """

    def __init__(self, opener: Optional[urllib.request.OpenerDirector] = None) -> None:
        self._opener = opener

    def __call__(
        self,
        method: str,
        url: str,
        body: Optional[bytes],
        headers: Mapping[str, str],
        timeout: float,
    ) -> Tuple[int, bytes]:
        req = urllib.request.Request(url, data=body, method=method)
        for k, v in headers.items():
            req.add_header(k, v)
        try:
            opener = self._opener
            if opener is not None:
                resp = opener.open(req, timeout=timeout)
            else:
                resp = urllib.request.urlopen(req, timeout=timeout)
            with resp:
                return resp.status, resp.read()
        except urllib.error.HTTPError as exc:
            # Non-2xx (and some 3xx) surface here; read the error body so the
            # client can parse the {"error":{...}} envelope. Bound the read.
            data = b""
            try:
                data = exc.read(MAX_ERROR_BODY)
            except Exception:  # pragma: no cover - defensive
                data = b""
            finally:
                exc.close()
            return exc.code, data


__all__ = ["Transport", "UrllibTransport", "MAX_ERROR_BODY"]
