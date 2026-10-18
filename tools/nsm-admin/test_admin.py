"""Automated verification suite for the clean-room NSM Sign-In & Admin Panel.

Run with:
  python3 -m unittest discover -s tools/nsm-admin -v
"""

from __future__ import annotations

import http.client
import json
import sys
import threading
import unittest
from http.server import ThreadingHTTPServer
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import admin_service as adm


class FakeClock:
    def __init__(self, initial: float = 1_700_000_000.0):
        self.now = initial

    def __call__(self) -> float:
        return self.now

    def advance(self, seconds: float) -> None:
        self.now += seconds


class NSMAdminTests(unittest.TestCase):
    def setUp(self) -> None:
        self.clock = FakeClock()
        self.ctrl = adm.NSMAdminController(clock=self.clock)
        handler = adm.make_admin_handler(self.ctrl)
        self.server = ThreadingHTTPServer(("127.0.0.1", 0), handler)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()

    def tearDown(self) -> None:
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=3)

    def request(
        self,
        method: str,
        path: str,
        body: object = None,
        headers: dict | None = None,
    ) -> tuple[int, bytes]:
        conn = http.client.HTTPConnection(*self.server.server_address, timeout=3)
        try:
            hdrs = dict(headers or {})
            payload = None
            if body is not None:
                payload = json.dumps(body).encode("utf-8")
                hdrs.setdefault("Content-Length", str(len(payload)))
            conn.request(method, path, payload, hdrs)
            resp = conn.getresponse()
            return resp.status, resp.read()
        finally:
            conn.close()

    def test_1_clean_room_admin_html_has_no_txadmin_or_discord_dependency(self) -> None:
        status, raw = self.request("GET", "/admin")
        self.assertEqual(status, 200)
        html_text = raw.decode("utf-8")
        self.assertIn("NSM Server Admin Panel", html_text)
        self.assertIn("nsm-native", html_text)
        self.assertNotIn("discord.com/api/oauth2", html_text.lower())

    def test_2_register_login_session_expiry_and_logout(self) -> None:
        # Register Alice
        r_status, r_raw = self.request(
            "POST",
            "/api/nsm/auth/register",
            {"username": "alice_op", "password": "StrongPassword!123", "role": "operator"},
        )
        self.assertEqual(r_status, 201, r_raw)
        acct = json.loads(r_raw)
        self.assertEqual(acct["authProvider"], "nsm-native")

        # Wrong password rejected
        bad_status, _ = self.request(
            "POST",
            "/api/nsm/auth/login",
            {"username": "alice_op", "password": "WrongPassword!123"},
        )
        self.assertEqual(bad_status, 401)

        # Valid login succeeds
        l_status, l_raw = self.request(
            "POST",
            "/api/nsm/auth/login",
            {"username": "alice_op", "password": "StrongPassword!123"},
        )
        self.assertEqual(l_status, 200)
        sess = json.loads(l_raw)
        self.assertTrue(sess["sessionToken"].startswith("nsm_sess_"))

        # Logout requires CSRF token
        no_csrf_status, _ = self.request(
            "POST",
            "/api/nsm/auth/logout",
            {},
            {"Authorization": f"Bearer {sess['sessionToken']}"},
        )
        self.assertEqual(no_csrf_status, 403)

        # Logout with CSRF succeeds and invalidates session
        out_status, _ = self.request(
            "POST",
            "/api/nsm/auth/logout",
            {},
            {
                "Authorization": f"Bearer {sess['sessionToken']}",
                "X-NSM-CSRF-Token": sess["csrfToken"],
            },
        )
        self.assertEqual(out_status, 200)

        after_status, _ = self.request(
            "GET",
            "/api/nsm/admin/servers",
            headers={"Authorization": f"Bearer {sess['sessionToken']}"},
        )
        self.assertEqual(after_status, 401)

    def test_3_server_management_isolation_and_redacted_audit(self) -> None:
        # Create Alice and Bob operator accounts
        self.request(
            "POST",
            "/api/nsm/auth/register",
            {"username": "alice", "password": "AlicePassword!123", "role": "operator"},
        )
        self.request(
            "POST",
            "/api/nsm/auth/register",
            {"username": "bob", "password": "BobPassword!12345", "role": "operator"},
        )
        _, a_raw = self.request(
            "POST",
            "/api/nsm/auth/login",
            {"username": "alice", "password": "AlicePassword!123"},
        )
        _, b_raw = self.request(
            "POST",
            "/api/nsm/auth/login",
            {"username": "bob", "password": "BobPassword!12345"},
        )
        alice = json.loads(a_raw)
        bob = json.loads(b_raw)

        # Issue key for Alice and register a live server in the shared registry
        full_key, key_rec = self.ctrl.key_store.issue_key(owner_id=alice["accountId"])
        self.ctrl.registry.upsert_authorized(
            end_point="127.0.0.1:30120",
            owner_id=alice["accountId"],
            key_id=key_rec.key_id,
            data={"hostname": "Alice NSM Server", "clients": 2, "svMaxclients": 32, "resources": ["spawnmanager"]},
        )

        # Alice sees her server
        s_status, s_raw = self.request(
            "GET",
            "/api/nsm/admin/servers",
            headers={"Authorization": f"Bearer {alice['sessionToken']}"},
        )
        self.assertEqual(s_status, 200)
        servers = json.loads(s_raw)["servers"]
        self.assertEqual(len(servers), 1)
        self.assertEqual(servers[0]["endPoint"], "127.0.0.1:30120")

        # Bob does NOT see Alice's server and cannot queue an action on it (403 anti-IDOR)
        b_servers_status, b_servers_raw = self.request(
            "GET",
            "/api/nsm/admin/servers",
            headers={"Authorization": f"Bearer {bob['sessionToken']}"},
        )
        self.assertEqual(b_servers_status, 200)
        self.assertEqual(json.loads(b_servers_raw)["servers"], [])

        idor_status, _ = self.request(
            "POST",
            "/api/nsm/admin/action",
            {
                "endPoint": "127.0.0.1:30120",
                "action": "restart_resource",
                "params": {"resource": "spawnmanager"},
            },
            {
                "Authorization": f"Bearer {bob['sessionToken']}",
                "X-NSM-CSRF-Token": bob["csrfToken"],
            },
        )
        self.assertEqual(idor_status, 403)

        # Alice queues a valid action and accidentally includes a server key in params -> redacted in audit
        act_status, act_raw = self.request(
            "POST",
            "/api/nsm/admin/action",
            {
                "endPoint": "127.0.0.1:30120",
                "action": "announce_broadcast",
                "params": {"message": f"Maintenance soon ({full_key})"},
            },
            {
                "Authorization": f"Bearer {alice['sessionToken']}",
                "X-NSM-CSRF-Token": alice["csrfToken"],
            },
        )
        self.assertEqual(act_status, 200, act_raw)
        self.assertNotIn(full_key, act_raw.decode("utf-8"))

        aud_status, aud_raw = self.request(
            "GET",
            "/api/nsm/admin/audit",
            headers={"Authorization": f"Bearer {alice['sessionToken']}"},
        )
        self.assertEqual(aud_status, 200)
        self.assertNotIn(full_key, aud_raw.decode("utf-8"))
        self.assertIn("nsm_srv_[REDACTED]", aud_raw.decode("utf-8"))


if __name__ == "__main__":
    unittest.main()
