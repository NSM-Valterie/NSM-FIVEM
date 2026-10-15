#!/usr/bin/env python3
"""
NSM masterlist service
======================
Drop-in replacement for the Cfx masterlist endpoints used by NSM clients
and servers. Single file, Python 3.8+ standard library only.

Serves (all on one port, default 80):

  Server browser (UI bundle, after patch_ui_urls.ps1):
    GET  /api/servers/streamRedir/            full list as a stream of
                                              [4-byte LE length][protobuf master.Server]
    GET  /api/servers/single/<ip:port>        single server as JSON {EndPoint, Data}
    GET  /api/servers/icon/<ep>/<v>.png       server icon (404 -> default icon)
    GET  /api/identities                      player identities (empty)
    GET  /api/ticket/identities               ticket identities (empty)
    GET  /v1/public/featured-servers          featured servers (empty)
    GET  /v1/public/notices/fivem             notices (empty)
    GET  /v1/public/tweets                    tweets (empty)
    GET  /runtime/counts.json                 player counts [0, 0, 0]

  Server side:
    POST /api/serverlist/ingress              server heartbeat (GameServer.cpp)
    GET  /api/ticket/pubkey                   RSA public key (unused with sv_lan true)
    GET  /v1/public/pool-size-limits/fivem    pool size limits (empty = unlimited)

Entries expire 5 minutes after their last heartbeat.
vars.gamename is forced to "gta5" so the client browser filter (init(["gta5"]))
shows the servers.

Run:  sudo python3 masterlist.py            (port 80)
      python3 masterlist.py --port 8080
"""

import argparse
import base64
import json
import socket
import struct
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

HEARTBEAT_TTL = 300.0
GAMENAME = "gta5"

# ---------------------------------------------------------------------------
# protobuf encoding (schema extracted from the cfx-ui worker bundle:
# master.Server { string EndPoint = 1; master.ServerData Data = 2; }
# master.ServerData {
#   int32 svMaxclients = 1; int32 clients = 2; int32 protocol = 3;
#   string hostname = 4; string gametype = 5; string mapname = 6;
#   repeated string resources = 8; string server = 9;
#   map<string,string> vars = 12; bool enhancedHostSupport = 16;
#   int32 upvotePower = 17; repeated string connectEndPoints = 18;
#   int32 burstPower = 19; })
# ---------------------------------------------------------------------------

def _varint(n):
    out = bytearray()
    while True:
        b = n & 0x7F
        n >>= 7
        out.append(b | (0x80 if n else 0))
        if not n:
            return bytes(out)


def _tag(field, wire):
    return _varint((field << 3) | wire)


def _pb_str(field, s):
    if s is None:
        return b""
    b = s.encode("utf-8") if isinstance(s, str) else bytes(s)
    return _tag(field, 2) + _varint(len(b)) + b


def _pb_int(field, v):
    if v is None:
        return b""
    return _tag(field, 0) + _varint(int(v))


def _pb_msg(field, payload):
    return _tag(field, 2) + _varint(len(payload)) + payload


def encode_server_data(d):
    out = b""
    out += _pb_int(1, d.get("svMaxclients"))
    out += _pb_int(2, d.get("clients"))
    out += _pb_int(3, d.get("protocol"))
    out += _pb_str(4, d.get("hostname", ""))
    out += _pb_str(5, d.get("gametype", ""))
    out += _pb_str(6, d.get("mapname", ""))
    for r in d.get("resources") or []:
        out += _pb_str(8, r)
    out += _pb_str(9, d.get("server", ""))
    for k, v in (d.get("vars") or {}).items():
        out += _pb_msg(12, _pb_str(1, k) + _pb_str(2, "" if v is None else str(v)))
    if d.get("enhancedHostSupport"):
        out += _tag(16, 0) + b"\x01"
    out += _pb_int(17, d.get("upvotePower", 0))
    for c in d.get("connectEndPoints") or []:
        out += _pb_str(18, c)
    out += _pb_int(19, d.get("burstPower", 0))
    return out


def encode_server(end_point, data):
    return _pb_str(1, end_point) + _pb_msg(2, encode_server_data(data))


