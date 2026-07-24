"""End-to-end test of the default urllib transport against a local
``http.server`` (loopback only, NO external network).

Exercises the real :class:`janus_client._transport.UrllibTransport` path,
including a 503 -> Sealed mapping over a genuine socket.
"""

from __future__ import annotations

import json
import threading
import unittest
from http.server import BaseHTTPRequestHandler, HTTPServer
from typing import Dict

from janus_client import Client, Sealed

TEST_TOKEN = "janus_svc_test-token-000"
TEST_CONFIG_ID = "cfg-00000000-0000-0000-0000-000000000001"


class _Handler(BaseHTTPRequestHandler):
    # Set by the test before serving.
    secrets: Dict[str, str] = {}
    sealed: bool = False
    seen_auth: str = ""

    def do_GET(self) -> None:  # noqa: N802 (stdlib naming)
        type(self).seen_auth = self.headers.get("Authorization", "")
        if type(self).sealed:
            body = json.dumps(
                {"error": {"code": "sealed", "message": "server is sealed"}}
            ).encode()
            self.send_response(503)
        else:
            body = json.dumps({"version": 1, "secrets": type(self).secrets}).encode()
            self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *args: object) -> None:
        pass  # silence


class TestUrllibTransport(unittest.TestCase):
    def setUp(self) -> None:
        _Handler.secrets = {"K": "v-fake"}
        _Handler.sealed = False
        _Handler.seen_auth = ""
        self.server = HTTPServer(("127.0.0.1", 0), _Handler)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        host = self.server.server_address[0]
        port = self.server.server_address[1]
        self.base_url = f"http://{host!s}:{port}"

    def tearDown(self) -> None:
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=5)

    def test_read_over_real_socket(self) -> None:
        c = Client(self.base_url, token=TEST_TOKEN, cache_ttl=0)
        got = c.get_secrets(TEST_CONFIG_ID)
        self.assertEqual(got, {"K": "v-fake"})
        self.assertEqual(_Handler.seen_auth, "Bearer " + TEST_TOKEN)

    def test_sealed_over_real_socket(self) -> None:
        _Handler.sealed = True
        c = Client(self.base_url, token=TEST_TOKEN, cache_ttl=0)
        with self.assertRaises(Sealed):
            c.get_secrets(TEST_CONFIG_ID)


if __name__ == "__main__":
    unittest.main()
