# NSM Dedicated Server & Two-Player Acceptance Runbook

This runbook covers the remaining Windows runtime verification item from the
Master Prompt without modifying any locked v0.7.7 file:
> *"Dedicated server and two-player acceptance: launch an NSM server and verify a real two-player session. A working browser does not prove the dedicated-server path."*

---

## 1. Prerequisites (Keep v0.7.7 Untouched)

1. Keep your working `NSM-client-v0.7.7-localtest` folder untouched.
2. Start the masterlist backend (`START-LOCALHOST.bat` for v0.7.7 `localtest` mode, or `python tools/nsm-keyservice/keyservice.py --bind 127.0.0.1 --port 80 --mode public` when testing the v0.7.8 authenticated key flow).
3. In your `nsm-server` configuration (`server.cfg`), include `tools/release-077/server-local.cfg` (or `tools/nsm-keyservice/server-nsm-key.cfg` if using `sv_nsmServerKey`).

---

## 2. Launch Dedicated Server & Connect Two Clients

1. Start `nsm-server.exe +exec server.cfg`.
2. Confirm the server sends its initial heartbeat to `http://127.0.0.1:80/api/serverlist/ingress`.
3. Connect **Player 1** and **Player 2** from two NSM client instances (with legitimate Steam + Rockstar Social Club signed in).
4. Once both players are spawned in-world, run the automated acceptance verifier below.

---

## 3. Run Automated End-to-End Acceptance Verifier

```bash
python tools/nsm-keyservice/verify_dedicated_server.py \
  --server-endpoint 127.0.0.1:30120 \
  --masterlist-url http://127.0.0.1:80 \
  --min-players 2
```

### What `verify_dedicated_server.py` Verifies

| Check | Endpoint | Pass Criterion |
|---|---|---|
| `server:/info.json` | `http://127.0.0.1:30120/info.json` | HTTP `200`, valid JSON server metadata |
| `server:/dynamic.json` | `http://127.0.0.1:30120/dynamic.json` | HTTP `200`, `clients >= 2` |
| `server:/players.json` | `http://127.0.0.1:30120/players.json` | HTTP `200`, player list length `>= 2` |
| `masterlist:/api/servers/single` | `http://127.0.0.1:80/api/servers/single/127.0.0.1:30120` | HTTP `200`, `EndPoint == 127.0.0.1:30120`, `vars.gamename == "gta5"`, `Data.clients >= 2` |
| `masterlist:/api/servers/streamRedir/` | `http://127.0.0.1:80/api/servers/streamRedir/` | HTTP `200`, framed binary protobuf contains `127.0.0.1:30120` |
| `masterlist:/runtime/counts.json` | `http://127.0.0.1:80/runtime/counts.json` | HTTP `200`, total players `>= 2` |

When all 6 checks pass, `verify_dedicated_server.py` outputs `"allPassed": true` and exits with code `0`.
