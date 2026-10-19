# NSM — Powered by AxiomFX (`AFX`)

**NSM (New State Multiplayer)** is a standalone, self-hosted GTA V multiplayer platform and full client/server/web ecosystem powered by the **AxiomFX (`AFX`)** runtime.

All core platform networks — including the **Masterlist**, **Server Browser**, **Server Policy & OneSync/EUP Unlock Service**, **Keymaster**, **In-Game Client Authentication**, **Upvotes & Featured Servers**, **Forum**, **Docs**, **Support**, and **Marketplace** — run independently on NSM infrastructure (**`https://nsmultiplayer.com`** / **`147.189.174.173`**) while maintaining **100% real-world runtime compatibility** with FiveM scripts, frameworks, and custom streaming assets, and preserving legitimate Rockstar Games GTA V ownership verification.

---

## Status: Confirmed Working in Production (`nsm-v0.8.0-candidate`)

| Component | Artifact | Status | Summary |
| :--- | :--- | :--- | :--- |
| **NSM Client Installer & Launcher** | `NSM.exe` | **Confirmed Working** | Single-file Windows GUI installer & launcher (`%LOCALAPPDATA%\NSM\NSM.app`), branded as `NSM - Powered by AFX`, `AxiomFX/` runtime directory, Steam App ID `218` (`Source SDK Base 2007`), fast edge game-storage updates, and in-app `nsmultiplayer.com` sign-in. |
| **NSM Dedicated Server** | `NSM-Server.zip` (`nsm-server.exe`) | **Confirmed Working** | Dedicated server runtime with `AxiomFX-*.dll` binaries, `afx>` console prompt, default `AFXserver` hostname, baked-in NSM masterlist ingress (no `sv_master1` URL needed in `server.cfg`), and automatic EUP + 2048-slot OneSync support. |
| **NSM Masterlist & Web Platform** | `NSM-Masterlist.exe` | **Confirmed Working** | All-in-one Go service powering `https://nsmultiplayer.com`, live server ingress/egress, in-app Discourse/RSA client auth, `/p/api/policy/` server policy unlock, Keymaster (`nsmk_...`), Server Upvotes (`1,000,000` Founder allowance for `nta~`), Featured Servers, Forum, Custom Profiles, Support, Marketplace, and Docs. |

---

## Release Downloads

- **Latest Release (`nsm-v0.8.0-candidate`)**:
  `https://github.com/platinum-cfx/NSM-FIVEM/releases/tag/nsm-v0.8.0-candidate`
  - **Client Installer (`NSM.exe`)**: `https://github.com/platinum-cfx/NSM-FIVEM/releases/download/nsm-v0.8.0-candidate/NSM.exe` (or `https://nsmultiplayer.com/download`)
  - **Dedicated Server (`NSM-Server.zip`)**: `https://github.com/platinum-cfx/NSM-FIVEM/releases/download/nsm-v0.8.0-candidate/NSM-Server.zip` (or `https://nsmultiplayer.com/download-server`)
  - **Masterlist & Website Server (`NSM-Masterlist.exe`)**: `https://github.com/platinum-cfx/NSM-FIVEM/releases/download/nsm-v0.8.0-candidate/NSM-Masterlist.exe`
- **Previous Stable Fallback (`nsm-v0.7.8-candidate`)**:
  `https://github.com/platinum-cfx/NSM-FIVEM/releases/tag/nsm-v0.7.8-candidate`

---

## Architecture & What Works

### 1. 100% Independent NSM Network Infrastructure
All NSM platform, account, policy, and server-discovery routes operate directly through the NSM stack (`https://nsmultiplayer.com` / `147.189.174.173`):

- **Masterlist Ingress & Server Browser (`/api/serverlist/ingress`, `/api/servers/live`, `/api/servers/single/{ep}`, `/api/servers/streamRedir/`)**:
  - `nsm-server.exe` (`AxiomFX-server-impl.dll`) has `http://147.189.174.173/api/serverlist/ingress` baked directly into the binary (`kDefaultServerList`), so server owners **do not need any `sv_master1` URL in `server.cfg`**.
  - Live servers appear simultaneously in the **`NSM.exe` in-game server browser** and on the **`https://nsmultiplayer.com/servers`** web server list.
- **Local Server Policy & Feature Unlock Service (`/p/api/policy/*`, `/api/policy/*`)**:
  - `NSM-Masterlist.exe` and `AxiomFX-net-five.dll` automatically grant:
    - `subdir_file_mapping` — unlocks **EUP custom clothing packs** and `stream/` subfolder organization (`^` prefix assets).
    - `onesync`, `onesync_plus`, `onesync_medium`, `onesync_big`, `onesync_platinum` — unlocks **OneSync Infinity up to 2048 player slots** (`maxClients <= 4096`) for every NSM server without external subscriptions.
    - `local_evaluation` — enables offline/local policy evaluation without external checks.
