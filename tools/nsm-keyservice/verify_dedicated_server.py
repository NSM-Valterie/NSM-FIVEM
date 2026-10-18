#!/usr/bin/env python3
"""End-to-End Dedicated Server & Two-Player Acceptance Verifier.

Verifies:
1. Dedicated server direct HTTP endpoints (/info.json, /dynamic.json, /players.json)
2. Masterlist single-server lookup (/api/servers/single/<endpoint>)
3. Masterlist framed protobuf stream (/api/servers/streamRedir/)
4. Masterlist aggregate player counts (/runtime/counts.json)
5. Active player count >= --min-players (default 2 for two-player acceptance)

Usage on Windows / LAN test host:
  python verify_dedicated_server.py --server-endpoint 127.0.0.1:30120 --masterlist-url http://127.0.0.1:80 --min-players 2
"""

from __future__ import annotations

import argparse
import json
import sys
import urllib.error
import urllib.request
from typing import Tuple


def _http_get(url: str, timeout: float = 4.0) -> Tuple[int, bytes]:
    req = urllib.request.Request(url, method="GET")
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return resp.status, resp.read()
    except urllib.error.HTTPError as exc:
        return exc.code, exc.read()


def verify_acceptance(
    server_endpoint: str,
    masterlist_url: str,
    min_players: int = 2,
    check_direct_server_http: bool = True,
) -> dict:
    """Run acceptance checks and return a structured verification report."""
    base_master = masterlist_url.rstrip("/")
    checks = []

    if check_direct_server_http:
        srv_base = f"http://{server_endpoint}"
        for path in ("/info.json", "/dynamic.json", "/players.json"):
            code, raw = _http_get(srv_base + path)
            ok = code == 200
            parsed = json.loads(raw.decode("utf-8")) if ok else None
            if path == "/players.json" and ok:
                ok = isinstance(parsed, list) and len(parsed) >= min_players
            elif path == "/dynamic.json" and ok:
                ok = isinstance(parsed, dict) and int(parsed.get("clients", 0)) >= min_players
            checks.append({"check": f"server:{path}", "status": code, "passed": ok})

    # Masterlist single lookup
    s_code, s_raw = _http_get(f"{base_master}/api/servers/single/{server_endpoint}")
    s_ok = False
    listed_clients = 0
    if s_code == 200:
        s_body = json.loads(s_raw.decode("utf-8"))
        data = s_body.get("Data") or {}
        listed_clients = int(data.get("clients", 0))
        s_ok = (
            s_body.get("EndPoint") == server_endpoint
            and (data.get("vars") or {}).get("gamename") == "gta5"
            and listed_clients >= min_players
        )
    checks.append(
        {
            "check": "masterlist:/api/servers/single",
            "status": s_code,
            "listedClients": listed_clients,
            "passed": s_ok,
        }
    )

    # Masterlist binary protobuf stream
    st_code, st_raw = _http_get(f"{base_master}/api/servers/streamRedir/")
    st_ok = st_code == 200 and server_endpoint.encode("utf-8") in st_raw
    checks.append(
        {
            "check": "masterlist:/api/servers/streamRedir/",
            "status": st_code,
            "bytes": len(st_raw),
            "passed": st_ok,
        }
    )

    # Masterlist aggregate counts
    c_code, c_raw = _http_get(f"{base_master}/runtime/counts.json")
    c_ok = False
    if c_code == 200:
        c_list = json.loads(c_raw.decode("utf-8"))
        c_ok = isinstance(c_list, list) and len(c_list) >= 1 and int(c_list[0]) >= min_players
    checks.append(
        {
            "check": "masterlist:/runtime/counts.json",
            "status": c_code,
            "passed": c_ok,
        }
    )

    all_passed = all(item["passed"] for item in checks)
    return {
        "serverEndpoint": server_endpoint,
        "masterlistUrl": base_master,
        "minPlayersRequired": min_players,
        "allPassed": all_passed,
        "checks": checks,
    }


def main() -> int:
    parser = argparse.ArgumentParser(description="Verify NSM Dedicated Server & 2-Player Session")
    parser.add_argument("--server-endpoint", default="127.0.0.1:30120")
    parser.add_argument("--masterlist-url", default="http://127.0.0.1:80")
    parser.add_argument("--min-players", type=int, default=2)
    parser.add_argument("--skip-direct-server-http", action="store_true")
    args = parser.parse_args()

    report = verify_acceptance(
        server_endpoint=args.server_endpoint,
        masterlist_url=args.masterlist_url,
        min_players=args.min_players,
        check_direct_server_http=not args.skip_direct_server_http,
    )
    print(json.dumps(report, indent=2))
    return 0 if report["allPassed"] else 1


if __name__ == "__main__":
    sys.exit(main())
