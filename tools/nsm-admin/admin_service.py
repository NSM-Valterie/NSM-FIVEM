#!/usr/bin/env python3
"""Clean-room NSM Sign-In & Server Admin Panel Service.

Built from scratch per the NSM-FIVEM Master Prompt:
- Replaces Discord-dependent / Cfx Discourse sign-in with an NSM-owned
  PBKDF2-HMAC-SHA256 account & session authentication service.
- Provides a clean-room NSM Server Admin control plane and web UI rather than
  rebranding txAdmin (`citizen:server:monitor`).
- Enforces strict per-owner isolation (anti-IDOR), CSRF/session token checks,
  and secret-redacted audit logging.
- Touches zero locked v0.7.7 paths.
"""

from __future__ import annotations

import base64
import hashlib
import hmac
import html
import json
import os
import secrets
import sys
import threading
import time
from dataclasses import dataclass, field
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Callable, Dict, List, Optional, Tuple

sys.path.insert(0, str(Path(__file__).resolve().parent.parent / "nsm-keyservice"))
import keyservice as ks

PBKDF2_ITERATIONS = 200_000
DEFAULT_SESSION_TTL = 3600 * 12  # 12 hours
MAX_BODY_BYTES = 128 * 1024
ALLOWED_ADMIN_ACTIONS = frozenset(
    {"restart_resource", "announce_broadcast", "kick_player", "shutdown_server"}
)


def _hash_password(password: str, salt: bytes) -> bytes:
    return hashlib.pbkdf2_hmac(
        "sha256",
        password.encode("utf-8"),
        salt,
        PBKDF2_ITERATIONS,
    )


@dataclass
class NSMAccount:
    account_id: str
    username: str
    role: str
    salt_b64: str
    password_hash_b64: str
    created_at: float


@dataclass
class NSMSession:
    token: str
    csrf_token: str
    account_id: str
    username: str
    role: str
    created_at: float
    expires_at: float


@dataclass
class AuditEvent:
    timestamp: int
    actor_account_id: str
    action: str
    target: str
    detail: str

    def to_dict(self) -> dict:
        return {
            "timestamp": self.timestamp,
            "actorAccountId": self.actor_account_id,
            "action": self.action,
            "target": self.target,
            "detail": ks.redact_secrets(self.detail),
        }