- **Pool-Size Limits & Ticket Public Keys (`/v1/public/pool-size-limits`, `/l/api/ticket/pubkey`)**:
  - Served directly by `NSM-Masterlist.exe` so neither client nor server depends on external Cfx endpoints for pool configuration or ticket key retrieval.
- **Fast Edge Game-Cache Updates (`c, r, s`) + Legitimate Rockstar GTA V Entitlement**:
  - Static GTA V DLC `.rpf` byte-range requests (`HTTP 206 Partial Content`) stream directly from high-speed edge CDNs so `"Updating game storage..."` completes in seconds.
  - Legitimate Rockstar Games / GTA V ownership verification (`legitimacy.dll` entitlement checks and ROS/EOS headers) is preserved intact.

---

### 2. Full Real-World FiveM Script & Resource Compatibility
NSM is engineered so existing FiveM servers, frameworks, scripts, and custom assets run **out of the box without code changes**:

- **All Scripting Runtimes Included**:
  - **Lua 5.4** (`AxiomFX-scripting-lua.dll` / `AxiomFX-scripting-lua54.dll`)
  - **JavaScript / V8 / Node.js** (`AxiomFX-scripting-v8*.dll`)
  - **C# / Mono** (`AxiomFX-scripting-mono.dll` & v2)
- **Both `fxmanifest.lua` and `__resource.lua` Supported**:
  - While external directories and DLLs are rebranded to **`AxiomFX/`** and **`AxiomFX-*.dll`**, internal binary component registry identifiers (`citizen:resources:core`, `citizen:scripting:lua`, etc.) and script globals (`Citizen.*`, `cfx`, `RegisterNetEvent`, `TriggerServerEvent`, `exports`, `SendNUIMessage`, `RegisterNUICallback`) are preserved so all scripts load cleanly.
- **Confirmed Compatible Frameworks & Resources**:
  - **QBCore**, **Qbox**, **ESX**, **vMenu**, **ox_lib**, **ox_inventory**, **ox_target**, **oxmysql**, **pma-voice**, custom NUI (`nui://`) interfaces, custom addon vehicles, weapons, MLO interiors, and EUP clothing packs.
- **All GTA V Game Builds Supported**:
  - Supports `sv_enforceGameBuild` (`b2802`, `b2944`, `b3095`, `b3258`, etc.).

---

### 3. NSM Client (`NSM.exe`) Features
- **Single-File AppData Self-Extractor & Launcher (`tools/nsm-installer`)**:
  - Downloading and running `NSM.exe` automatically installs the client runtime into `%LOCALAPPDATA%\NSM\NSM.app`, creates Desktop and Start Menu shortcuts (`NSM.lnk`), configures `%LOCALAPPDATA%\NSM\NSM.app\desktop.ini` with the blue NSM icon (`NSM Application Data`), and launches the client.
- **Full AxiomFX (`AFX`) Rebrand**:
  - Window title: **`NSM - Powered by AFX`**.
  - Runtime folder renamed from `citizen/` to **`AxiomFX/`** (including `legitimacy.dll` runtime TLS path trampolines and MBA constant patches so `AxiomFX/ros/...` certificates load seamlessly).
  - Client & subprocess binaries renamed: `afx_curl_x86_64.dll`, `AxiomFX-*.dll`, `AxiomFXGame.dll`, `AfxLaunch_TLSDummy.dll`, `AxiomFX.ini`, `AxiomFX_log`, `AxiomFX_SubProcess_*.bin`, and `AFXCrashDump`.
  - Custom blue NSM icons across `NSM.exe`, `NSM Application Data`, and `NSM_Diag.exe` (no FiveM snail icon).
- **Steam Presence (`Source SDK Base 2007` — App ID `218`)**:
  - `steam_api64.dll` initializes `SteamAppId=218`, `SteamGameId=218`, `SteamOverlayGameId=218`, and `steam_appid.txt` prior to `steamclient64.dll` connection so Steam displays **Source SDK Base 2007** while playing NSM.
- **In-App Account Sign-In (`/user-api-key/new`)**:
  - Clicking **Sign In** inside `NSM.exe` authenticates directly against `https://nsmultiplayer.com` using RSA-2048 OAEP encrypted Discourse user API keys (`client_auth.go`) with zero `"Discourse Auth service unavailable"` errors.

---

### 4. NSM Dedicated Server (`NSM-Server.zip`) Features
- **Ready-to-Run Dedicated Server (`nsm-server.exe`)**:
  - Double-clicking `nsm-server.exe` (or running `START-NSM-SERVER.bat`) automatically detects and executes `server.cfg`.
- **AxiomFX Server Binaries & Console**:
  - Uses `AxiomFX-*.dll` components (`AxiomFX-server-impl.dll`, `AxiomFX-resources-core.dll`, etc.) and `afx_curl_x86_64.dll`.
  - Interactive server console uses the **`afx>`** prompt (`afx> status`, `afx> refresh`, `afx> ensure <resource>`).
  - Default fallback server hostname is **`AFXserver`** (replacing `default FXServer`).
