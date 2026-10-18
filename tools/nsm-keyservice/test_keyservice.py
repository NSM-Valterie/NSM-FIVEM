"""Automated test suite for the NSM Server Key & Registration candidate.

Run with:
  python3 -m unittest discover -s tools/nsm-keyservice -v

Covers the full test matrix from docs/nsm-audit-v077/KEYMASTER-SERVERKEY-AUDIT.md §3:
1. Valid, unexpired NSM key -> registration + heartbeat 200, server listed.
2. Unknown / malformed / wrong-secret key -> 401/403, not listed, no key material leaked.
3. Expired NSM key -> 403, not listed (and evicted if previously listed).
4. Revoked NSM key -> 403, immediately evicted and not listed on subsequent heartbeats.
5. No key in public mode -> 401, rejected, not listed (fail-closed).
6. No key in localtest mode -> 200 (clearly labeled localtest), while invalid/expired/revoked
   keys in localtest mode still fail closed.
7. Cross-account IDOR & endpoint hijacking -> 403; Account B cannot revoke Account A's key
   or overwrite Account A's active server endpoint.
8. Non-impersonation & secret-redaction guardrails -> Rockstar/Cfx entitlement routes return
   404 out_of_scope_upstream_only; public listings never leak key material.
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
import keyservice as ks


class FakeClock:
    def __init__(self, initial: float = 1_700_000_000.0):
        self.now = initial

    def __call__(self) -> float:
        return self.now

    def advance(self, seconds: float) -> None:
        self.now += seconds


class _HTTPTestHarness:
    def __init__(self, mode: str = "public"):
        self.clock = FakeClock()
        self.store = ks.KeyStore(clock=self.clock)
        self.store.register_account_session("acct_alice", "tok_alice_secret")
        self.store.register_account_session("acct_bob", "tok_bob_secret")
        self.registry = ks.AuthenticatedRegistry(
            self.store, ttl_seconds=300.0, clock=self.clock
        )
        handler_cls = ks.make_handler(self.store, self.registry, mode=mode)
        self.server = ThreadingHTTPServer(("127.0.0.1", 0), handler_cls)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()

    def close(self) -> None:
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
            payload_bytes: bytes | None = None
            if body is not None:
                if isinstance(body, (bytes, bytearray)):
                    payload_bytes = bytes(body)
                elif isinstance(body, str):
                    payload_bytes = body.encode("utf-8")
                else:
                    payload_bytes = json.dumps(body).encode("utf-8")
                hdrs.setdefault("Content-Length", str(len(payload_bytes)))
            conn.request(method, path, payload_bytes, hdrs)
            resp = conn.getresponse()
            return resp.status, resp.read()
        finally:
            conn.close()


class KeyServiceMatrixTests(unittest.TestCase):
    def setUp(self) -> None:
        self.h = _HTTPTestHarness(mode="public")

    def tearDown(self) -> None:
        self.h.close()

    def _issue_key_http(
        self,
        account_id: str = "acct_alice",
        token: str = "tok_alice_secret",
        label: str = "Alice Main Server",
        ttl_seconds: float = 3600,
    ) -> tuple[str, dict]:
        status, raw = self.h.request(
            "POST",
            "/api/nsm/keys/issue",
            {"label": label, "ttlSeconds": ttl_seconds},
            {
                "X-NSM-Account-Id": account_id,
                "X-NSM-Account-Token": token,
            },
        )
        self.assertEqual(status, 201, raw)
        body = json.loads(raw)
        return body["serverKey"], body["key"]

    def test_1_valid_unexpired_key_registers_and_lists_server(self) -> None:
        server_key, meta = self._issue_key_http()
        self.assertTrue(server_key.startswith("nsm_srv_"))

        # Register server via NSM registration endpoint
        reg_status, reg_raw = self.h.request(
            "POST",
            "/api/nsm/servers/register",
            {"port": 30120},
            {"Authorization": f"Bearer {server_key}"},
        )
        self.assertEqual(reg_status, 200)
        reg_body = json.loads(reg_raw)
        self.assertTrue(reg_body["listingToken"].startswith("nsm_lt_"))
        self.assertEqual(reg_body["keyId"], meta["keyId"])

        # Send authenticated heartbeat
        hb = {
            "port": 30120,
            "listingToken": reg_body["listingToken"],
            "fallbackData": {
                "dynamic": {"hostname": "Alice RP", "clients": 4, "svMaxclients": 32},
                "info": {"resources": ["baseevents", "spawnmanager"]},
            },
        }
        hb_status, hb_raw = self.h.request(
            "POST",
            "/api/serverlist/ingress",
            hb,
            {"Authorization": f"Bearer {server_key}"},
        )
        self.assertEqual(hb_status, 200, hb_raw)

        # Verify listed in single and protobuf stream
        s_status, s_raw = self.h.request("GET", "/api/servers/single/127.0.0.1:30120")
        self.assertEqual(s_status, 200)
        s_body = json.loads(s_raw)
        self.assertEqual(s_body["OwnerId"], "acct_alice")
        self.assertEqual(s_body["Data"]["hostname"], "Alice RP")
        self.assertEqual(s_body["Data"]["vars"]["gamename"], "gta5")
        self.assertEqual(s_body["Data"]["vars"]["nsm_listing_mode"], "authenticated")
        # Ensure raw key never appears in listing output
        self.assertNotIn(server_key, s_raw.decode("utf-8"))

        st_status, st_raw = self.h.request("GET", "/api/servers/streamRedir/")
        self.assertEqual(st_status, 200)
        self.assertIn(b"127.0.0.1:30120", st_raw)
        self.assertIn(b"Alice RP", st_raw)

    def test_2_unknown_malformed_or_wrong_secret_key_rejected_and_not_listed(self) -> None:
        server_key, meta = self._issue_key_http()
        wrong_secret_key = f"nsm_srv_{meta['keyId']}." + ("A" * 43)
        unknown_id_key = "nsm_srv_0011223344556677." + ("B" * 43)

        bad_keys = [
            ("malformed_prefix", "cfx_key_12345", 401),
            ("malformed_no_dot", "nsm_srv_0011223344556677", 401),
            ("malformed_short_secret", f"nsm_srv_{meta['keyId']}.short", 401),
            ("unknown_key_id", unknown_id_key, 403),
            ("wrong_secret", wrong_secret_key, 403),
        ]
        for label, candidate, expected_code in bad_keys:
            with self.subTest(case=label):
                status, raw = self.h.request(
                    "POST",
                    "/api/serverlist/ingress",
                    {"port": 30121, "fallbackData": {"dynamic": {"clients": 1}}},
                    {"Authorization": f"Bearer {candidate}"},
                )
                self.assertEqual(status, expected_code)
                # Must never echo raw secret in error response
                self.assertNotIn(candidate, raw.decode("utf-8"))

        # Confirm server 30121 was NEVER listed
        s_status, _ = self.h.request("GET", "/api/servers/single/127.0.0.1:30121")
        self.assertEqual(s_status, 404)

    def test_3_expired_key_rejected_and_evicted(self) -> None:
        server_key, _ = self._issue_key_http(ttl_seconds=60)
        # Valid initially
        status, _ = self.h.request(
            "POST",
            "/api/serverlist/ingress",
            {"port": 30122, "fallbackData": {"dynamic": {"clients": 2}}},
            {"Authorization": f"Bearer {server_key}"},
        )
        self.assertEqual(status, 200)

        # Advance clock past key expiry (61s < 300s listing TTL, so only key expiry triggers)
        self.h.clock.advance(61)

        # Next heartbeat rejected with 403 expired_key
        status, raw = self.h.request(
            "POST",
            "/api/serverlist/ingress",
            {"port": 30122, "fallbackData": {"dynamic": {"clients": 2}}},
            {"Authorization": f"Bearer {server_key}"},
        )
        self.assertEqual(status, 403)
        self.assertEqual(json.loads(raw)["lastError"], "expired_key")

        # Existing listing is also pruned because its key expired
        s_status, _ = self.h.request("GET", "/api/servers/single/127.0.0.1:30122")
        self.assertEqual(s_status, 404)

    def test_4_revoked_key_rejected_and_immediately_evicted(self) -> None:
        server_key, meta = self._issue_key_http(ttl_seconds=3600)
        status, _ = self.h.request(
            "POST",
            "/api/serverlist/ingress",
            {"port": 30123, "fallbackData": {"dynamic": {"clients": 5}}},
            {"Authorization": f"Bearer {server_key}"},
        )
        self.assertEqual(status, 200)

        # Owner revokes key
        rev_status, rev_raw = self.h.request(
            "POST",
            "/api/nsm/keys/revoke",
            {"keyId": meta["keyId"]},
            {
                "X-NSM-Account-Id": "acct_alice",
                "X-NSM-Account-Token": "tok_alice_secret",
            },
        )
        self.assertEqual(rev_status, 200, rev_raw)
        self.assertEqual(json.loads(rev_raw)["evictedListings"], 1)

        # Immediately unlisted even before 5-min TTL
        s_status, _ = self.h.request("GET", "/api/servers/single/127.0.0.1:30123")
        self.assertEqual(s_status, 404)

        # Subsequent heartbeat with revoked key fails with 403 revoked_key
        hb_status, hb_raw = self.h.request(
            "POST",
            "/api/serverlist/ingress",
            {"port": 30123, "fallbackData": {"dynamic": {"clients": 5}}},
            {"Authorization": f"Bearer {server_key}"},
        )
        self.assertEqual(hb_status, 403)
        self.assertEqual(json.loads(hb_raw)["lastError"], "revoked_key")

    def test_5_no_key_in_public_mode_rejected_fail_closed(self) -> None:
        status, raw = self.h.request(
            "POST",
            "/api/serverlist/ingress",
            {"port": 30124, "fallbackData": {"dynamic": {"clients": 1}}},
        )
        self.assertEqual(status, 401)
        self.assertEqual(json.loads(raw)["lastError"], "missing_key")
        s_status, _ = self.h.request("GET", "/api/servers/single/127.0.0.1:30124")
        self.assertEqual(s_status, 404)

    def test_6_localtest_mode_allows_no_key_but_rejects_bad_keys(self) -> None:
        local_h = _HTTPTestHarness(mode="localtest")
        try:
            # Keyless loopback heartbeat succeeds in localtest mode and is labeled localtest
            status, _ = local_h.request(
                "POST",
                "/api/serverlist/ingress",
                {"port": 30125, "fallbackData": {"dynamic": {"hostname": "Local Dev", "clients": 0}}},
            )
            self.assertEqual(status, 200)
            s_status, s_raw = local_h.request("GET", "/api/servers/single/127.0.0.1:30125")
            self.assertEqual(s_status, 200)
            self.assertEqual(
                json.loads(s_raw)["Data"]["vars"]["nsm_listing_mode"], "localtest"
            )

            # Even in localtest mode, supplying an invalid key MUST fail closed
            bad_status, _ = local_h.request(
                "POST",
                "/api/serverlist/ingress",
                {"port": 30126, "fallbackData": {"dynamic": {"clients": 0}}},
                {"Authorization": "Bearer nsm_srv_deadbeefdeadbeef." + ("Z" * 43)},
            )
            self.assertEqual(bad_status, 403)
            s2_status, _ = local_h.request("GET", "/api/servers/single/127.0.0.1:30126")
            self.assertEqual(s2_status, 404)
        finally:
            local_h.close()

    def test_7_cross_account_idor_and_endpoint_hijack_prevented(self) -> None:
        alice_key, alice_meta = self._issue_key_http(
            account_id="acct_alice", token="tok_alice_secret", label="Alice Server"
        )
        bob_key, _ = self._issue_key_http(
            account_id="acct_bob", token="tok_bob_secret", label="Bob Server"
        )

        # Alice lists her server on port 30127
        status, _ = self.h.request(
            "POST",
            "/api/serverlist/ingress",
            {"port": 30127, "fallbackData": {"dynamic": {"hostname": "Alice Official", "clients": 10}}},
            {"Authorization": f"Bearer {alice_key}"},
        )
        self.assertEqual(status, 200)

        # Bob attempts IDOR revocation of Alice's key -> 403 Forbidden
        idor_rev_status, _ = self.h.request(
            "POST",
            "/api/nsm/keys/revoke",
            {"keyId": alice_meta["keyId"]},
            {
                "X-NSM-Account-Id": "acct_bob",
                "X-NSM-Account-Token": "tok_bob_secret",
            },
        )
        self.assertEqual(idor_rev_status, 403)

        # Bob lists his own keys -> must NOT contain Alice's key
        bob_list_status, bob_list_raw = self.h.request(
            "GET",
            "/api/nsm/keys",
            headers={
                "X-NSM-Account-Id": "acct_bob",
                "X-NSM-Account-Token": "tok_bob_secret",
            },
        )
        self.assertEqual(bob_list_status, 200)
        bob_key_ids = [k["keyId"] for k in json.loads(bob_list_raw)["keys"]]
        self.assertNotIn(alice_meta["keyId"], bob_key_ids)

        # Bob attempts to overwrite Alice's active 127.0.0.1:30127 endpoint using Bob's valid key -> 403
        hijack_status, hijack_raw = self.h.request(
            "POST",
            "/api/serverlist/ingress",
            {"port": 30127, "fallbackData": {"dynamic": {"hostname": "Bob Hijacked", "clients": 99}}},
            {"Authorization": f"Bearer {bob_key}"},
        )
        self.assertEqual(hijack_status, 403)
        self.assertEqual(
            json.loads(hijack_raw)["lastError"], "endpoint_owned_by_another_account"
        )

        # Verify Alice's listing is untouched
        s_status, s_raw = self.h.request("GET", "/api/servers/single/127.0.0.1:30127")
        self.assertEqual(s_status, 200)
        s_body = json.loads(s_raw)
        self.assertEqual(s_body["OwnerId"], "acct_alice")
        self.assertEqual(s_body["Data"]["hostname"], "Alice Official")

    def test_8_entitlement_and_cfx_routes_never_impersonated_and_secrets_redacted(self) -> None:
        for path in (
            "/api/validate/entitlement",
            "/api/validate/entitlement/rosfive",
            "/api/validate/entitlement/ros2",
            "/api/register/?v=2",
        ):
            for method in ("GET", "POST"):
                status, raw = self.h.request(method, path, {})
                self.assertEqual(status, 404)
                self.assertEqual(
                    json.loads(raw)["error"], "out_of_scope_upstream_only"
                )

        # Secret redaction helper check
        sample = "Failed auth for token nsm_srv_0123456789abcdef.SecretValue_12345678901234567890"
        self.assertEqual(
            ks.redact_secrets(sample),
            "Failed auth for token nsm_srv_[REDACTED]",
        )

    def test_9_e2e_dedicated_server_two_player_heartbeat_and_counts(self) -> None:
        server_key, _ = self._issue_key_http(label="NSM 2-Player Acceptance Server")
        # Initial boot heartbeat (0 players)
        hb0 = {
            "port": 30130,
            "fallbackData": {
                "players": [],
                "dynamic": {"hostname": "NSM Dedicated #1", "clients": 0, "svMaxclients": 48},
                "info": {"version": "nsm-server 1.0.0", "resources": ["hardcap", "sessionmanager"]},
            },
        }
        status, _ = self.h.request(
            "POST",
            "/api/serverlist/ingress",
            hb0,
            {"Authorization": f"Bearer {server_key}"},
        )
        self.assertEqual(status, 200)

        # Two players join -> next heartbeat reports 2 connected players
        hb2 = {
            "port": 30130,
            "fallbackData": {
                "players": [
                    {"id": 1, "name": "PlayerOne", "ping": 24},
                    {"id": 2, "name": "PlayerTwo", "ping": 31},
                ],
                "dynamic": {"hostname": "NSM Dedicated #1", "clients": 2, "svMaxclients": 48},
                "info": {"version": "nsm-server 1.0.0", "resources": ["hardcap", "sessionmanager"]},
            },
        }
        status, _ = self.h.request(
            "POST",
            "/api/serverlist/ingress",
            hb2,
            {"Authorization": f"Bearer {server_key}"},
        )
        self.assertEqual(status, 200)

        # Verify /runtime/counts.json and single endpoint reflect 2 players and 1 active server
        c_status, c_raw = self.h.request("GET", "/runtime/counts.json")
        self.assertEqual(c_status, 200)
        self.assertEqual(json.loads(c_raw), [2, 1, 2])

        # Verify ticket pubkey returns 503 (never serves committed sample private key)
        tk_status, tk_raw = self.h.request("GET", "/api/ticket/pubkey")
        self.assertEqual(tk_status, 503)
        self.assertEqual(json.loads(tk_raw)["error"], "ticket_pubkey_not_configured")

    def test_10_verify_dedicated_server_acceptance_helper(self) -> None:
        import verify_dedicated_server as vds

        server_key, _ = self._issue_key_http(label="Verifier Test Server")
        hb = {
            "port": 30140,
            "fallbackData": {
                "players": [{"id": 1, "name": "P1"}, {"id": 2, "name": "P2"}],
                "dynamic": {"hostname": "Verifier Server", "clients": 2, "svMaxclients": 32},
                "info": {"version": "nsm-server 1.0.0", "resources": ["spawnmanager"]},
            },
        }
        status, _ = self.h.request(
            "POST",
            "/api/serverlist/ingress",
            hb,
            {"Authorization": f"Bearer {server_key}"},
        )
        self.assertEqual(status, 200)

        host, port = self.h.server.server_address
        report = vds.verify_acceptance(
            server_endpoint="127.0.0.1:30140",
            masterlist_url=f"http://{host}:{port}",
            min_players=2,
            check_direct_server_http=False,
        )
        self.assertTrue(report["allPassed"], report)


if __name__ == "__main__":
    unittest.main()