class NSMAdminController:
    """Clean-room NSM Sign-In, Server Control Plane, and Audit Log."""

    def __init__(
        self,
        key_store: Optional[ks.KeyStore] = None,
        registry: Optional[ks.AuthenticatedRegistry] = None,
        clock: Callable[[], float] = time.time,
    ):
        self._lock = threading.Lock()
        self._clock = clock
        self.key_store = key_store or ks.KeyStore(clock=clock)
        self.registry = registry or ks.AuthenticatedRegistry(
            self.key_store, clock=clock
        )
        self._accounts_by_user: Dict[str, NSMAccount] = {}
        self._accounts_by_id: Dict[str, NSMAccount] = {}
        self._sessions: Dict[str, NSMSession] = {}
        self._pending_actions: Dict[str, List[dict]] = {}
        self._audit_log: List[AuditEvent] = []

    def _record_audit_locked(
        self, actor_id: str, action: str, target: str, detail: str = ""
    ) -> None:
        self._audit_log.append(
            AuditEvent(
                timestamp=int(self._clock()),
                actor_account_id=actor_id,
                action=action,
                target=target,
                detail=ks.redact_secrets(detail),
            )
        )

    def create_account(
        self, username: str, password: str, role: str = "operator"
    ) -> Tuple[bool, int, str, Optional[dict]]:
        if not isinstance(username, str) or len(username.strip()) < 3 or len(username.strip()) > 48:
            return False, 400, "username must be 3..48 characters", None
        if not isinstance(password, str) or len(password) < 10:
            return False, 400, "password must be at least 10 characters", None
        if role not in ("admin", "operator", "viewer"):
            return False, 400, "invalid role", None

        clean_user = username.strip().lower()
        now = self._clock()
        salt = os.urandom(16)
        pw_hash = _hash_password(password, salt)

        with self._lock:
            if clean_user in self._accounts_by_user:
                return False, 409, "username_already_exists", None
            account_id = "acct_" + secrets.token_hex(8)
            acct = NSMAccount(
                account_id=account_id,
                username=clean_user,
                role=role,
                salt_b64=base64.b64encode(salt).decode("ascii"),
                password_hash_b64=base64.b64encode(pw_hash).decode("ascii"),
                created_at=now,
            )
            self._accounts_by_user[clean_user] = acct
            self._accounts_by_id[account_id] = acct
            self._record_audit_locked(account_id, "account.register", clean_user, f"role={role}")
            return (
                True,
                201,
                "",
                {
                    "accountId": account_id,
                    "username": clean_user,
                    "role": role,
                    "authProvider": "nsm-native",
                },
            )

    def login(
        self, username: str, password: str, ttl_seconds: float = DEFAULT_SESSION_TTL
    ) -> Tuple[bool, int, str, Optional[dict]]:
        if not isinstance(username, str) or not isinstance(password, str):
            return False, 400, "username and password required", None
        clean_user = username.strip().lower()
        now = self._clock()

        with self._lock:
            acct = self._accounts_by_user.get(clean_user)
            if acct is None:
                return False, 401, "invalid_credentials", None
            salt = base64.b64decode(acct.salt_b64)
            expected_hash = base64.b64decode(acct.password_hash_b64)
            candidate_hash = _hash_password(password, salt)
            if not hmac.compare_digest(expected_hash, candidate_hash):
                self._record_audit_locked(acct.account_id, "auth.login_failed", clean_user)
                return False, 401, "invalid_credentials", None

            token = "nsm_sess_" + secrets.token_urlsafe(32)
            csrf = secrets.token_hex(16)
            sess = NSMSession(
                token=token,
                csrf_token=csrf,
                account_id=acct.account_id,
                username=acct.username,
                role=acct.role,
                created_at=now,
                expires_at=now + float(ttl_seconds),
            )
            self._sessions[token] = sess
            self.key_store.register_account_session(acct.account_id, token)
            self._record_audit_locked(acct.account_id, "auth.login_success", clean_user)
            return (
                True,
                200,
                "",
                {
                    "sessionToken": token,
                    "csrfToken": csrf,
                    "accountId": acct.account_id,
                    "username": acct.username,
                    "role": acct.role,
                    "authProvider": "nsm-native",
                    "expiresAt": int(sess.expires_at),
                },
            )

    def verify_session(self, token: Optional[str]) -> Optional[NSMSession]:
        if not token or not isinstance(token, str):
            return None
        now = self._clock()
        with self._lock:
            sess = self._sessions.get(token.strip())
            if sess is None:
                return None
            if now >= sess.expires_at:
                del self._sessions[token.strip()]
                return None
            return sess

    def logout(self, token: Optional[str]) -> bool:
        if not token:
            return False
        with self._lock:
            sess = self._sessions.pop(token.strip(), None)
            if sess is None:
                return False
            self._record_audit_locked(sess.account_id, "auth.logout", sess.username)
            return True

    def queue_server_action(
        self,
        sess: NSMSession,
        end_point: str,
        action: str,
        params: Optional[dict] = None,
    ) -> Tuple[bool, int, str, Optional[dict]]:
        if sess.role == "viewer":
            return False, 403, "viewer_role_cannot_mutate", None
        if action not in ALLOWED_ADMIN_ACTIONS:
            return False, 400, "unsupported_admin_action", None

        found = self.registry.get(end_point)
        if found is None:
            return False, 404, "server_not_found_or_offline", None
        owner_id, _ = found
        if owner_id != sess.account_id and sess.role != "admin":
            with self._lock:
                self._record_audit_locked(
                    sess.account_id,
                    "admin.action_denied_idor",
                    end_point,
                    f"action={action}",
                )
            return False, 403, "forbidden_not_server_owner", None

        safe_params = {}
        for k, v in (params or {}).items():
            safe_params[str(k)] = ks.redact_secrets(str(v))

        cmd_entry = {
            "commandId": "cmd_" + secrets.token_hex(8),
            "endPoint": end_point,
            "action": action,
            "params": safe_params,
            "issuedBy": sess.account_id,
            "issuedAt": int(self._clock()),
        }
        with self._lock:
            self._pending_actions.setdefault(end_point, []).append(cmd_entry)
            self._record_audit_locked(
                sess.account_id,
                f"admin.{action}",
                end_point,
                json.dumps(safe_params),
            )
        return True, 200, "", cmd_entry

    def list_owned_servers(self, sess: NSMSession) -> List[dict]:
        out = []
        for ep, data in self.registry.entries():
            found = self.registry.get(ep)
            if not found:
                continue
            owner_id, _ = found
            if owner_id == sess.account_id or sess.role == "admin":
                with self._lock:
                    queued = list(self._pending_actions.get(ep, ()))
                out.append(
                    {
                        "endPoint": ep,
                        "ownerId": owner_id,
                        "hostname": data.get("hostname", ""),
                        "clients": data.get("clients", 0),
                        "svMaxclients": data.get("svMaxclients", 0),
                        "resources": data.get("resources", []),
                        "queuedActions": queued,
                    }
                )
        return out

    def list_audit_events(self, sess: NSMSession) -> List[dict]:
        with self._lock:
            return [
                ev.to_dict()
                for ev in self._audit_log
                if ev.actor_account_id == sess.account_id or sess.role == "admin"
            ]


