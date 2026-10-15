#!/usr/bin/env python3
"""
NSM Discord feed poller
=======================
Polls one Discord channel and mirrors new messages into the NSM masterlist
official feed (shown in the client's home feed panel).

Setup (one time, on the Discord side):
  1. https://discord.com/developers/applications -> your app (or New App)
  2. Bot tab -> Add Bot -> Reset Token -> copy it
  3. Bot tab -> enable "Message Content Intent" (REQUIRED)
  4. OAuth2 -> URL Generator -> scopes: bot -> perms: View Channels,
     Read Message History -> open the URL -> invite to YOUR server
  5. Give the bot read access to the updates channel.

Config (environment or /opt/nsm-masterlist/poller.env):
  DISCORD_BOT_TOKEN   bot token from step 2
  DISCORD_CHANNEL_ID  1545984006196166736
  MASTERLIST_ADMIN    http://127.0.0.1:80/admin  (masterlist base + /admin)
  OWNER_TOKEN         the same owner token as the masterlist
  POLL_INTERVAL       seconds between polls (default 45)

Systemd unit: nsm-feed-poller.service (included in the kit).
"""
import json, os, sys, time, urllib.request

DISCORD_API = "https://discord.com/api/v10"

def env(k, d=None):
    v = os.environ.get(k, d)
    if v is None:
        sys.exit(f"missing env {k}")
    return v

TOKEN = os.environ.get("DISCORD_BOT_TOKEN", "")
CHANNEL = os.environ.get("DISCORD_CHANNEL_ID", "1545984006196166736")
ADMIN = os.environ.get("MASTERLIST_ADMIN", "http://127.0.0.1:80/admin")
OWNER = os.environ.get("OWNER_TOKEN", "")
INTERVAL = int(os.environ.get("POLL_INTERVAL", "45"))
STATE_FILE = os.environ.get("POLLER_STATE", "poller-state.json")

# load .env style file if present (kit deploys one)
envf = os.environ.get("POLLER_ENV_FILE", "/opt/nsm-masterlist/poller.env")
if os.path.isfile(envf):
    for line in open(envf):
        line = line.strip()
        if line and not line.startswith("#") and "=" in line:
            k, _, v = line.partition("=")
            os.environ.setdefault(k.strip(), v.strip())
    TOKEN = os.environ.get("DISCORD_BOT_TOKEN", TOKEN)
    OWNER = os.environ.get("OWNER_TOKEN", OWNER)
    ADMIN = os.environ.get("MASTERLIST_ADMIN", ADMIN)

def http_json(url, headers, data=None, method=None):
    req = urllib.request.Request(url, headers=headers,
                                 data=json.dumps(data).encode() if data else None,
                                 method=method or ("POST" if data else "GET"))
    with urllib.request.urlopen(req, timeout=30) as r:
        body = r.read()
        return json.loads(body) if body else {}

def fetch_recent():
    msgs = http_json(
        f"{DISCORD_API}/channels/{CHANNEL}/messages?limit=10",
        {"Authorization": f"Bot {TOKEN}", "User-Agent": "NSM-feed-poller"})
    return msgs  # newest first

def to_item(m):
    a = m.get("author", {})
    name = a.get("global_name") or a.get("username", "NSM")
    screen = a.get("username", "nsm")
    avatar = ""
    if a.get("avatar"):
        avatar = (f"https://cdn.discordapp.com/avatars/{a['id']}/{a['avatar']}.png?size=128")
    media = []
    for att in m.get("attachments", []):
        if att.get("content_type", "").startswith("image/"):
            media.append({
                "url": att.get("url"),
                "w": att.get("width") or 1280,
                "h": att.get("height") or 720,
            })
    ts = m.get("timestamp", "")  # RFC3339
    return {
        "id": m["id"],
        "text": (m.get("content") or "").strip(),
        "author": name,
        "screenName": screen,
        "avatar": avatar,
        "url": f"https://discord.com/channels/{m.get('guild_id','')}/{CHANNEL}/{m['id']}",
        "createdAt": ts,
        "source": "discord",
        "media": media,
    }

def push(items):
    out = http_json(f"{ADMIN}/feed/import",
                    {"Authorization": f"Bearer {OWNER}",
                     "Content-Type": "application/json",
                     "User-Agent": "NSM-feed-poller"},
                    {"items": items})
    return out

def main():
    if not TOKEN or not OWNER:
        sys.exit("DISCORD_BOT_TOKEN and OWNER_TOKEN required")
    seen = set()
    if os.path.isfile(STATE_FILE):
        try:
            seen = set(json.load(open(STATE_FILE)))
        except Exception:
            pass
    if not seen:
        # first run: only import the newest 5 so the feed isn't flooded
        try:
            for m in fetch_recent()[:5]:
                seen.add(m["id"])
            json.dump(list(seen)[-500:], open(STATE_FILE, "w"))
            print("first run: marked", len(seen), "existing messages seen (no backfill)")
        except Exception as e:
            print("initial fetch failed:", e)
    while True:
        try:
            msgs = fetch_recent()
            new = [to_item(m) for m in msgs if m["id"] not in seen and (m.get("content") or "").strip()]
            if new:
                res = push(new)
                print(f"{time.strftime('%H:%M:%S')} pushed {res.get('added', '?')} items "
                      f"(skipped {res.get('skipped', '?')})", flush=True)
            for m in msgs:
                seen.add(m["id"])
            if len(seen) > 500:
                seen = set(list(seen)[-500:])
            json.dump(list(seen), open(STATE_FILE, "w"))
        except Exception as e:
            print(f"{time.strftime('%H:%M:%S')} poll error: {e}", flush=True)
        time.sleep(INTERVAL)

if __name__ == "__main__":
    main()
