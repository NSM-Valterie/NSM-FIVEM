# NSM masterlist service

Minimal replacement for the Cfx masterlist/identity endpoints, pointed at by
NSM clients (v3+) and servers (v6+). Ships as **standalone binaries — nothing
to install** (no Python, no .NET, no dependencies):

| File | Runs on |
|---|---|
| `NSM-Masterlist.exe` | Windows x64 |
| `nsm-masterlist-linux-amd64` | Linux x64 (most VPSes) |
| `nsm-masterlist-linux-arm64` | Linux ARM64 |
| `masterlist.py` | anything with Python 3.8+ (fallback, same behavior) |

## Windows

```bat
:: from an ELEVATED prompt (port 80 needs admin):
NSM-Masterlist.exe
:: or without admin:
NSM-Masterlist.exe --port 8080
```

Open the firewall once (elevated):
```bat
netsh advfirewall firewall add rule name="NSM Masterlist" dir=in action=allow protocol=TCP localport=80
```

To keep it running 24/7: NSSM (`nssm install NSM-Masterlist C:\path\NSM-Masterlist.exe`)
or Task Scheduler ("At startup", run whether user is logged on or not).

## Linux VPS (recommended)

```bash
mkdir -p /opt/nsm-masterlist && cp nsm-masterlist-linux-amd64 masterlist-deps/* /opt/nsm-masterlist/ 2>/dev/null || true
cp nsm-masterlist-linux-amd64 /opt/nsm-masterlist/nsm-masterlist
chmod +x /opt/nsm-masterlist/nsm-masterlist
cp nsm-masterlist.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now nsm-masterlist
curl http://127.0.0.1/health        # -> NSM masterlist service
```

Open TCP 80 inbound in the VPS firewall. Quick test without systemd:
`./nsm-masterlist-linux-amd64 --port 8080`

## Verify it works

```bash
curl http://194.104.146.133/health
# -> NSM masterlist service

# fake a heartbeat, then check the list:
curl -X POST http://194.104.146.133/api/serverlist/ingress \
  -H 'Content-Type: application/json' \
  -d '{"port":30120,"fallbackData":{"dynamic":{"clients":0,"hostname":"test","svMaxclients":48}}}'
curl http://194.104.146.133/api/servers/single/127.0.0.1:30120
```

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
in NSM server v6+; the heartbeat is sent regardless of license key.

## Rebuilding from source

`masterlist.go` is the source (Go standard library only):
`GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o NSM-Masterlist.exe .`

## Notes

- `ticket-signing-key.pem` is the RSA key whose public half is served at
  `/api/ticket/pubkey`. Only relevant if a server runs `sv_lan false`; the
  ticket-issuing flow is not implemented, so keep `sv_lan true`.
- Heartbeats are accepted from any source; protect the port or trust your
  network if listing garbage entries is a concern.