def render_admin_panel_html() -> str:
    """Clean-room NSM Admin HTML view (not derived from txAdmin)."""
    return """<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>NSM Server Admin Control Plane</title>
  <meta name="viewport" content="width=device-width, initial-scale=1">
</head>
<body>
  <header>
    <h1>NSM Server Admin Panel</h1>
    <p>Native NSM authentication &amp; dedicated-server management (clean-room NSM implementation; no Discord OAuth or txAdmin dependency).</p>
  </header>
  <main>
    <section id="auth-status">Authentication provider: <code>nsm-native</code></section>
    <section id="endpoints">
      <h2>Control Plane Endpoints</h2>
      <ul>
        <li><code>POST /api/nsm/auth/register</code> — Create NSM operator account</li>
        <li><code>POST /api/nsm/auth/login</code> — Obtain NSM session &amp; CSRF token</li>
        <li><code>POST /api/nsm/auth/logout</code> — Revoke active session</li>
        <li><code>GET /api/nsm/admin/servers</code> — Inspect owned servers &amp; player counts</li>
        <li><code>POST /api/nsm/admin/action</code> — Queue authorized server action</li>
        <li><code>GET /api/nsm/admin/audit</code> — Inspect redacted audit trail</li>
      </ul>
    </section>
  </main>
</body>
</html>
"""


