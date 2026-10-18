#!/usr/bin/env python3
"""NSM-owned Server Key & Registration Service (Workstream B candidate).

Additive, standalone implementation of the design in:
  docs/nsm-audit-v077/KEYMASTER-SERVERKEY-AUDIT.md (§3)

Security & scope rules enforced:
- Only authenticates server registration and masterlist heartbeats for NSM's
  own server list.
- Fail-closed: missing (in public mode), malformed, unknown, expired, or
  revoked keys are rejected with 401/403 and NEVER list a server.
- Salted PBKDF2-HMAC-SHA256 key storage + constant-time verification; raw key
  secrets are returned once at issuance and never persisted or logged.
- Strict account ownership (anti-IDOR): accounts cannot view/revoke others'
  keys, and one account's valid key cannot hijack another account's active
  server endpoint.
- Never touches, mocks, or bypasses Rockstar/GTA entitlement validation,
  Steam authentication, Cfx portal-api, or Cfx escrow/DRM.
"""

from __future__ import annotations

import base64
import hashlib
import hmac
import ipaddress
import json
import os
import re
import secrets
import struct
import threading
import time
from dataclasses import dataclass, field
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Callable, Dict, List, Optional, Tuple

KEY_PREFIX = "nsm_srv_"
KEY_ID_HEX_LEN = 16
PBKDF2_ITERATIONS = 200_000
DEFAULT_TTL_SECONDS = 300          # 5-minute listing TTL matching v0.7.7 masterlist
DEFAULT_KEY_TTL_SECONDS = 86_400 * 90  # 90-day default key validity
MAX_HEARTBEAT_BYTES = 256 * 1024
GAMENAME = "gta5"

_KEY_REDACT_RE = re.compile(r"nsm_srv_[A-Za-z0-9_\-]+\.[A-Za-z0-9_\-]+")


def redact_secrets(text: str) -> str:
    """Redact any NSM server key secret from log or error strings."""
    if not text:
        return ""
    return _KEY_REDACT_RE.sub("nsm_srv_[REDACTED]", str(text))


def _hash_secret(secret: str, salt: bytes) -> bytes:
    return hashlib.pbkdf2_hmac(
        "sha256",
        secret.encode("utf-8"),
        salt,
        PBKDF2_ITERATIONS,
    )


def parse_server_key(raw_key: str) -> Optional[Tuple[str, str]]:
    """Parse 'nsm_srv_<16-hex-id>.<secret>' into (key_id, secret), or None."""
    if not isinstance(raw_key, str):
        return None
    token = raw_key.strip()
    if not token.startswith(KEY_PREFIX):
        return None
    rest = token[len(KEY_PREFIX):]
    if "." not in rest:
        return None
    key_id, secret = rest.split(".", 1)
    if len(key_id) != KEY_ID_HEX_LEN:
        return None
    if not all(c in "0123456789abcdef" for c in key_id):
        return None
    if len(secret) < 24 or len(secret) > 128:
        return None
    return key_id, secret


def extract_bearer_or_header_key(headers: Dict[str, str]) -> Optional[str]:
    """Extract server key from Authorization: Bearer ... or X-NSM-Server-Key."""
    auth = headers.get("Authorization") or headers.get("authorization") or ""
    if auth.lower().startswith("bearer "):
        candidate = auth[7:].strip()
        if candidate:
            return candidate
    hdr = headers.get("X-NSM-Server-Key") or headers.get("x-nsm-server-key") or ""
    if hdr.strip():
        return hdr.strip()
    return None


@dataclass
class ServerKeyRecord:
    key_id: str
    owner_id: str
    label: str
    salt_b64: str
    secret_hash_b64: str
    created_at: float
    expires_at: float
    revoked: bool = False
    revoked_at: Optional[float] = None
    bound_endpoint: Optional[str] = None
    last_used_at: Optional[float] = None

    def public_metadata(self) -> dict:
        """Safe metadata view (never exposes salt or secret_hash)."""
        return {
            "keyId": self.key_id,
            "ownerId": self.owner_id,
            "label": self.label,
            "createdAt": int(self.created_at),
            "expiresAt": int(self.expires_at),
            "revoked": self.revoked,
            "revokedAt": int(self.revoked_at) if self.revoked_at is not None else None,
            "boundEndpoint": self.bound_endpoint,
            "lastUsedAt": int(self.last_used_at) if self.last_used_at is not None else None,
        }


