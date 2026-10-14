# NSM masterlist service

Minimal replacement for the Cfx masterlist/identity endpoints, pointed at by
NSM clients (v3+) and servers (v6+). Single file, Python 3.8+ standard library
only — nothing to install.

Deploy on the VPS (194.104.146.133):

```bash
# as root
mkdir -p /opt/nsm-masterlist
cp masterlist.py /opt/nsm-masterlist/

# --- option A: systemd (recommended) ---
cp nsm-masterlist.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now nsm-masterlist

# --- option B: quick test ---
python3 /opt/nsm-masterlist/masterlist.py --port 8080
```

Check it:

```bash
curl http://127.0.0.1/health
# -> NSM masterlist service

curl -X POST http://127.0.0.1/api/serverlist/ingress \
  -H 'Content-Type: application/json' -d '{"port":30120,"fallbackData":{"dynamic":{"clients":0,"hostname":"test","svMaxclients":48}}}'
curl http://127.0.0.1/api/servers/single/127.0.0.1:30120
```

## Firewall

Open TCP 80 inbound (the client UI, and the servers' heartbeats, both hit it).

## How it fits together

| Endpoint | Consumer | Purpose |
|---|---|---|
| `GET /api/servers/streamRedir/` | client UI (server browser) | full list, 4-byte LE length-prefixed protobuf `master.Server` frames |
| `GET /api/servers/single/<ep>` | client UI | single server JSON for direct connect / favorites |
| `GET /api/servers/icon/...` | client UI | server icons (404 = default icon) |
| `POST /api/serverlist/ingress` | NSM server heartbeat | registers/refreshes a server (5 min TTL) |
| `GET /api/ticket/pubkey` | server (when `sv_lan false`) | RSA pubkey for ticket checks |
| `GET /api/identities`, `/api/ticket/identities` | client UI | linked identities (empty) |
| `GET /v1/public/*`, `/runtime/counts.json` | client UI / server | news, featured, pool limits (empty defaults) |

Servers appear in the browser only while they heartbeat; entries expire after
5 minutes. The service forces `vars.gamename = gta5` so the client's browser
filter (`init(["gta5"])`) shows them.

## Server configuration (on the NSM server)

```
sv_lan "true"        # no Cfx connect tickets — required for direct connect
sv_hostname "..."    # shown in the browser
```

`sv_master1` already defaults to `http://194.104.146.133/api/serverlist/ingress`
in NSM server builds; the heartbeat is sent regardless of license key.

## Notes

- `ticket-signing-key.pem` is the RSA key whose public half is served at
  `/api/ticket/pubkey`. Only relevant if a server runs `sv_lan false`; the
  ticket-issuing flow is not implemented, so keep `sv_lan true`.
- Heartbeats are accepted from any source; protect the port or trust your
  network if listing garbage entries is a concern.
