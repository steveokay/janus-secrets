"""Typed exceptions for the Janus Python SDK.

Mirrors the Go SDK's error taxonomy: non-2xx responses are parsed from the
server's ``{"error":{"code","message"}}`` envelope into a :class:`JanusError`
carrying the HTTP status alongside the machine ``code`` and human ``message``.
Common statuses raise a dedicated subclass so callers can ``except`` on the
specific case.

A :class:`JanusError` never carries a secret value: the server's error
envelope is value-free by design, and this type only ever holds the code and
message parsed from that envelope.
"""

from __future__ import annotations

from typing import Optional


class JanusError(Exception):
    """Base error for a non-2xx response from the Janus API.

    Attributes:
        status: HTTP status code of the response (e.g. 403, 404, 503).
        code: Machine-readable error code (e.g. ``"forbidden"``, ``"sealed"``).
        message: Human-readable message from the server envelope.
    """

    def __init__(self, status: int, code: str = "", message: str = "") -> None:
        self.status: int = status
        self.code: str = code
        self.message: str = message
        super().__init__(str(self))

    def __str__(self) -> str:
        if not self.code:
            return f"janus: api error (status {self.status})"
        return f"janus: api error {self.code} (status {self.status}): {self.message}"


class Unauthorized(JanusError):
    """HTTP 401 — token missing, invalid, or expired."""


class Forbidden(JanusError):
    """HTTP 403 — authenticated but not authorized for the operation."""


class NotFound(JanusError):
    """HTTP 404 — config, key, or resource does not exist or is not visible."""


class Sealed(JanusError):
    """HTTP 503 (or error code ``"sealed"``) — the server is sealed and cannot
    serve secret operations until unsealed."""


def error_for(status: int, code: str = "", message: str = "") -> JanusError:
    """Construct the most specific :class:`JanusError` subclass for a response.

    Mapping is by HTTP status (with the ``"sealed"`` code also mapping to
    :class:`Sealed`), matching the Go SDK's ``Unwrap`` behaviour, so it stays
    stable even if the server refines its code strings.
    """
    if status == 401:
        return Unauthorized(status, code, message)
    if status == 403:
        return Forbidden(status, code, message)
    if status == 404:
        return NotFound(status, code, message)
    if status == 503 or code == "sealed":
        return Sealed(status, code, message)
    return JanusError(status, code, message)


__all__ = [
    "JanusError",
    "Unauthorized",
    "Forbidden",
    "NotFound",
    "Sealed",
    "error_for",
]