def stream_bytes(entries):
    out = bytearray()
    for ep, data in entries:
        payload = encode_server(ep, data)
        out += struct.pack("<I", len(payload)) + payload
    return bytes(out)


# ---------------------------------------------------------------------------
# registry
# ---------------------------------------------------------------------------

class Registry:
    def __init__(self):
        self._lock = threading.Lock()
        self._servers = {}  # EndPoint -> {"data": {...}, "last": ts}

    def upsert(self, end_point, data):
        with self._lock:
            self._servers[end_point] = {"data": data, "last": time.time()}

    def entries(self):
        now = time.time()
        with self._lock:
            dead = [ep for ep, s in self._servers.items() if now - s["last"] > HEARTBEAT_TTL]
            for ep in dead:
                del self._servers[ep]
            return [(ep, s["data"]) for ep, s in sorted(self._servers.items())]

    def get(self, end_point):
        now = time.time()
        with self._lock:
            s = self._servers.get(end_point)
            if not s or now - s["last"] > HEARTBEAT_TTL:
                return None
            return s["data"]


REGISTRY = Registry()


# ---------------------------------------------------------------------------
# heartbeat -> ServerData mapping
# ---------------------------------------------------------------------------

def _num(x, default=0):
    try:
        return int(x)
    except (TypeError, ValueError):
        return default


def server_data_from_heartbeat(hb):
    fb = hb.get("fallbackData") or {}
    info = fb.get("info") or {}
    dynamic = fb.get("dynamic") or {}
    info = info.get("info", info) if isinstance(info.get("info", None), dict) else info
    dynamic = dynamic.get("dynamic", dynamic) if isinstance(dynamic.get("dynamic", None), dict) else dynamic

    # vars live in both info.vars and dynamic.vars; merge them
    vars_ = {}
    vars_.update(info.get("vars") or {})
    vars_.update(dynamic.get("vars") or {})
    vars_["gamename"] = GAMENAME  # client browser filter requires this

    return {
        "svMaxclients": _num(dynamic.get("svMaxclients", hb.get("maxClients")), 48),
        "clients": _num(dynamic.get("clients", len(fb.get("players") or [])), 0),
        "hostname": dynamic.get("hostname") or info.get("hostname") or "NSM Server",
        "gametype": dynamic.get("gametype") or "",
        "mapname": dynamic.get("mapname") or "",
        "resources": info.get("resources") or [],
        "server": str(info.get("version") or ""),
        "vars": vars_,
        "enhancedHostSupport": bool(info.get("enhancedHostSupport")),
        "connectEndPoints": [],
        "upvotePower": 0,
        "burstPower": 0,
    }


# ---------------------------------------------------------------------------
# HTTP
# ---------------------------------------------------------------------------

