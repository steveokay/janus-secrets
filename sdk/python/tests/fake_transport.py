"""A minimal in-memory fake transport for the Janus SDK tests.

Records the requests it receives (method, url, headers) and replies from a
routing table, so tests assert on the Bearer header, endpoint calls, and cache
hit/miss counts with NO live network.
"""

from __future__ import annotations

import json
from typing import Callable, Dict, List, Mapping, Optional, Tuple


class RecordedRequest:
    def __init__(self, method: str, url: str, body: Optional[bytes], headers: Mapping[str, str]) -> None:
        self.method = method
        self.url = url
        self.body = body
        self.headers = dict(headers)


# A route handler receives the RecordedRequest and returns (status, body_bytes).
Handler = Callable[[RecordedRequest], Tuple[int, bytes]]


class FakeTransport:
    """Injectable transport matching ``janus_client._transport.Transport``.

    Routes are matched by an exact ``"METHOD path"`` key against the request's
    path (query string included). Unmatched requests return 404.
    """

    def __init__(self) -> None:
        self.requests: List[RecordedRequest] = []
        self._routes: Dict[str, Handler] = {}

    def route(self, method: str, path: str, handler: Handler) -> None:
        self._routes[method.upper() + " " + path] = handler

    def json_route(self, method: str, path: str, status: int, payload: object) -> "Counter":
        """Register a route returning JSON; returns a hit Counter for the route."""
        counter = Counter()

        def handler(_req: RecordedRequest) -> Tuple[int, bytes]:
            counter.n += 1
            return status, json.dumps(payload).encode("utf-8")

        self.route(method, path, handler)
        return counter

    def __call__(
        self,
        method: str,
        url: str,
        body: Optional[bytes],
        headers: Mapping[str, str],
        timeout: float,
    ) -> Tuple[int, bytes]:
        rec = RecordedRequest(method, url, body, headers)
        self.requests.append(rec)
        # Strip scheme://host to get the path (+query) for routing.
        path = url
        for prefix in ("http://", "https://"):
            if path.startswith(prefix):
                rest = path[len(prefix):]
                slash = rest.find("/")
                path = rest[slash:] if slash >= 0 else "/"
                break
        handler = self._routes.get(method.upper() + " " + path)
        if handler is None:
            return 404, b'{"error":{"code":"not_found","message":"no route"}}'
        return handler(rec)


class Counter:
    __slots__ = ("n",)

    def __init__(self) -> None:
        self.n = 0