- **Direct Player Connections & Automatic Masterlist Heartbeats**:
  - Bypasses external `cfxTicket2` and `sv_lan` restrictions in `AxiomFX-server-impl.dll` so players can connect over the internet or LAN without Cfx Nucleus tokens.
  - Sends live heartbeats directly to `http://147.189.174.173/api/serverlist/ingress` out of the box — **no `sv_master1` URL needed in `server.cfg`**.
  - Ships with all standard `cfx-server-data` resources (`basic-gamemode`, `chat`, `spawnmanager`, `mapmanager`, `sessionmanager`, `hardcap`, `baseevents`, `yarn`, `webpack`, etc.).

---

### 5. NSM Website, Masterlist, Keymaster & Community Stack (`NSM-Masterlist.exe`)
`NSM-Masterlist.exe` (`tools/local-stack/`) is a self-contained binary serving the entire NSM web and API ecosystem:

- **Main Website (`https://nsmultiplayer.com`)**:
  - Landing page with live player/server metrics and direct `/download` (`NSM.exe`) and `/download-server` (`NSM-Server.zip`) endpoints.
- **Unified Blue Server List (`/servers` & `/join/{endpoint}`)**:
  - Single, unified blue Server Browser UI matching `NSM.exe` with real-time search, tags, player lists, ping/location details, and **`https://nsmultiplayer.com/join/<endpoint>`** direct connect links (`X-CitizenFX-Url` header support).
- **Persistent Server Upvotes & Founder Controls**:
  - **Founder (`nta~`)** automatically holds **`1,000,000` Upvote Power** (`User.UpvoteAllowance()`), with persistent upvote storage across `/servers` and `NSM.exe` (`UpvotePower` & `BurstPower`).
  - **Featured Servers (`/v1/public/featured-servers`)**: Exclusively controllable by **`nta~`** (`POST /api/servers/feature`) and showcased at the top of `/servers`.
- **AFX Keymaster (`/keymaster`)**:
  - Generate, manage, and toggle NSM server license keys (`nsmk_...`) tied to user accounts.
- **AFX Community Forum (`/forum`), Custom Profiles (`/u/{username}`) & Support (`/support`)**:
  - Full Discourse-style forum with categories, staff-only `announcements` topic creation, real-time view counters, two-way friend requests & DMs, custom AFX profile themes, animated avatar decorations, 18 hard-to-earn badges, email verification, 2FA, Staff Moderation Panel, and Zendesk-style Support Tickets with strict per-user privacy (`MyTickets` vs staff-only `AllTickets`).

---

## Quick Start Guide

### 1. Running the Masterlist & Website (`NSM-Masterlist.exe`)
On your Windows VPS (`147.189.174.173` / `nsmultiplayer.com`):
```powershell
.\NSM-Masterlist.exe
```
- Binds to `0.0.0.0:80` by default with public URL `https://nsmultiplayer.com`.
- Automatically configures Windows Firewall rules for TCP port `80` and persists all accounts, forum posts, keys, and server upvotes in its data directory.

### 2. Installing & Playing NSM (`NSM.exe`)
1. Download **`NSM.exe`** from `https://nsmultiplayer.com/download` (or the GitHub release).
2. Run **`NSM.exe`** — it installs into `%LOCALAPPDATA%\NSM\NSM.app`, creates Desktop/Start Menu shortcuts, verifies your GTA V installation, updates game storage from fast edge mirrors, and opens the NSM launcher.

### 3. Hosting an NSM Server (`NSM-Server.zip`)
1. Download **`NSM-Server.zip`** from `https://nsmultiplayer.com/download-server` and extract it.
2. *(Optional)* Edit `NSM-Server\server.cfg` to set your `sv_hostname`, `tags`, `sv_maxclients`, and `ensure` your scripts/resources in `NSM-Server\resources\`. (No `sv_master1` URL is required.)
3. Run **`START-NSM-SERVER.bat`** or double-click **`NSM-Server\nsm-server.exe`**.
4. Open port `30120` (TCP & UDP) in your firewall — your server will automatically appear on `https://nsmultiplayer.com/servers` and inside `NSM.exe`.

---

## Repository Layout

- `code/` — C++ client (`launcher`, `diag`, `shared`, `citicore`) and server (`citizen-server-impl`, `net`, `pool-sizes-state`, etc.) source code.
- `data/client/` — Client runtime data and patched `steam_api64.dll` (Steam App ID `218`).
- `tools/local-stack/` — Go source code, HTML templates (`site/templates/`), static assets (`site/static/`), and test suite (`local_api_test.go`) for `NSM-Masterlist.exe`.
- `tools/nsm-installer/` — Windows GUI self-extracting AppData installer (`main_windows.go`) for `NSM.exe` and the server artifact builder/patcher (`build_server_artifact.py`) for `NSM-Server.zip`.
- `.github/workflows/build-v078-release.yml` — Automated CI/CD pipeline that verifies locked paths, runs `go test -race ./...`, patches client and server PE binaries, builds `NSM.exe`, `NSM-Server.zip`, and `NSM-Masterlist.exe`, and publishes GitHub releases.
