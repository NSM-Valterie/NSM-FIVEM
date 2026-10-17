"""Acceptance tests for the NSM server-key candidate (design section 3 matrix).

Run: python3 -m unittest tools.nsm-keyservice.test_nsm_keyservice -v
or:  python3 -m unittest discover -s tools/nsm-keyservice -v
Stdlib only. Each test runs the real HTTP service on an ephemeral port.
"""
import json
import os
import sys
import tempfile
import threading
import time
import unittest
import urllib.request
import urllib.error
from http.server import ThreadingHTTPServer

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import nsm_keyservice as svc


class KeyServiceTest(unittest.TestCase):
    mode = "public"
    admin = "test-admin-token"

    @classmethod
    def setUpClass(cls):
        cls.tmp = tempfile.TemporaryDirectory()
        svc._state_path = os.path.join(cls.tmp.name, "state.json")
        svc._state = {"keys": {}, "servers": {}}

        class H(svc.Handler):
            pass
        H.mode = cls.mode
        cls.httpd = ThreadingHTTPServer(("127.0.0.1", 0), H)
        cls.port = cls.httpd.server_address[1]
        cls.thread = threading.Thread(target=cls.httpd.serve_forever, daemon=True)
        cls.thread.start()
        os.environ["NSM_KEY_ADMIN_TOKEN"] = cls.admin

    @classmethod
    def tearDownClass(cls):
        cls.httpd.shutdown()
        cls.tmp.cleanup()

    def setUp(self):
        svc._state = {"keys": {}, "servers": {}}

    # helpers ---------------------------------------------------------------
    def call(self, method, path, body=None, bearer=None):
        req = urllib.request.Request(
            "http://127.0.0.1:%d%s" % (self.port, path),
            method=method,
            data=json.dumps(body).encode() if body is not None else None,
            headers={"Content-Type": "application/json",
                     **({"Authorization": "Bearer " + bearer} if bearer else {})})
        try:
            with urllib.request.urlopen(req) as resp:
                return resp.status, json.loads(resp.read().decode() or "{}")
        except urllib.error.HTTPError as e:
            return e.code, json.loads(e.read().decode() or "{}")

    def issue(self, account="acct-a", ttl=None):
        code, out = self.call("POST", "/api/keys",
                              {"account": account, "ttlSeconds": ttl}, self.admin)
        self.assertEqual(code, 201, out)
        return out["key"], out["id"]

    def heartbeat(self, key=None, endpoint="127.0.0.1:30120"):
        return self.call("POST", "/api/serverlist/ingress",
                         {"endPoint": endpoint, "hostname": "Test", "clients": 2}, key)

    def listed(self):
        code, out = self.call("GET", "/api/servers")
        self.assertEqual(code, 200)
        return out["servers"]

    # matrix ------------------------------------------------------------------
    def test_valid_key_lists_server(self):
        key, _ = self.issue()
        code, _ = self.heartbeat(key)
        self.assertEqual(code, 200)
        self.assertEqual(len(self.listed()), 1)

    def test_unknown_and_malformed_keys_rejected_not_listed(self):
        for bad in ("nsm_srv_deadbeef00.nope", "garbage", "nsm_srv_.x"):
            code, _ = self.heartbeat(bad)
            self.assertIn(code, (401, 403), bad)
        self.assertEqual(self.listed(), [])

    def test_expired_key_rejected_not_listed(self):
        key, _ = self.issue(ttl=1)
        # force expiry deterministically instead of sleeping
        pid = key.split(".", 1)[0].replace(svc.KEY_PREFIX, "")
        svc._state["keys"][pid]["expires"] = int(time.time()) - 10
        code, _ = self.heartbeat(key)
        self.assertEqual(code, 403)
        self.assertEqual(self.listed(), [])

    def test_revoked_key_rejected_not_listed(self):
        key, kid = self.issue()
        code, _ = self.call("POST", "/api/keys/%s/revoke" % kid,
                            {"account": "acct-a"}, self.admin)
        self.assertEqual(code, 200)
        code, _ = self.heartbeat(key)
        self.assertEqual(code, 403)
        self.assertEqual(self.listed(), [])

    def test_no_key_rejected_in_public_mode(self):
        if self.mode != "public":
            self.skipTest("public-mode expectation; localtest labels no-key heartbeats localOnly")
        code, _ = self.heartbeat(None)
        self.assertEqual(code, 401)
        self.assertEqual(self.listed(), [])

    def test_other_account_cannot_revoke_or_claim(self):
        key_a, kid_a = self.issue("acct-a")
        # acct-b cannot revoke acct-a's key
        code, _ = self.call("POST", "/api/keys/%s/revoke" % kid_a,
                            {"account": "acct-b"}, self.admin)
        self.assertEqual(code, 403)
        # acct-b's key cannot claim acct-a's registered endpoint
        code, _ = self.heartbeat(key_a, "127.0.0.1:30120")
        self.assertEqual(code, 200)
        key_b, _ = self.issue("acct-b")
        code, _ = self.heartbeat(key_b, "127.0.0.1:30120")
        self.assertEqual(code, 403)

    def test_secret_never_stored_in_plaintext(self):
        key, _ = self.issue()
        secret = key.split(".", 1)[1]
        with open(svc._state_path, encoding="utf-8") as fh:
            self.assertNotIn(secret, fh.read())

    def test_admin_disabled_without_token(self):
        saved = os.environ.pop("NSM_KEY_ADMIN_TOKEN")
        try:
            code, _ = self.call("POST", "/api/keys", {"account": "x"}, "anything")
            self.assertEqual(code, 503)
        finally:
            os.environ["NSM_KEY_ADMIN_TOKEN"] = saved


class LocaltestModeTest(KeyServiceTest):
    mode = "localtest"

    def test_no_key_accepted_but_labelled_local_only(self):
        code, _ = self.heartbeat(None)
        self.assertEqual(code, 200)
        servers = self.listed()
        self.assertEqual(len(servers), 1)
        self.assertTrue(servers[0]["localOnly"])


if __name__ == "__main__":
    unittest.main()