class KeyStore:
    """Thread-safe store for NSM server keys and account sessions."""

    def __init__(self, clock: Callable[[], float] = time.time):
        self._lock = threading.Lock()
        self._clock = clock
        self._keys: Dict[str, ServerKeyRecord] = {}
        self._account_tokens: Dict[str, str] = {}
        self._listing_tokens: Dict[str, Tuple[str, float]] = {}

    def register_account_session(self, account_id: str, session_token: str) -> None:
        if not account_id or not session_token:
            raise ValueError("account_id and session_token are required")
        with self._lock:
            self._account_tokens[account_id] = session_token

    def verify_account_session(self, account_id: str, session_token: str) -> bool:
        if not account_id or not session_token:
            return False
        with self._lock:
            expected = self._account_tokens.get(account_id)
            if not expected:
                return False
            return hmac.compare_digest(expected, session_token)

    def issue_key(
        self,
        owner_id: str,
        label: str = "NSM Dedicated Server",
        ttl_seconds: float = DEFAULT_KEY_TTL_SECONDS,
        bound_endpoint: Optional[str] = None,
    ) -> Tuple[str, ServerKeyRecord]:
        """Issue a new key for owner_id. Returns (plaintext_key_once, record)."""
        if not owner_id or not isinstance(owner_id, str):
            raise ValueError("owner_id is required")
        if not isinstance(label, str) or not label.strip() or len(label) > 128:
            raise ValueError("label must be a non-empty string <= 128 chars")
        now = self._clock()
        key_id = secrets.token_hex(KEY_ID_HEX_LEN // 2)
        secret = secrets.token_urlsafe(32)
        salt = os.urandom(16)
        digest = _hash_secret(secret, salt)
        record = ServerKeyRecord(
            key_id=key_id,
            owner_id=owner_id,
            label=label.strip(),
            salt_b64=base64.b64encode(salt).decode("ascii"),
            secret_hash_b64=base64.b64encode(digest).decode("ascii"),
            created_at=now,
            expires_at=now + float(ttl_seconds),
            revoked=False,
            bound_endpoint=bound_endpoint,
        )
        with self._lock:
            self._keys[key_id] = record
        full_key = f"{KEY_PREFIX}{key_id}.{secret}"
        return full_key, record

    def validate_key(
        self, raw_key: Optional[str], endpoint: Optional[str] = None
    ) -> Tuple[bool, int, str, Optional[ServerKeyRecord]]:
        """Validate a server key fail-closed.

        Returns (is_valid, http_status, error_code, record_or_none).
        """
        if not raw_key:
            return False, 401, "missing_key", None
        parsed = parse_server_key(raw_key)
        if parsed is None:
            return False, 401, "malformed_key", None
        key_id, secret = parsed

        now = self._clock()
        with self._lock:
            rec = self._keys.get(key_id)
            if rec is None:
                return False, 403, "invalid_key", None

            salt = base64.b64decode(rec.salt_b64)
            expected_hash = base64.b64decode(rec.secret_hash_b64)
            candidate_hash = _hash_secret(secret, salt)
            if not hmac.compare_digest(expected_hash, candidate_hash):
                return False, 403, "invalid_key", None

            if rec.revoked:
                return False, 403, "revoked_key", rec

            if now >= rec.expires_at:
                return False, 403, "expired_key", rec

            if rec.bound_endpoint and endpoint and rec.bound_endpoint != endpoint:
                return False, 403, "endpoint_mismatch", rec

            rec.last_used_at = now
            return True, 200, "", rec

    def revoke_key(self, actor_account_id: str, key_id: str) -> Tuple[bool, int, str]:
        """Revoke a key owned by actor_account_id. Enforces anti-IDOR ownership."""
        now = self._clock()
        with self._lock:
            rec = self._keys.get(key_id)
            if rec is None:
                return False, 404, "key_not_found"
            if rec.owner_id != actor_account_id:
                return False, 403, "forbidden_not_key_owner"
            rec.revoked = True
            rec.revoked_at = now
            # Invalidate any listing tokens issued for this key_id
            to_remove = [
                tok for tok, (kid, _) in self._listing_tokens.items() if kid == key_id
            ]
            for tok in to_remove:
                del self._listing_tokens[tok]
            return True, 200, ""

    def list_keys_for_owner(self, actor_account_id: str) -> List[dict]:
        with self._lock:
            return [
                rec.public_metadata()
                for rec in self._keys.values()
                if rec.owner_id == actor_account_id
            ]

    def issue_listing_token(self, key_id: str, ttl_seconds: float = DEFAULT_TTL_SECONDS) -> str:
        token = "nsm_lt_" + secrets.token_urlsafe(24)
        now = self._clock()
        with self._lock:
            self._listing_tokens[token] = (key_id, now + ttl_seconds)
        return token

    def is_key_active(self, key_id: str) -> bool:
        now = self._clock()
        with self._lock:
            rec = self._keys.get(key_id)
            if rec is None or rec.revoked or now >= rec.expires_at:
                return False
            return True


# ---------------------------------------------------------------------------
# Protobuf wire framing (compatible with NSM client masterlist consumer)
# ---------------------------------------------------------------------------

def _varint(value: int) -> bytes:
    if value < -(1 << 63) or value >= (1 << 64):
        raise ValueError("integer out of 64-bit protobuf bounds")
    if value < 0:
        value += 1 << 64
    out = bytearray()
    while value > 0x7F:
        out.append((value & 0x7F) | 0x80)
        value >>= 7
    out.append(value & 0x7F)
    return bytes(out)


def _tag(field_no: int, wire_type: int) -> bytes:
    return _varint((field_no << 3) | wire_type)


def _f_varint(field_no: int, value: int) -> bytes:
    if not value:
        return b""
    return _tag(field_no, 0) + _varint(int(value))


def _f_bool(field_no: int, value: bool) -> bytes:
    return _tag(field_no, 0) + (b"\x01" if value else b"\x00") if value else b""


def _f_bytes(field_no: int, raw: bytes) -> bytes:
    if not raw:
        return b""
    return _tag(field_no, 2) + _varint(len(raw)) + raw


def _f_str(field_no: int, text: str) -> bytes:
    if not text:
        return b""
    return _f_bytes(field_no, str(text).encode("utf-8", "replace"))


def _f_map_ss(field_no: int, mapping: Dict[str, str]) -> bytes:
    out = bytearray()
    for k, v in (mapping or {}).items():
        entry = _f_str(1, str(k)) + _f_str(2, str(v))
        out += _f_bytes(field_no, entry)
    return bytes(out)


def encode_server_data(d: dict) -> bytes:
    msg = bytearray()
    msg += _f_varint(1, d.get("svMaxclients", 0))
    msg += _f_varint(2, d.get("clients", 0))
    msg += _f_varint(3, d.get("protocol", 0))
    msg += _f_str(4, d.get("hostname", ""))
    msg += _f_str(5, d.get("gametype", ""))
    msg += _f_str(6, d.get("mapname", ""))
    for r in d.get("resources") or ():
        msg += _f_str(8, r)
    msg += _f_str(9, d.get("server", ""))
    msg += _f_map_ss(10, d.get("vars") or {})
    msg += _f_varint(16, d.get("iconVersion", 0))
    msg += _f_bool(17, d.get("enhancedHostSupport", False))
    msg += _f_varint(18, d.get("upvotePower", 0))
    for ep in d.get("connectEndPoints") or ():
        msg += _f_str(19, ep)
    msg += _f_varint(20, d.get("burstPower", 0))
    return bytes(msg)


def encode_server_frame(end_point: str, data_dict: dict) -> bytes:
    frame = _f_str(1, end_point) + _f_bytes(2, encode_server_data(data_dict))
    return struct.pack("<I", len(frame)) + frame


def stream_bytes(entries: List[Tuple[str, dict]]) -> bytes:
    return b"".join(encode_server_frame(ep, d) for ep, d in entries)


# ---------------------------------------------------------------------------
# Strict heartbeat validation (matches v0.7.7 local_api.go / masterlist.py)
# ---------------------------------------------------------------------------

def _require_int_in_range(val, low: int, high: int, name: str) -> int:
    if isinstance(val, bool) or not isinstance(val, int):
        raise ValueError(f"{name} must be an integer")
    if val < low or val > high:
        raise ValueError(f"{name} out of range [{low}, {high}]")
    return val


def validate_and_extract_heartbeat(hb: object) -> Tuple[int, Optional[str], dict]:
    """Validate heartbeat JSON payload and return (port, ip_override, server_data)."""
    if not isinstance(hb, dict):
        raise ValueError("heartbeat payload must be a JSON object")

    port_raw = hb.get("port", 30120)
    port = _require_int_in_range(port_raw, 1, 65535, "port")

    ip_override = hb.get("ipOverride")
    if ip_override is not None:
        if not isinstance(ip_override, str):
            raise ValueError("ipOverride must be a string")
        ip_override = ip_override.strip()
        if ip_override:
            try:
                ipaddress.ip_address(ip_override)
            except ValueError as exc:
                raise ValueError("ipOverride is not a valid IP address") from exc

    fb = hb.get("fallbackData")
    if fb is None:
        fb = {}
    if not isinstance(fb, dict):
        raise ValueError("fallbackData must be an object")

    info = fb.get("info", {})
    if not isinstance(info, dict):
        raise ValueError("fallbackData.info must be an object")
    if "info" in info and isinstance(info["info"], dict):
        info = info["info"]

    dynamic = fb.get("dynamic", {})
    if not isinstance(dynamic, dict):
        raise ValueError("fallbackData.dynamic must be an object")
    if "dynamic" in dynamic and isinstance(dynamic["dynamic"], dict):
        dynamic = dynamic["dynamic"]

    players = fb.get("players", [])
    if not isinstance(players, list):
        raise ValueError("fallbackData.players must be a list")

    resources = info.get("resources", [])
    if not isinstance(resources, list) or not all(isinstance(r, str) for r in resources):
        raise ValueError("resources must be a list of strings")

    hostname = dynamic.get("hostname", info.get("hostname", "NSM Server"))
    if not isinstance(hostname, str):
        raise ValueError("hostname must be a string")

    gametype = dynamic.get("gametype", "")
    if not isinstance(gametype, str):
        raise ValueError("gametype must be a string")

    mapname = dynamic.get("mapname", "")
    if not isinstance(mapname, str):
        raise ValueError("mapname must be a string")

    clients_raw = dynamic.get("clients", len(players))
    clients = _require_int_in_range(clients_raw, 0, 4096, "clients")

    max_raw = dynamic.get("svMaxclients", hb.get("maxClients", 48))
    if isinstance(max_raw, str) and max_raw.isdigit():
        max_raw = int(max_raw)
    sv_maxclients = _require_int_in_range(max_raw, 1, 4096, "svMaxclients")

    vars_dict: Dict[str, str] = {}
    for src in (info.get("vars") or {}, dynamic.get("vars") or {}):
        if not isinstance(src, dict):
            raise ValueError("vars must be an object")
        for k, v in src.items():
            # Never allow internal key material to leak via server vars
            if str(k).lower() in ("sv_licensekey", "nsm_server_key", "authorization"):
                continue
            vars_dict[str(k)] = redact_secrets(str(v))
    vars_dict["gamename"] = GAMENAME

    server_data = {
        "svMaxclients": sv_maxclients,
        "clients": clients,
        "hostname": redact_secrets(hostname) or "NSM Server",
        "gametype": redact_secrets(gametype),
        "mapname": redact_secrets(mapname),
        "resources": resources,
        "server": redact_secrets(str(info.get("version") or "")),
        "vars": vars_dict,
        "enhancedHostSupport": bool(info.get("enhancedHostSupport")),
        "connectEndPoints": [],
        "upvotePower": 0,
        "burstPower": 0,
    }
    return port, ip_override, server_data


# ---------------------------------------------------------------------------
# Authenticated Server Registry (with ownership & revocation enforcement)
# ---------------------------------------------------------------------------

@dataclass
class ListedServerEntry:
    end_point: str
    owner_id: str
    key_id: Optional[str]
    updated_at: float
    data: dict = field(default_factory=dict)


class AuthenticatedRegistry:
    """In-memory server registry that enforces key validity and owner isolation."""

    def __init__(
        self,
        key_store: KeyStore,
        ttl_seconds: float = DEFAULT_TTL_SECONDS,
        clock: Callable[[], float] = time.time,
    ):
        self._lock = threading.Lock()
        self._key_store = key_store
        self._ttl = ttl_seconds
        self._clock = clock
        self._servers: Dict[str, ListedServerEntry] = {}

    def _prune_locked(self, now: float) -> None:
        expired = [
            ep
            for ep, entry in self._servers.items()
            if (now - entry.updated_at > self._ttl)
            or (entry.key_id is not None and not self._key_store.is_key_active(entry.key_id))
        ]
        for ep in expired:
            del self._servers[ep]

    def upsert_authorized(
        self,
        end_point: str,
        owner_id: str,
        key_id: Optional[str],
        data: dict,
    ) -> Tuple[bool, int, str]:
        """Upsert a server listing, rejecting cross-account endpoint hijacks."""
        now = self._clock()
        with self._lock:
            self._prune_locked(now)
            existing = self._servers.get(end_point)
            if existing is not None and existing.owner_id != owner_id:
                return False, 403, "endpoint_owned_by_another_account"
            clean_data = dict(data)
            clean_data["connectEndPoints"] = [end_point]
            self._servers[end_point] = ListedServerEntry(
                end_point=end_point,
                owner_id=owner_id,
                key_id=key_id,
                updated_at=now,
                data=clean_data,
            )
            return True, 200, ""

    def evict_by_key(self, key_id: str) -> int:
        with self._lock:
            to_delete = [
                ep for ep, entry in self._servers.items() if entry.key_id == key_id
            ]
            for ep in to_delete:
                del self._servers[ep]
            return len(to_delete)

    def get(self, end_point: str) -> Optional[Tuple[str, dict]]:
        now = self._clock()
        with self._lock:
            self._prune_locked(now)
            entry = self._servers.get(end_point)
            if entry is None:
                return None
            return entry.owner_id, dict(entry.data)

    def entries(self) -> List[Tuple[str, dict]]:
        now = self._clock()
        with self._lock:
            self._prune_locked(now)
            return [(ep, dict(e.data)) for ep, e in sorted(self._servers.items())]


# ---------------------------------------------------------------------------
# HTTP Server Factory (supports "public" fail-closed and "localtest" modes)
# ---------------------------------------------------------------------------

def _is_loopback(addr: str) -> bool:
    try:
        return ipaddress.ip_address(addr).is_loopback
    except ValueError:
        return addr in ("localhost", "127.0.0.1", "::1")


def make_handler(
    key_store: KeyStore,
    registry: AuthenticatedRegistry,
    mode: str = "public",
):
    if mode not in ("public", "localtest"):
        raise ValueError("mode must be 'public' or 'localtest'")

    class NSMKeyServiceHandler(BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"
        server_version = "NSM-KeyService/0.8-candidate"

        def log_message(self, fmt: str, *args) -> None:
            # Never log raw headers or unredacted parameters
            safe_msg = redact_secrets(fmt % args if args else fmt)
            # Quiet during unit tests unless NSM_KEYSERVICE_VERBOSE=1
            if os.environ.get("NSM_KEYSERVICE_VERBOSE") == "1":
                print(f"[nsm-keyservice] {safe_msg}", flush=True)

        def _send(self, code: int, body: bytes | str, ctype: str = "application/json") -> None:
            if isinstance(body, str):
                body = body.encode("utf-8")
            self.send_response(code)
            self.send_header("Content-Type", ctype)
            self.send_header("Content-Length", str(len(body)))
            self.send_header("Access-Control-Allow-Origin", "*")
            self.send_header("Access-Control-Allow-Methods", "GET, POST, OPTIONS, HEAD")
            self.send_header("Access-Control-Allow-Headers", "*")
            self.send_header("X-NSM-Mode", mode)
            self.end_headers()
            if self.command != "HEAD":
                self.wfile.write(body)

        def do_OPTIONS(self) -> None:
            self._send(204, b"")

        def do_HEAD(self) -> None:
            self.do_GET()

        def _json(self, obj: dict | list, code: int = 200) -> None:
            self._send(code, json.dumps(obj), "application/json")

        def _read_json_body(self) -> Tuple[Optional[object], Optional[Tuple[int, str]]]:
            if self.headers.get("Transfer-Encoding", "").lower() == "chunked":
                return None, (400, "chunked transfer-encoding is not supported")
            raw_len = self.headers.get("Content-Length")
            if raw_len is None:
                return None, (400, "missing Content-Length")
            try:
                length = int(raw_len)
            except ValueError:
                return None, (400, "invalid Content-Length")
            if length < 0:
                return None, (400, "negative Content-Length")
            if length > MAX_HEARTBEAT_BYTES:
                return None, (413, "payload too large")
            raw = self.rfile.read(length) if length > 0 else b"{}"
            try:
                return json.loads(raw.decode("utf-8")), None
            except Exception:
                return None, (400, "malformed JSON")

        def _require_account_auth(self) -> Optional[str]:
            account_id = (self.headers.get("X-NSM-Account-Id") or "").strip()
            token = (self.headers.get("X-NSM-Account-Token") or "").strip()
            if not key_store.verify_account_session(account_id, token):
                self._json({"error": "unauthorized_account"}, 401)
                return None
            return account_id

        def do_GET(self) -> None:
            path = self.path.split("?", 1)[0]

            # Explicit guardrail: never impersonate Rockstar/GTA entitlement or Cfx portal
            if path.startswith("/api/validate/entitlement") or path.startswith("/api/register"):
                self._json(
                    {
                        "error": "out_of_scope_upstream_only",
                        "detail": (
                            "Rockstar/GTA entitlement and Cfx services are never "
                            "impersonated or bypassed by NSM KeyService."
                        ),
                    },
                    404,
                )
                return

            if path in ("/", "/health"):
                self._json(
                    {
                        "service": "nsm-keyservice",
                        "mode": mode,
                        "authentication_bypassed": False,
                    },
                    200,
                )
                return

            if path == "/api/nsm/keys":
                account_id = self._require_account_auth()
                if not account_id:
                    return
                self._json({"keys": key_store.list_keys_for_owner(account_id)}, 200)
                return

            if path == "/api/servers/streamRedir/":
                entries = registry.entries()
                self._send(200, stream_bytes(entries), "application/octet-stream")
                return

            if path.startswith("/api/servers/single/"):
                ep = path[len("/api/servers/single/"):]
                found = registry.get(ep)
                if not found:
                    self._json({}, 404)
                    return
                owner_id, data = found
                self._json({"EndPoint": ep, "OwnerId": owner_id, "Data": data}, 200)
                return

            if path == "/runtime/counts.json":
                entries = registry.entries()
                players = sum(int(d.get("clients", 0)) for _, d in entries)
                self._json([players, len(entries), players], 200)
                return

            if path == "/api/ticket/pubkey":
                # Deliberately do not serve the committed repo key; require explicit config
                self._json(
                    {
                        "error": "ticket_pubkey_not_configured",
                        "detail": "Configure a rotated NSM ticket public key; repo sample key is not used.",
                    },
                    503,
                )
                return

            if path in ("/api/identities", "/api/ticket/identities", "/v1/public/featured-servers"):
                self._json([], 200)
                return

            if path == "/v1/public/notices/fivem":
                self._json({"notices": []}, 200)
                return

            if path == "/v1/public/tweets":
                self._json({"tweets": []}, 200)
                return

            if path.startswith("/v1/public/pool-size-limits/"):
                self._json({}, 200)
                return

            if path.startswith("/api/servers/icon/"):
                self._send(404, b"", "image/png")
                return

            self._json({"error": "not_found"}, 404)

        def do_POST(self) -> None:
            path = self.path.split("?", 1)[0]

            if path.startswith("/api/validate/entitlement") or path.startswith("/api/register"):
                self._json(
                    {
                        "error": "out_of_scope_upstream_only",
                        "detail": "Upstream entitlement/registration is never impersonated.",
                    },
                    404,
                )
                return

            if path == "/api/nsm/keys/issue":
                account_id = self._require_account_auth()
                if not account_id:
                    return
                payload, err = self._read_json_body()
                if err:
                    self._json({"error": err[1]}, err[0])
                    return
                if not isinstance(payload, dict):
                    self._json({"error": "body must be a JSON object"}, 400)
                    return
                label = payload.get("label", "NSM Dedicated Server")
                ttl = payload.get("ttlSeconds", DEFAULT_KEY_TTL_SECONDS)
                bound_ep = payload.get("boundEndpoint")
                try:
                    full_key, record = key_store.issue_key(
                        owner_id=account_id,
                        label=label,
                        ttl_seconds=float(ttl),
                        bound_endpoint=bound_ep,
                    )
                except ValueError as exc:
                    self._json({"error": str(exc)}, 400)
                    return
                self._json(
                    {
                        "serverKey": full_key,  # shown ONCE at creation
                        "key": record.public_metadata(),
                    },
                    201,
                )
                return

            if path == "/api/nsm/keys/revoke":
                account_id = self._require_account_auth()
                if not account_id:
                    return
                payload, err = self._read_json_body()
                if err:
                    self._json({"error": err[1]}, err[0])
                    return
                if not isinstance(payload, dict) or not isinstance(payload.get("keyId"), str):
                    self._json({"error": "keyId is required"}, 400)
                    return
                key_id = payload["keyId"].strip()
                ok, code, reason = key_store.revoke_key(account_id, key_id)
                if not ok:
                    self._json({"error": reason}, code)
                    return
                evicted = registry.evict_by_key(key_id)
                self._json({"revoked": True, "keyId": key_id, "evictedListings": evicted}, 200)
                return

            if path == "/api/nsm/servers/register":
                payload, err = self._read_json_body()
                if err:
                    self._json({"lastError": err[1]}, err[0])
                    return
                if not isinstance(payload, dict):
                    self._json({"lastError": "body must be a JSON object"}, 400)
                    return
                raw_key = extract_bearer_or_header_key(dict(self.headers))
                ok, code, reason, rec = key_store.validate_key(raw_key)
                if not ok or rec is None:
                    self._json({"lastError": reason}, code)
                    return
                listing_token = key_store.issue_listing_token(rec.key_id)
                self._json(
                    {
                        "listingToken": listing_token,
                        "keyId": rec.key_id,
                        "ownerId": rec.owner_id,
                    },
                    200,
                )
                return

            if path == "/api/serverlist/ingress":
                payload, err = self._read_json_body()
                if err:
                    self._json({"lastError": err[1]}, err[0])
                    return
                try:
                    port, ip_override, server_data = validate_and_extract_heartbeat(payload)
                except ValueError as exc:
                    self._json({"lastError": redact_secrets(str(exc))}, 400)
                    return

                client_ip = self.client_address[0]
                if ip_override:
                    # Only loopback callers may override IP to loopback
                    if not (_is_loopback(client_ip) and _is_loopback(ip_override)):
                        self._json({"lastError": "ipOverride only allowed for loopback"}, 400)
                        return
                    effective_ip = ip_override
                else:
                    effective_ip = client_ip

                end_point = f"{effective_ip}:{port}"
                raw_key = extract_bearer_or_header_key(dict(self.headers))

                if raw_key is None and mode == "localtest" and _is_loopback(client_ip):
                    owner_id = "localtest-unauthenticated"
                    key_id = None
                    server_data["vars"]["nsm_listing_mode"] = "localtest"
                else:
                    ok, code, reason, rec = key_store.validate_key(raw_key, endpoint=end_point)
                    if not ok or rec is None:
                        self._json({"lastError": reason}, code)
                        return
                    owner_id = rec.owner_id
                    key_id = rec.key_id
                    server_data["vars"]["nsm_listing_mode"] = "authenticated"

                ok, code, reason = registry.upsert_authorized(
                    end_point=end_point,
                    owner_id=owner_id,
                    key_id=key_id,
                    data=server_data,
                )
                if not ok:
                    self._json({"lastError": reason}, code)
                    return

                self._json({"status": "ok", "endPoint": end_point, "mode": mode}, 200)
                return

            self._json({"error": "not_found"}, 404)

    return NSMKeyServiceHandler


def main() -> None:
    import argparse

    parser = argparse.ArgumentParser(description="NSM Server Key & Masterlist Service")
    parser.add_argument("--bind", default="127.0.0.1", help="Bind address (default: 127.0.0.1)")
    parser.add_argument("--port", type=int, default=8080, help="Listen port (default: 8080)")
    parser.add_argument(
        "--mode",
        choices=("public", "localtest"),
        default="public",
        help="Operating mode: 'public' (fail-closed key required) or 'localtest'",
    )
    args = parser.parse_args()

    store = KeyStore()
    registry = AuthenticatedRegistry(store)
    handler_cls = make_handler(store, registry, mode=args.mode)
    srv = ThreadingHTTPServer((args.bind, args.port), handler_cls)
    srv.daemon_threads = True
    print(
        f"NSM KeyService ({args.mode} mode) listening on http://{args.bind}:{args.port}",
        flush=True,
    )
    try:
        srv.serve_forever()
    except KeyboardInterrupt:
        pass


if __name__ == "__main__":
    main()

