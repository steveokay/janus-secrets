"""Unit tests for the Janus Python SDK client (reads + cache + errors).

Everything runs against an injected FakeTransport with a controllable clock —
NO live network.
"""

from __future__ import annotations

import json
import unittest
from typing import Dict, Tuple

from janus_client import (
    Client,
    Forbidden,
    JanusError,
    NotFound,
    Sealed,
    Unauthorized,
)

from .fake_transport import Counter, FakeTransport

# obviously-fake, low-entropy fixtures (not real secrets)
TEST_TOKEN = "janus_svc_test-token-000"
TEST_CONFIG_ID = "cfg-00000000-0000-0000-0000-000000000001"
SECRETS_PATH = "/v1/configs/%s/secrets?reveal=true" % TEST_CONFIG_ID


class FakeClock:
    """Deterministic clock; advance() moves virtual time forward."""

    def __init__(self, start: float = 1000.0) -> None:
        self.t = start

    def __call__(self) -> float:
        return self.t

    def advance(self, seconds: float) -> None:
        self.t += seconds


def reveal_transport(secrets: Dict[str, str]) -> Tuple["FakeTransport", "Counter"]:
    ft = FakeTransport()
    counter = ft.json_route(
        "GET", SECRETS_PATH, 200, {"version": 3, "secrets": secrets}
    )
    return ft, counter


class TestReads(unittest.TestCase):
    def test_get_secrets_sends_bearer_and_parses(self) -> None:
        ft, _ = reveal_transport(
            {"DATABASE_URL": "postgres://fake", "API_KEY": "abc-123-fake"}
        )
        c = Client("https://janus.example", token=TEST_TOKEN, transport=ft)
        got = c.get_secrets(TEST_CONFIG_ID)
        self.assertEqual(got["DATABASE_URL"], "postgres://fake")
        self.assertEqual(got["API_KEY"], "abc-123-fake")
        # Bearer header sent, and the reveal endpoint was called.
        self.assertEqual(
            ft.requests[-1].headers.get("Authorization"), "Bearer " + TEST_TOKEN
        )
        self.assertTrue(ft.requests[-1].url.endswith(SECRETS_PATH))

    def test_get_secret_single_and_missing(self) -> None:
        ft, _ = reveal_transport({"KEY_A": "val-a-fake"})
        c = Client("https://janus.example", token=TEST_TOKEN, transport=ft)
        self.assertEqual(c.get_secret(TEST_CONFIG_ID, "KEY_A"), "val-a-fake")
        with self.assertRaises(NotFound):
            c.get_secret(TEST_CONFIG_ID, "MISSING")

    def test_returned_dict_is_copy(self) -> None:
        ft, _ = reveal_transport({"K": "v-fake"})
        c = Client("https://janus.example", token=TEST_TOKEN, transport=ft)
        m1 = c.get_secrets(TEST_CONFIG_ID)
        m1["K"] = "mutated"
        m1["EXTRA"] = "x"
        m2 = c.get_secrets(TEST_CONFIG_ID)
        self.assertEqual(m2, {"K": "v-fake"})


class TestCache(unittest.TestCase):
    def test_hit_and_expiry(self) -> None:
        ft, counter = reveal_transport({"K": "v-fake"})
        clock = FakeClock()
        c = Client(
            "https://janus.example",
            token=TEST_TOKEN,
            transport=ft,
            cache_ttl=30.0,
            clock=clock,
        )
        c.get_secrets(TEST_CONFIG_ID)  # miss
        clock.advance(29)
        c.get_secrets(TEST_CONFIG_ID)  # hit
        self.assertEqual(counter.n, 1)
        clock.advance(2)
        c.get_secrets(TEST_CONFIG_ID)  # miss again (past TTL)
        self.assertEqual(counter.n, 2)

    def test_disabled_always_hits(self) -> None:
        ft, counter = reveal_transport({"K": "v-fake"})
        c = Client("https://janus.example", token=TEST_TOKEN, transport=ft, cache_ttl=0)
        for _ in range(3):
            c.get_secrets(TEST_CONFIG_ID)
        self.assertEqual(counter.n, 3)

    def test_refresh_evicts(self) -> None:
        ft, counter = reveal_transport({"K": "v-fake"})
        c = Client(
            "https://janus.example", token=TEST_TOKEN, transport=ft, cache_ttl=3600
        )
        c.get_secrets(TEST_CONFIG_ID)
        c.refresh(TEST_CONFIG_ID)
        c.get_secrets(TEST_CONFIG_ID)
        self.assertEqual(counter.n, 2)
        c.refresh()  # clear all
        c.get_secrets(TEST_CONFIG_ID)
        self.assertEqual(counter.n, 3)


class TestErrors(unittest.TestCase):
    def _error_client(self, status: int, code: str) -> Client:
        ft = FakeTransport()
        body = json.dumps({"error": {"code": code, "message": "fake message"}}).encode()
        ft.route("GET", SECRETS_PATH, lambda _req: (status, body))
        return Client("https://janus.example", token=TEST_TOKEN, transport=ft)

    def test_typed_errors(self) -> None:
        cases = [
            (401, "unauthorized", Unauthorized),
            (403, "forbidden", Forbidden),
            (404, "not_found", NotFound),
            (503, "sealed", Sealed),
        ]
        for status, code, exc_type in cases:
            with self.subTest(status=status):
                c = self._error_client(status, code)
                with self.assertRaises(exc_type) as ctx:
                    c.get_secrets(TEST_CONFIG_ID)
                err = ctx.exception
                self.assertIsInstance(err, JanusError)
                self.assertEqual(err.status, status)
                self.assertEqual(err.code, code)
                self.assertIn(code, str(err))

    def test_sealed_by_code_on_non_503(self) -> None:
        # A "sealed" code on some other status still maps to Sealed.
        c = self._error_client(500, "sealed")
        with self.assertRaises(Sealed):
            c.get_secrets(TEST_CONFIG_ID)

    def test_error_envelope_never_holds_value(self) -> None:
        c = self._error_client(403, "forbidden")
        try:
            c.get_secrets(TEST_CONFIG_ID)
        except JanusError as err:
            # Only code + message from the (value-free) envelope.
            self.assertEqual(err.message, "fake message")


class TestValidation(unittest.TestCase):
    def test_empty_base_url(self) -> None:
        with self.assertRaises(ValueError):
            Client("")

    def test_empty_config_and_key(self) -> None:
        c = Client("https://janus.example", token=TEST_TOKEN, transport=FakeTransport())
        with self.assertRaises(ValueError):
            c.get_secrets("")
        with self.assertRaises(ValueError):
            c.get_secret(TEST_CONFIG_ID, "")

    def test_no_token_omits_auth_header(self) -> None:
        ft, _ = reveal_transport({"K": "v-fake"})
        c = Client("https://janus.example", transport=ft)
        c.get_secrets(TEST_CONFIG_ID)
        self.assertNotIn("Authorization", ft.requests[-1].headers)


if __name__ == "__main__":
    unittest.main()
