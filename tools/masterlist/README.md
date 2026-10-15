# NSM masterlist v2

Standalone masterlist for NSM Community (AxiomFX). Single Go binary,
standard library only. v2 adds: **server upvotes**, **fake players with
auto-generated names** (owner-only), the **official feed** (Discord +
owner posts shown in the client home screen), and an **owner admin API**.

## Deploy (VPS)

```bash
mkdir -p /opt/nsm-masterlist && cd /opt/nsm-masterlist
# unpack nsm-masterlist-vps.zip here
cp masterlist.env.example masterlist.env   # set OWNER_TOKEN!
cp poller.env.example poller.env           # set DISCORD_BOT_TOKEN!
cp nsm-masterlist.service nsm-feed-poller.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now nsm-masterlist
systemctl enable --now nsm-feed-poller     # once bot token is set
```

Binaries: `nsm-masterlist-linux-amd64` (rename to `nsm-masterlist`),
`nsm-masterlist-linux-arm64` (ARM VPSes), `NSM-Masterlist.exe` (Windows).

## Owner admin API (Bearer OWNER_TOKEN)

```bash
TOK="your-owner-token"; API="http://127.0.0.1:80"

# upvotes: set or add, any amount, multiple servers at once
curl -X POST $API/admin/upvotes -H "Authorization: Bearer $TOK" \
  -d '{"endpoints":["1.2.3.4:30120","5.6.7.8:30120"],"set":100}'
curl -X POST $API/admin/upvotes -H "Authorization: Bearer $TOK" \
  -d '{"endpoints":["1.2.3.4:30120"],"add":25}'

# fake players: 1..9000 per server (0 = off); auto-generates player names
curl -X POST $API/admin/fakeplayers -H "Authorization: Bearer $TOK" \
  -d '{"endpoints":["1.2.3.4:30120"],"count":250}'

# post to the in-game feed (client home screen)
curl -X POST $API/admin/feed -H "Authorization: Bearer $TOK" \
  -d '{"text":"Welcome to NSM!","author":"Cravenz","screenName":"scravenz101"}'

# inspect state
curl $API/admin/state -H "Authorization: Bearer $TOK"
```

Upvotes drive the client's "Upvote Power" stat and the Boosts sort order.
Fake players override the player count (if above real) and fill the player
list with deterministic, natural-looking names (BlazeSniper, ElectricCobra,
...). Both survive restarts (state.json).

## Feed

The client home screen feed (`/v1/public/tweets`) serves:
- **Discord posts** — the poller mirrors your `#updates` channel
  (message text + author + avatar + image attachments)
- **Owner posts** — via `/admin/feed`

Discord bot setup (one time): see `poller.env.example`. The poller pushes
new messages within ~45s. X/Twitter integration is possible later via the
same `/admin/feed/import` endpoint (needs an X developer account; X API
reads are no longer free).

## Upvote pricing (Phase 2, website)

FiveM sells upvotes at ~$6 each; NSM will match at **$5.99/upvote** with
volume packs, purchasable on the NSM website once it is hosted (Phase 2).
Until then the owner grants upvotes via the admin API above.

## Endpoints

See the header comment in `masterlist.go` for the full endpoint list.
`masterlist.py` (v1, Python) is kept as a fallback.
