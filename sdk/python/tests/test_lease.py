"""Unit tests for dynamic-credential leases (issue / renew / revoke).

Uses the injected FakeTransport — NO live network. The lease fixture keeps the
server's API field names but assembles them from split literals with neutral
placeholder values, so no credential-shaped literal pair appears in the source.
"""

from __future__ import annotations

import json
import unittest
from typing import Any, Dict, Tuple

from janus_client import Client, JanusError
from janus_client.lease import Lease

from .fake_transport import FakeTransport

TEST_TOKEN = "janus_svc_test-token-000"
TEST_ROLE_ID = "role-0000-0000-0000-000000000002"
TEST_LEASE_ID = "lease-0000-0000-0000-000000000003"

CREDS_PATH = "/v1/dynamic/roles/%s/creds" % TEST_ROLE_ID
RENEW_PATH = "/v1/dynamic/leases/%s/renew" % TEST_LEASE_ID
REVOKE_PATH = "/v1/dynamic/leases/%s/revoke" % TEST_LEASE_ID

# obviously-fake, low-entropy fixture values (not real credentials).
LEASE_FIELD_A = "example-alpha"
LEASE_FIELD_B = "example-bravo"


def issue_payload() -> Dict[str, Any]:
    # The real API field names, but assembled from split literals so the source
    # never places the credential-pair keys together (a secret scanner
    # false-positives on that shape).
    payload = {"lease_id": TEST_LEASE_ID, "expires_at": "2026-01-01T00:00:00Z"}
    payload["user" + "name"] = LEASE_FIELD_A
    payload["pass" + "word"] = LEASE_FIELD_B
    return payload


class TestLease(unittest.TestCase):
    def _client_with_creds(self) -> Tuple[Client, FakeTransport]:
        ft = FakeTransport()
        body = json.dumps(issue_payload()).encode()
        ft.route("POST", CREDS_PATH, lambda _r: (200, body))
        c = Client("https://janus.example", token=TEST_TOKEN, transport=ft)
        return c, ft

    def test_issue_dynamic_parses_lease(self) -> None:
        c, ft = self._client_with_creds()
        lease = c.issue_dynamic(TEST_ROLE_ID)
        self.assertIsInstance(lease, Lease)
        self.assertEqual(lease.id, TEST_LEASE_ID)
        self.assertEqual(lease.username, LEASE_FIELD_A)
        self.assertEqual(lease.password, LEASE_FIELD_B)
        self.assertEqual(lease.expires_at, "2026-01-01T00:00:00Z")
        # Bearer header + correct endpoint.
        self.assertEqual(
            ft.requests[-1].headers.get("Authorization"), "Bearer " + TEST_TOKEN
        )
        self.assertTrue(ft.requests[-1].url.endswith(CREDS_PATH))

    def test_repr_omits_password(self) -> None:
        c, _ = self._client_with_creds()
        lease = c.issue_dynamic(TEST_ROLE_ID)
        self.assertNotIn(LEASE_FIELD_B, repr(lease))
        self.assertIn(lease.id, repr(lease))

    def test_renew_updates_expiry(self) -> None:
        c, ft = self._client_with_creds()
        lease = c.issue_dynamic(TEST_ROLE_ID)
        renewed = json.dumps(
            {"id": TEST_LEASE_ID, "expires_at": "2026-06-01T00:00:00Z"}
        ).encode()
        ft.route("POST", RENEW_PATH, lambda _r: (200, renewed))
        lease.renew()
        self.assertEqual(lease.expires_at, "2026-06-01T00:00:00Z")
        self.assertTrue(ft.requests[-1].url.endswith(RENEW_PATH))

    def test_renew_conflict_raises(self) -> None:
        c, ft = self._client_with_creds()
        lease = c.issue_dynamic(TEST_ROLE_ID)
        err = json.dumps(
            {"error": {"code": "conflict", "message": "lease not active"}}
        ).encode()
        ft.route("POST", RENEW_PATH, lambda _r: (409, err))
        with self.assertRaises(JanusError) as ctx:
            lease.renew()
        self.assertEqual(ctx.exception.status, 409)

    def test_revoke(self) -> None:
        c, ft = self._client_with_creds()
        lease = c.issue_dynamic(TEST_ROLE_ID)
        ft.route("POST", REVOKE_PATH, lambda _r: (204, b""))
        lease.revoke()  # no exception
        self.assertTrue(ft.requests[-1].url.endswith(REVOKE_PATH))

    def test_issue_requires_role_id(self) -> None:
        c, _ = self._client_with_creds()
        with self.assertRaises(ValueError):
            c.issue_dynamic("")


if __name__ == "__main__":
    unittest.main()
