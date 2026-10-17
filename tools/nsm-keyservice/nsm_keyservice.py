#!/usr/bin/env python3
"""NSM-owned server-key service (candidate — NOT deployed, NOT wired into
any shipping client/server build).

Purpose: let the NSM masterlist accept heartbeats only from servers that
registered with NSM and hold a valid NSM server key. This attests "this
server is registered with NSM" and nothing more.

Hard boundaries (by design):
- Never gates, replaces, or weakens Rockstar/GTA entitlement, Steam auth,
  or any Cfx validation. Those flows are untouched and out of scope.
- Never impersonates Cfx endpoints (no portal-api.cfx.re / cfx.re paths).
- Never unlocks third-party (Cfx escrow) content.
- Failure is closed: missing/invalid/expired/revoked key => heartbeat is
  rejected (401/403) and the server is NOT listed. No silent acceptance.

Key format: nsm_srv_<public-id>.<secret>
Only a PBKDF2-HMAC-SHA256 hash of the secret (per-key random salt) is
stored server-side. The secret is shown exactly once, at issuance.

Modes (env NSM_KEY_MODE):
- public    (default): heartbeat requires a valid key.
- localtest: heartbeat without a key is accepted and clearly labelled
             local-only. This preserves current v0.7.7 localtest behavior
             for LAN development; it must never be used on a public host.

Admin (key issuance/revocation) requires env NSM_KEY_ADMIN_TOKEN, sent as
Authorization: Bearer <token>. If it is unset, admin routes are disabled
(503) — they are never open. No token or secret is ever written to logs.

State: JSON file at env NSM_KEY_STATE (default ./nsm-keyservice-state.json).
The state file contains hashes, never plaintext secrets.

Run:  NSM_KEY_ADMIN_TOKEN=... NSM_KEY_MODE=public python3 nsm_keyservice.py --port 8080
Test: python3 -m unittest discover -s tools/nsm-keyservice -v
"""

import hashlib
import hmac
import json
import os
import re
import secrets
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlparse

KEY_PREFIX = "nsm_srv_"
HEARTBEAT_TTL = 300  # seconds; mirrors existing masterlist behaviour
PBKDF2_ROUNDS = 120_000
MAX_BODY = 1 << 20  # 1 MiB heartbeat cap, as in the existing masterlist

_lock = threading.Lock()
_state_path = os.environ.get("NSM_KEY_STATE", "./nsm-keyservice-state.json")
_state = {"keys": {}, "servers": {}}  # keys: id -> record; servers: endpoint -> entry


# --------------------------------------------------------------------------
# Key handling
# --------------------------------------------------------------------------

def _hash_secret(secret, salt_hex):
    return hashlib.pbkdf2_hmac(
        "sha256", secret.encode("utf-8"), bytes.fromhex(salt_hex), PBKDF2_ROUNDS
    ).hex()


def _new_key(account, label, ttl_seconds):
    public_id = secrets.token_hex(6)
    secret = secrets.token_urlsafe(32)
    salt = secrets.token_hex(16)
    now = int(time.time())
    rec = {
        "id": public_id,
        "account": account,
        "label": label or "",
        "salt": salt,
        "hash": _hash_secret(secret, salt),
        "created": now,
        "expires": (now + int(ttl_seconds)) if ttl_seconds else None,
        "revoked": False,
    }
    with _lock:
        _state["keys"][public_id] = rec
        _save_locked()
    return "%s%s.%s" % (KEY_PREFIX, public_id, secret), rec


def _parse_key(raw):
    if not raw or not raw.startswith(KEY_PREFIX):
        return None, None
    body = raw[len(KEY_PREFIX):]
    if "." not in body:
        return None, None
    public_id, secret = body.split(".", 1)
    if not re.fullmatch(r"[0-9a-f]+", public_id or "") or not secret:
        return None, None
    return public_id, secret


def _verify_key(raw):
    """Return (record, error_code). error_code None means valid."""
    public_id, secret = _parse_key(raw)
    if public_id is None:
        return None, 401
    with _lock:
        rec = _state["keys"].get(public_id)
    if rec is None:
        return None, 401
    if rec.get("revoked"):
        return rec, 403
    if rec.get("expires") is not None and int(time.time()) > int(rec["expires"]):
        return rec, 403
    candidate = _hash_secret(secret, rec["salt"])
    if not hmac.compare_digest(candidate, rec["hash"]):
        return None, 401
    return rec, None


# --------------------------------------------------------------------------
# Persistence (hashes only — never plaintext secrets)
# --------------------------------------------------------------------------

def _save_locked():
    tmp = _state_path + ".tmp"
    with open(tmp, "w", encoding="utf-8") as fh:
        json.dump(_state, fh, indent=1)
    os.replace(tmp, _state_path)


def _load():
    global _state
    if os.path.exists(_state_path):
        with open(_state_path, encoding="utf-8") as fh:
            loaded = json.load(fh)
        if isinstance(loaded, dict) and "keys" in loaded and "servers" in loaded:
            _state = loaded


# --------------------------------------------------------------------------
# Server registry (heartbeat -> listing)
# --------------------------------------------------------------------------

def _register_server(endpoint, key_rec, payload, mode):
    now = int(time.time())
    with _lock:
        existing = _state["servers"].get(endpoint)
        key_id = key_rec["id"] if key_rec else None
        # Ownership: an endpoint already registered under a different key
        # cannot be claimed or mutated by another key (IDOR guard).
        if existing and existing.get("keyId") and key_id and existing["keyId"] != key_id:
            return 403, "endpoint is registered to a different NSM server key"
        _state["servers"][endpoint] = {
            "endPoint": endpoint,
            "hostname": str(payload.get("hostname") or "NSM Server")[:256],
            "clients": int(payload.get("clients") or 0),
            "keyId": key_id,
            "account": key_rec["account"] if key_rec else None,
            "localOnly": mode == "localtest" and key_id is None,
            "lastSeen": now,
        }
        _save_locked()
    return 200, None


