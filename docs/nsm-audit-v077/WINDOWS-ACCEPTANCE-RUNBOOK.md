# Dedicated-server + two-player acceptance runbook (Windows)

Status: **READY TO RUN — not yet executed.** This sandbox is Linux with no
GTA V / Windows build, so this test can only run on the owner's Windows PC.
It closes the oldest open backlog item: *"a working browser does not prove
the dedicated-server path."* Nothing in v0.7.7 is modified by running it.

## Setup (both machines, legit installs only)

1. Keep the working v0.7.7 install untouched in its own folder; extract the
   server package to a *separate* folder. Keep Steam + Rockstar signed in
   normally on the client PC. Do not merge folders.
2. Server config: your normal `server.cfg` **plus** the v0.7.7-supplied
   `server-local.cfg` lines (`sv_master1 http://127.0.0.1:80/…` for a
   same-PC test, or the LAN IP variant for two PCs). Note the shipped
   warning: an unpatched older server binary also heartbeats to its
   compiled-in default — record whether `local-server-heartbeat.patch`
   has been applied to the server under test; either is acceptable, but
   the result differs and must be written down.
3. Start the backend via `START-LOCALHOST.bat` (v0.7.7); confirm it reports
   the correct version/PID and no port conflict.

## Test steps

| # | Step | Pass criteria |
|---|---|---|
| 1 | Start dedicated server | Server console reaches ready state, no fatal/license errors |
| 2 | Wait ≤ 5 min | Server appears in the NSM server browser with correct hostname and `0` players (no fake players) |
| 3 | Client A connects from browser | Loads in, spawns, can move/drive for 2 min |
| 4 | Client B (second PC/account, legit GTA copy) connects | Both players see each other, position/vehicle sync visible both ways |
| 5 | Chat/voice-free sanity: both stay connected 10 min | No drops, no timeout kicks |
| 6 | Restart server, repeat step 2 | Server re-lists after restart (heartbeat works) |
| 7 | (Key candidate only, if testing it) set `sv_nsmServerKey` to a valid key against the candidate key service in `public` mode | Listed; then revoke the key → server disappears within 5 min and heartbeat returns 403 |

## Evidence to capture (redact before sharing)

- Server console log + client `CitizenFX.log` tails (remove tokens, IPs
  if public, account names).
- Screenshot of the browser showing the server, and one in-game shot with
  both players visible.
- Note explicitly: server build/version, whether the heartbeat patch was
  applied, same-PC or two-PC, and any step that failed with its exact error.

## Fail handling

Record the failing step and stop — do not reinstall GTA, change registry
mappings, swap DLLs, or disable any authentication. Report back and the
failure gets diagnosed from the logs. Rollback is just closing the test
folders; the working v0.7.7 install was never touched.
