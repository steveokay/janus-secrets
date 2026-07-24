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

from .client import DEFAULT_CACHE_TTL, DEFAULT_TIMEOUT, Client
from .errors import Forbidden, JanusError, NotFound, Sealed, Unauthorized
from .lease import Lease

__version__ = "0.1.0"

__all__ = [
    "Client",
    "Lease",
    "JanusError",
    "Unauthorized",
    "Forbidden",
    "NotFound",
    "Sealed",
    "DEFAULT_CACHE_TTL",
    "DEFAULT_TIMEOUT",
    "__version__",
]