def _live_servers():
    now = int(time.time())
    with _lock:
        items = [dict(v) for v in _state["servers"].values()
                 if now - int(v.get("lastSeen", 0)) <= HEARTBEAT_TTL]
    return sorted(items, key=lambda s: s["endPoint"])


# --------------------------------------------------------------------------
# HTTP
# --------------------------------------------------------------------------

class Handler(BaseHTTPRequestHandler):
    server_version = "NSM-KeyService/0.1-candidate"
    mode = os.environ.get("NSM_KEY_MODE", "public")

    def log_message(self, fmt, *args):  # no key material ever reaches here
        sys.stderr.write("[nsm-keyservice] %s %s\n" % (self.command, fmt % args))

    def _json(self, code, obj):
        body = json.dumps(obj).encode("utf-8")
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        if self.command != "HEAD":
            self.wfile.write(body)

    def _body(self):
        length = int(self.headers.get("Content-Length") or 0)
        if length <= 0:
            return {}
        if length > MAX_BODY:
            raise ValueError("heartbeat body too large")
        raw = self.rfile.read(length)
        return json.loads(raw.decode("utf-8"))

    def _bearer(self):
        auth = self.headers.get("Authorization") or ""
        return auth[7:].strip() if auth.startswith("Bearer ") else ""

    def _require_admin(self):
        token = os.environ.get("NSM_KEY_ADMIN_TOKEN", "")
        if not token:
            self._json(503, {"error": "admin disabled: NSM_KEY_ADMIN_TOKEN not configured"})
            return False
        if not hmac.compare_digest(self._bearer(), token):
            self._json(401, {"error": "admin authorization required"})
            return False
        return True

    # -- routes ------------------------------------------------------------
    def do_GET(self):
        path = urlparse(self.path).path
        if path in ("/", "/health"):
            self._json(200, {"service": "nsm-keyservice", "mode": self.mode,
                             "servers": len(_live_servers())})
        elif path == "/api/servers":
            self._json(200, {"servers": _live_servers()})
        else:
            self._json(404, {"error": "not found"})

    def do_POST(self):
        path = urlparse(self.path).path
        try:
            if path == "/api/keys":
                self._issue_key()
            elif path.startswith("/api/keys/") and path.endswith("/revoke"):
                self._revoke_key(path.split("/")[3])
            elif path == "/api/serverlist/ingress":
                self._ingress()
            else:
                self._json(404, {"error": "not found"})
        except ValueError as exc:
            self._json(400, {"lastError": str(exc)})
        except Exception:  # never leak internals (or key material) to clients
            self._json(500, {"error": "internal error"})

    def _issue_key(self):
        if not self._require_admin():
            return
        body = self._body()
        account = str(body.get("account") or "").strip()
        if not account:
            self._json(400, {"error": "account is required"})
            return
        ttl = body.get("ttlSeconds")
        full_key, rec = _new_key(account, str(body.get("label") or ""),
                                 int(ttl) if ttl else None)
        # The secret is returned exactly once, here. Only its hash is stored.
        self._json(201, {"key": full_key, "id": rec["id"], "account": account,
                         "expires": rec["expires"]})

    def _revoke_key(self, public_id):
        if not self._require_admin():
            return
        body = self._body()
        account = str(body.get("account") or "")
        with _lock:
            rec = _state["keys"].get(public_id)
            if rec is None:
                self._json(404, {"error": "unknown key id"})
                return
            # Only the owning account (via admin) may revoke; a mismatched
            # account is refused so keys cannot be revoked cross-account.
            if account and account != rec["account"]:
                self._json(403, {"error": "key belongs to a different account"})
                return
            rec["revoked"] = True
            _save_locked()
        self._json(200, {"id": public_id, "revoked": True})

    def _ingress(self):
        body = self._body()
        endpoint = str(body.get("endPoint") or "").strip()
        if not endpoint:
            port = int(body.get("port") or 30120)
            endpoint = "%s:%d" % (self.client_address[0], port)
        raw_key = self._bearer()
        if not raw_key:
            if self.mode == "localtest":
                code, err = _register_server(endpoint, None, body, self.mode)
                self._json(code, {"localOnly": True} if code == 200 else {"lastError": err})
                return
            # Closed failure: no key in public mode => not listed.
            self._json(401, {"lastError": "NSM server key required"})
            return
        rec, err_code = _verify_key(raw_key)
        if err_code is not None:
            reason = "NSM server key revoked or expired" if err_code == 403 \
                else "invalid NSM server key"
            self._json(err_code, {"lastError": reason})
            return
        code, err = _register_server(endpoint, rec, body, self.mode)
        self._json(code, {} if code == 200 else {"lastError": err})


def main(argv=None):
    argv = list(sys.argv[1:] if argv is None else argv)
    port = 8080
    if "--port" in argv:
        port = int(argv[argv.index("--port") + 1])
    _load()
    httpd = ThreadingHTTPServer(("0.0.0.0", port), Handler)
    print("nsm-keyservice candidate listening on :%d (mode=%s)" % (port, Handler.mode),
          flush=True)
    httpd.serve_forever()


if __name__ == "__main__":
    main()
