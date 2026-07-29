"""Unit tests for the Janus Python SDK's group catalog support.

Everything runs against an injected FakeTransport — NO live network.
"""

from __future__ import annotations

import json
import unittest
from typing import Tuple

from janus_client import (
    GROUP_KIND_LOCAL,
    GROUP_KIND_OIDC,
    Client,
    Forbidden,
    JanusError,
)

from .fake_transport import FakeTransport, RecordedRequest

# obviously-fake fixtures (not real identifiers)
TEST_TOKEN = "janus_svc_test-token-000"
TEST_GROUP_ID = "grp-00000000-0000-0000-0000-000000000001"
TEST_USER_ID = "usr-00000000-0000-0000-0000-000000000002"
TEST_CLAIM = "8f14e45f-ceea-467a-9d0e-7f4b2a1c9c33"

MEMBER_PATH = "/v1/groups/%s/members/%s" % (TEST_GROUP_ID, TEST_USER_ID)


def no_content(_req: RecordedRequest) -> Tuple[int, bytes]:
    return 204, b""


def client_with(ft: FakeTransport) -> Client:
    return Client("https://janus.example", token=TEST_TOKEN, transport=ft)


class TestGroupCatalog(unittest.TestCase):
    def test_create_group_sends_wire_shape(self) -> None:
        ft = FakeTransport()
        ft.json_route(
            "POST",
            "/v1/groups",
            201,
            {
                "id": TEST_GROUP_ID,
                "name": "Team Payments",
                "kind": "oidc",
                "claim_value": TEST_CLAIM,
                "member_count": 2,
                "binding_count": 1,
                "created_at": "2026-07-29T10:00:00Z",
            },
        )
        group = client_with(ft).create_group(
            "Team Payments", GROUP_KIND_OIDC, claim_value=TEST_CLAIM
        )

        sent = json.loads(ft.requests[0].body or b"{}")
        self.assertEqual(
            sent, {"name": "Team Payments", "kind": "oidc", "claim_value": TEST_CLAIM}
        )
        self.assertEqual(ft.requests[0].headers["Authorization"], "Bearer " + TEST_TOKEN)
        self.assertEqual(group.kind, GROUP_KIND_OIDC)
        self.assertEqual(group.claim_value, TEST_CLAIM)
        # Deliberately members_seen, not member_count: for an oidc group this
        # only counts users who have signed in.
        self.assertEqual(group.members_seen, 2)
        self.assertEqual(group.binding_count, 1)

    def test_local_group_never_sends_a_claim(self) -> None:
        ft = FakeTransport()
        ft.json_route("POST", "/v1/groups", 201, {"id": TEST_GROUP_ID, "kind": "local"})
        group = client_with(ft).create_group("Platform", GROUP_KIND_LOCAL)

        sent = json.loads(ft.requests[0].body or b"{}")
        self.assertEqual(sent, {"name": "Platform", "kind": "local"})
        self.assertIsNone(group.claim_value)

    def test_two_kinds_rule_is_enforced_before_any_request(self) -> None:
        ft = FakeTransport()
        ft.json_route("POST", "/v1/groups", 201, {"id": TEST_GROUP_ID})
        c = client_with(ft)

        with self.assertRaisesRegex(ValueError, "requires a claim value"):
            c.create_group("t", GROUP_KIND_OIDC)
        with self.assertRaisesRegex(ValueError, "cannot have a claim value"):
            c.create_group("t", GROUP_KIND_LOCAL, claim_value="x")
        with self.assertRaisesRegex(ValueError, "group kind must be"):
            c.create_group("t", "ldap")
        with self.assertRaisesRegex(ValueError, "name is required"):
            c.create_group("", GROUP_KIND_LOCAL)

        self.assertEqual(
            ft.requests, [], "no request may be issued for an invalid group"
        )

    def test_get_group_unwraps_envelope(self) -> None:
        ft = FakeTransport()
        ft.json_route(
            "GET",
            "/v1/groups/" + TEST_GROUP_ID,
            200,
            {
                "group": {
                    "id": TEST_GROUP_ID,
                    "name": "Team Payments",
                    "kind": "oidc",
                    "claim_value": TEST_CLAIM,
                },
                "bindings": [{"group_id": TEST_GROUP_ID, "role": "developer"}],
            },
        )
        group = client_with(ft).get_group(TEST_GROUP_ID)
        self.assertEqual(group.name, "Team Payments")
        self.assertEqual(group.claim_value, TEST_CLAIM)

    def test_listings_follow_every_cursor_page(self) -> None:
        ft = FakeTransport()
        ft.json_route(
            "GET",
            "/v1/groups?limit=100",
            200,
            {"groups": [{"id": "g1", "kind": "local"}], "next_cursor": "page2"},
        )
        ft.json_route(
            "GET",
            "/v1/groups?limit=100&cursor=page2",
            200,
            {"groups": [{"id": "g2", "kind": "oidc"}], "next_cursor": None},
        )
        members_path = "/v1/groups/%s/members?limit=100" % TEST_GROUP_ID
        ft.json_route(
            "GET",
            members_path,
            200,
            {
                "members": [{"user_id": "u1", "created_at": "2026-07-01T00:00:00Z"}],
                "next_cursor": "page2",
            },
        )
        ft.json_route(
            "GET",
            members_path + "&cursor=page2",
            200,
            {"members": [{"user_id": "u2"}], "next_cursor": None},
        )
        c = client_with(ft)

        self.assertEqual([g.id for g in c.list_groups()], ["g1", "g2"])
        members = c.list_group_members(TEST_GROUP_ID)
        self.assertEqual([m.user_id for m in members], ["u1", "u2"])
        self.assertEqual(members[0].added_at, "2026-07-01T00:00:00Z")

    def test_membership_and_capability_writes_hit_the_right_routes(self) -> None:
        ft = FakeTransport()
        ft.route("PUT", MEMBER_PATH, no_content)
        ft.route("DELETE", MEMBER_PATH, no_content)
        ft.json_route(
            "PUT",
            "/v1/groups/%s/capabilities" % TEST_GROUP_ID,
            200,
            {"can_create_projects": True},
        )
        ft.route("DELETE", "/v1/groups/" + TEST_GROUP_ID, no_content)
        c = client_with(ft)

        c.add_group_member(TEST_GROUP_ID, TEST_USER_ID)
        c.remove_group_member(TEST_GROUP_ID, TEST_USER_ID)
        c.set_group_project_creation(TEST_GROUP_ID, True)
        c.delete_group(TEST_GROUP_ID)

        self.assertEqual(
            [r.method for r in ft.requests], ["PUT", "DELETE", "PUT", "DELETE"]
        )
        # A membership PUT carries no body at all.
        self.assertIsNone(ft.requests[0].body)
        self.assertEqual(
            json.loads(ft.requests[2].body or b"{}"), {"can_create_projects": True}
        )

    def test_adding_a_member_to_an_oidc_group_surfaces_the_409(self) -> None:
        ft = FakeTransport()
        ft.json_route(
            "PUT",
            MEMBER_PATH,
            409,
            {
                "error": {
                    "code": "validation",
                    "message": "membership of an oidc group comes from the identity provider",
                }
            },
        )
        with self.assertRaises(JanusError) as ctx:
            client_with(ft).add_group_member(TEST_GROUP_ID, TEST_USER_ID)
        self.assertEqual(ctx.exception.status, 409)
        self.assertIn("identity provider", ctx.exception.message)

    def test_catalog_forbidden_raises_forbidden(self) -> None:
        ft = FakeTransport()
        ft.json_route(
            "GET",
            "/v1/groups?limit=100",
            403,
            {"error": {"code": "forbidden", "message": "forbidden"}},
        )
        with self.assertRaises(Forbidden):
            client_with(ft).list_groups()

    def test_my_groups_tolerates_an_empty_list(self) -> None:
        ft = FakeTransport()
        ft.json_route("GET", "/v1/auth/me/groups", 200, {"groups": []})
        self.assertEqual(client_with(ft).my_groups(), [])
        self.assertTrue(ft.requests[0].url.endswith("/v1/auth/me/groups"))

    def test_empty_identifiers_are_rejected_locally(self) -> None:
        ft = FakeTransport()
        c = client_with(ft)
        for call in (
            lambda: c.get_group(""),
            lambda: c.delete_group(""),
            lambda: c.list_group_members(""),
            lambda: c.set_group_project_creation("", True),
            lambda: c.add_group_member(TEST_GROUP_ID, ""),
            lambda: c.remove_group_member("", TEST_USER_ID),
        ):
            with self.assertRaises(ValueError):
                call()
        self.assertEqual(ft.requests, [])

    def test_group_bindings_are_deliberately_absent(self) -> None:
        # Binding a group at a scope is a different authority (member:manage
        # there, capped by your own bound role) and a durable grant of access.
        # It belongs in something that plans and diffs — Terraform's
        # janus_group_binding — not a one-line call from a read-mostly secret
        # client. This test makes adding one a deliberate decision.
        binding_like = [name for name in dir(Client) if "bind" in name.lower()]
        self.assertEqual(binding_like, [])


if __name__ == "__main__":  # pragma: no cover
    unittest.main()