def make_admin_handler(controller: NSMAdminController):
    class NSMAdminHTTPHandler(BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"
        server_version = "NSM-Admin/0.8-candidate"

        def log_message(self, fmt: str, *args) -> None:
            if os.environ.get("NSM_ADMIN_VERBOSE") == "1":
                safe = ks.redact_secrets(fmt % args if args else fmt)
                print(f"[nsm-admin] {safe}", flush=True)

        def _send(self, code: int, body: bytes | str, ctype: str = "application/json") -> None:
            if isinstance(body, str):
                body = body.encode("utf-8")
            self.send_response(code)
            self.send_header("Content-Type", ctype)
            self.send_header("Content-Length", str(len(body)))
            self.send_header("X-Frame-Options", "DENY")
            self.send_header("X-Content-Type-Options", "nosniff")
            self.end_headers()
            if self.command != "HEAD":
                self.wfile.write(body)

        def _json(self, obj: dict | list, code: int = 200) -> None:
            self._send(code, json.dumps(obj), "application/json")

        def _read_json(self) -> Tuple[Optional[dict], Optional[Tuple[int, str]]]:
            raw_len = self.headers.get("Content-Length")
            if raw_len is None:
                return None, (400, "missing Content-Length")
            try:
                length = int(raw_len)
            except ValueError:
                return None, (400, "invalid Content-Length")
            if length < 0 or length > MAX_BODY_BYTES:
                return None, (413, "payload out of bounds")
            raw = self.rfile.read(length) if length > 0 else b"{}"
            try:
                parsed = json.loads(raw.decode("utf-8"))
            except Exception:
                return None, (400, "malformed JSON")
            if not isinstance(parsed, dict):
                return None, (400, "body must be a JSON object")
            return parsed, None

        def _require_session(self, require_csrf: bool = False) -> Optional[NSMSession]:
            auth = self.headers.get("Authorization") or ""
            token = None
            if auth.lower().startswith("bearer "):
                token = auth[7:].strip()
            elif self.headers.get("X-NSM-Session-Token"):
                token = self.headers.get("X-NSM-Session-Token")
            sess = controller.verify_session(token)
            if sess is None:
                self._json({"error": "unauthorized_session"}, 401)
                return None
            if require_csrf:
                csrf = (self.headers.get("X-NSM-CSRF-Token") or "").strip()
                if not csrf or not hmac.compare_digest(sess.csrf_token, csrf):
                    self._json({"error": "invalid_csrf_token"}, 403)
                    return None
            return sess

        def do_GET(self) -> None:
            path = self.path.split("?", 1)[0]

            if path in ("/admin", "/admin/"):
                self._send(200, render_admin_panel_html(), "text/html; charset=utf-8")
                return

            if path == "/api/nsm/admin/servers":
                sess = self._require_session(require_csrf=False)
                if not sess:
                    return
                self._json({"servers": controller.list_owned_servers(sess)}, 200)
                return

            if path == "/api/nsm/admin/audit":
                sess = self._require_session(require_csrf=False)
                if not sess:
                    return
                self._json({"events": controller.list_audit_events(sess)}, 200)
                return

            self._json({"error": "not_found"}, 404)

        def do_POST(self) -> None:
            path = self.path.split("?", 1)[0]

            if path == "/api/nsm/auth/register":
                body, err = self._read_json()
                if err:
                    self._json({"error": err[1]}, err[0])
                    return
                ok, code, reason, acct = controller.create_account(
                    username=body.get("username", ""),
                    password=body.get("password", ""),
                    role=body.get("role", "operator"),
                )
                if not ok or acct is None:
                    self._json({"error": reason}, code)
                    return
                self._json(acct, code)
                return

            if path == "/api/nsm/auth/login":
                body, err = self._read_json()
                if err:
                    self._json({"error": err[1]}, err[0])
                    return
                ok, code, reason, sess_info = controller.login(
                    username=body.get("username", ""),
                    password=body.get("password", ""),
                )
                if not ok or sess_info is None:
                    self._json({"error": reason}, code)
                    return
                self._json(sess_info, code)
                return

            if path == "/api/nsm/auth/logout":
                sess = self._require_session(require_csrf=True)
                if not sess:
                    return
                controller.logout(sess.token)
                self._json({"loggedOut": True}, 200)
                return

            if path == "/api/nsm/admin/action":
                sess = self._require_session(require_csrf=True)
                if not sess:
                    return
                body, err = self._read_json()
                if err:
                    self._json({"error": err[1]}, err[0])
                    return
                end_point = str(body.get("endPoint", "")).strip()
                action = str(body.get("action", "")).strip()
                params = body.get("params") if isinstance(body.get("params"), dict) else {}
                ok, code, reason, cmd = controller.queue_server_action(
                    sess, end_point=end_point, action=action, params=params
                )
                if not ok or cmd is None:
                    self._json({"error": reason}, code)
                    return
                self._json({"queued": True, "command": cmd}, 200)
                return

            self._json({"error": "not_found"}, 404)

    return NSMAdminHTTPHandler