# Public key served at /api/ticket/pubkey (PKCS#1 RSAPublicKey, base64 DER).
# Matches the NSM ticket-signing key (tools/masterlist/ticket-signing-key.pem).
# Only used when a server runs with sv_lan false; NSM servers should set
# `sv_lan true` so connect tickets are not required at all.
TICKET_PUBKEY_B64 = (
    "MIIBCgKCAQEAwleR7LYjK6zv5GPU4+go+4u42JI8fA/hX4wykkwhxG8fLpdGTA1kjQm+NWrWi7Xg"
    "OMOAVzeiMCpr5cLlcTabf1fJpexyN1XKmQLvnIxVgofc/N/ZvzVbnqTV5RGJ7ZfnXDd//r+bBuxg"
    "DWJIEAiXnYAwTSZq5crxgXorya7gVTcD2P/RQQUCCzuniFaXO2mROxj28UDSihVCKriTd1p6StxX"
    "SnsnekH6av5uuCENf1KHL85n7bl4gKZQoG0qoYQxohigjfUXvvO93AbN7M10r12rvgLcLTCHhMci"
    "y0xEmbj2pI5b9wOjPbx/oHqn5rtAhqYae4803I80aZRweafWzQIDAQAB"
)


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    server_version = "NSM-Masterlist/1.0"

    # -- helpers ------------------------------------------------------------
    def _cors(self):
        self.send_header("Access-Control-Allow-Origin", "*")
        self.send_header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
        self.send_header("Access-Control-Allow-Headers", "*")

    def _send(self, code, body, ctype="application/json"):
        if isinstance(body, str):
            body = body.encode("utf-8")
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(body)))
        self._cors()
        self.end_headers()
        if self.command != "HEAD":
            self.wfile.write(body)

    def _json(self, obj, code=200):
        self._send(code, json.dumps(obj), "application/json")

    def _client_ip(self):
        ff = self.headers.get("X-Forwarded-For")
        if ff:
            return ff.split(",")[0].strip()
        return self.client_address[0]

    def log_message(self, fmt, *args):
        print("[%s] %s %s" % (time.strftime("%Y-%m-%d %H:%M:%S"),
                              self.command, fmt % args), flush=True)

    # -- routing ------------------------------------------------------------
    def do_OPTIONS(self):
        self._send(204, b"")

    def do_HEAD(self):
        self.do_GET()

    def do_GET(self):
        path = self.path.split("?", 1)[0].rstrip("?")
        # strip duplicate trailing slashes but keep the streamRedir/ form
        if path.endswith("/") and not path.endswith("streamRedir/"):
            path = path.rstrip("/") or "/"

        if path in ("/", "/health"):
            self._send(200, "NSM masterlist service\n", "text/plain")

        elif path == "/api/servers/streamRedir/":
            entries = REGISTRY.entries()
            # fill connectEndPoints now that we know the EndPoints
            fixed = [(ep, dict(d, connectEndPoints=[ep])) for ep, d in entries]
            self._send(200, stream_bytes(fixed), "application/octet-stream")

        elif path.startswith("/api/servers/single/"):
            ep = path[len("/api/servers/single/"):]
            data = REGISTRY.get(ep)
            if data:
                data = dict(data, connectEndPoints=[ep])
                self._json({"EndPoint": ep, "Data": data})
            else:
                self._json({}, 404)

        elif path.startswith("/api/servers/icon/"):
            self._send(404, b"", "image/png")

        elif path == "/api/ticket/pubkey":
            self._json({"key": TICKET_PUBKEY_B64})

        elif path == "/api/identities" or path == "/api/ticket/identities":
            self._json([])

        elif path == "/v1/public/featured-servers":
            self._json([])

        elif path == "/v1/public/notices/fivem":
            self._json({"notices": []})

        elif path == "/v1/public/tweets":
            self._json({"tweets": []})

        elif path.startswith("/v1/public/pool-size-limits/"):
            self._json({})

        elif path == "/runtime/counts.json":
            entries = REGISTRY.entries()
            players = sum(_num(d.get("clients")) for _, d in entries)
            self._json([players, 0, players])

        else:
            self._json({"error": "not found", "path": path}, 404)

    def do_POST(self):
        path = self.path.split("?", 1)[0]

        if path == "/api/serverlist/ingress":
            try:
                length = int(self.headers.get("Content-Length") or 0)
                hb = json.loads(self.rfile.read(length) or b"{}")
            except Exception as e:
                self._json({"lastError": "bad json: %s" % e}, 400)
                return

            ip_override = hb.get("ipOverride")
            ip = (ip_override or "").strip() or self._client_ip()
            port = _num(hb.get("port"), 30120)
            end_point = "%s:%d" % (ip, port)
            REGISTRY.upsert(end_point, server_data_from_heartbeat(hb))
            print("[heartbeat] %s clients=%s" % (
                end_point, (hb.get("fallbackData") or {}).get("dynamic", {}).get("clients", "?")),
                flush=True)
            self._json({})  # empty lastError = success
        else:
            self._json({"error": "not found", "path": path}, 404)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--port", type=int, default=80)
    ap.add_argument("--bind", default="0.0.0.0")
    args = ap.parse_args()

    srv = ThreadingHTTPServer((args.bind, args.port), Handler)
    srv.daemon_threads = True
    print("NSM masterlist listening on %s:%d" % (args.bind, args.port), flush=True)
    try:
        srv.serve_forever()
    except KeyboardInterrupt:
        pass


if __name__ == "__main__":
    main()
