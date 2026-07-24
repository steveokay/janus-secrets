"""Dynamic-credential leases for the Janus Python SDK.

Mirrors the Go SDK's ``Lease``: a dynamic database credential issued by Janus
whose one-time password is returned exactly once at issue time. The server
never persists or audits the password in plaintext; the SDK likewise holds it
only in memory and never logs it.
"""

from __future__ import annotations

from typing import TYPE_CHECKING, Any, Dict, Optional
import urllib.parse

if TYPE_CHECKING:  # avoid a runtime import cycle with client.py
    from .client import Client


class Lease:
    """A dynamic database credential lease.

    Attributes:
        id: The lease identifier (server field ``lease_id``).
        username: The issued database username.
        password: The one-time password, returned only at issue time. Held in
            memory only; never persisted or logged by the SDK.
        expires_at: The lease expiry as the raw server string (RFC 3339), or
            ``None`` if absent.

    Instances are created by :meth:`janus_client.client.Client.issue_dynamic`;
    do not construct one directly.
    """

    __slots__ = ("id", "username", "password", "expires_at", "_client")

    def __init__(
        self,
        client: "Client",
        id: str = "",
        username: str = "",
        password: str = "",
        expires_at: Optional[str] = None,
    ) -> None:
        self._client = client
        self.id = id
        self.username = username
        self.password = password
        self.expires_at = expires_at

    @classmethod
    def _from_response(cls, client: "Client", data: Dict[str, Any]) -> "Lease":
        return cls(
            client,
            id=str(data.get("lease_id", "") or ""),
            username=str(data.get("username", "") or ""),
            password=str(data.get("password", "") or ""),
            expires_at=(str(data["expires_at"]) if data.get("expires_at") else None),
        )

    def renew(self) -> None:
        """Extend the lease's expiry (capped server-side at the role's max TTL)
        and update :attr:`expires_at`. Does not change the password.

        Raises a :class:`~janus_client.errors.JanusError` on failure (e.g. 409
        when the lease is no longer active).
        """
        if self._client is None:
            raise ValueError("janus: lease not bound to a client")
        if not self.id:
            raise ValueError("janus: lease has no id")
        path = "/v1/dynamic/leases/%s/renew" % urllib.parse.quote(self.id, safe="")
        resp = self._client._do("POST", path)
        if isinstance(resp, dict):
            new_expiry = resp.get("expires_at")
            if new_expiry:
                self.expires_at = str(new_expiry)

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

    def __repr__(self) -> str:
        # Never include the password in the repr.
        return f"Lease(id={self.id!r}, username={self.username!r}, expires_at={self.expires_at!r})"


__all__ = ["Lease"]
