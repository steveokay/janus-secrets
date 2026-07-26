"""Janus Python client SDK.

A typed, standard-library-only client for reading secrets from the Janus
secrets manager's ``/v1`` REST API, with an in-process memory-only TTL cache
and optional dynamic-credential leases. Mirrors the Janus Go SDK.

Example::

    from janus_client import Client, NotFound

    client = Client("https://janus.example.com", token="janus_svc_...")
    secrets = client.get_secrets("cfg-...")   # dict[str, str]
    db_url = client.get_secret("cfg-...", "DATABASE_URL")

The cache lives in process memory only and is never written to disk.
"""

from __future__ import annotations

from .autorenew import (
    DEFAULT_MIN_RENEW_INTERVAL,
    DEFAULT_RENEW_FRACTION,
    DEFAULT_RENEW_JITTER,
    STOP_ERROR,
    STOP_EXPIRED,
    STOP_FORBIDDEN,
    STOP_LEASE_GONE,
    STOP_MAX_TTL,
    STOP_REJECTED,
    STOP_REVOKE_FAILED,
    STOP_STOPPED,
    STOP_UNAUTHORIZED,
    LeaseRenewer,
    RenewEvent,
)
from .client import DEFAULT_CACHE_TTL, DEFAULT_TIMEOUT, Client
from .errors import (
    Forbidden,
    JanusError,
    LeaseExpired,
    MaxTTLReached,
    NotFound,
    RevokeFailed,
    Sealed,
    Unauthorized,
)
from .lease import Lease

__version__ = "0.1.0"

__all__ = [
    "Client",
    "Lease",
    "LeaseRenewer",
    "RenewEvent",
    "JanusError",
    "Unauthorized",
    "Forbidden",
    "NotFound",
    "Sealed",
    "MaxTTLReached",
    "LeaseExpired",
    "RevokeFailed",
    "DEFAULT_CACHE_TTL",
    "DEFAULT_TIMEOUT",
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
    "__version__",
]
