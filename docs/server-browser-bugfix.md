# NSM server browser: root cause analysis and fix

**Status:** root cause identified, fix implemented, automated tests green.
Windows end-to-end verification is still outstanding (see *Remaining blockers*).

---

## 1. Root cause

### 1.1 Primary cause — the masterlist broke the client's server-list contract

The server browser is the `cfx-ui` application, served from `citizen:/ui.zip`
and hosted in the `mpMenu` NUI frame
(`code/components/citizen-game-main/src/Game.cpp`).

The UI does **not** fetch the server list itself. It calls the `nui:gsclient`
NUI RPC `gsclient:url`, which performs the HTTP request inside the game
process with `HttpRequestOptions::followLocation = false` and returns the
response's **`Location`** header to the UI
(`code/components/nui-gsclient/src/ServerList.cpp`):

```cpp
options.followLocation = false;
...
if (auto it = rhl->find("location"); it != rhl->end()) { cb(it->second); return; }
cb("");   // <-- what the NSM masterlist produced
```

The NSM masterlist answered `/api/servers/streamRedir/` with **`200` and the
protobuf stream in the body** — no `Location` header at all. The RPC therefore
returned an empty string, the UI never obtained a server-list URL, and the
browser could not retrieve its server list.

Both implementations had this defect:

* `tools/masterlist/masterlist.py` — `do_GET`, `path == "/api/servers/streamRedir/"`
* `tools/masterlist/masterlist.go` — `case path == "/api/servers/streamRedir/":`

The endpoint is named `streamRedir` precisely because it is a redirect, and the
client explicitly does not follow redirects when resolving it.

### 1.2 Secondary cause — the launcher could get stuck on "Analyzing game data"

The progress window is shared memory (`CfxUUIState`). The **game** process
opens it and shows "Analyzing game data"
(`code/client/citigame/Launcher.cpp`, `PostLoadGame`) and closes it again from
`PreResumeGame`. If the game process dies or stalls before it gets there, the
window stays up forever, on top of the launcher window — hiding the server
browser. There was no path back to the browser.

### 1.3 Tertiary cause — the game process was told not to start

`data/client/components.json` was changed from `"adhesive"` to `"sticky"` in
PR #5 (commit `6da4341`, 2026-10-14) because `adhesive.dll` is a closed-source
Cfx binary that the open build does not produce.

`code/client/citigame/Launcher.cpp` still tested for the *old* name:

```cpp
if (map.find("adhesive") == map.end())
{
    initState->gamePid = initState->initialGamePid;
}
```

`sticky` spawns the game as a separate process and sets
`CfxState::gamePid` to that process' PID
(`code/components/sticky/src/LaunchIntercept.cpp`). Because the map is keyed by
the component's manifest name (`code/components/sticky/component.json` →
`"name": "sticky"`), `map.find("adhesive")` no longer found anything, so the
condition was permanently true and `gamePid` was overwritten with the master's
PID. In the spawned game process `CfxState::IsGameProcess()` then returned
false, and `CitizenGame::Launch` bailed out before loading the game:

```cpp
if (isMainGame && !initState->IsMasterProcess() && !initState->IsGameProcess())
{
    return;
}
```

### 1.4 Contributing cause — the NUI is a secure origin talking to a plain-HTTP backend

`nui-core` registers the `nui` scheme with `CEF_SCHEME_OPTION_SECURE` and
serves the UI from `https://nui-game-internal/…`, while every NSM service URL
in the rebranded UI bundle is plain `http://194.104.146.133`. Chromium blocks
active mixed content, which would silently break every API call the UI makes.
`--disable-web-security` was already set; `--allow-running-insecure-content`
was not.

---

## 2. Working vs. broken comparison

| | Working | Broken |
|---|---|---|
| Build | `nsm-v0.1-preview` / `nsm-v0.2-preview` (rebrand commits 1–2 and 7/N) | rebrand commit `9179aec` "Rebrand masterlist, CNL and UI service endpoints to the NSM VPS" and later |
| UI bundle URLs | still `https://servers-frontend.fivem.net/…` — the v0.1 release notes state *"metadata/endpoints/masterlist wiring are not rebranded yet"* | `http://194.104.146.133/…` (patched by `ext/ui-build/patch_ui_urls.ps1`) |
| Masterlist used | Cfx's real masterlist, whose `streamRedir` answers `302` + `Location` | the NSM masterlist, whose `streamRedir` answered `200` |
| Browser result | server list loaded | `gsclient:url` → `""` → no server list |
| `components.json` | `"adhesive"` | `"sticky"` (PR #5, `6da4341`) |
| `gamePid` in the game process | set by the game component → `IsGameProcess()` true | overwritten with the master's PID → `IsGameProcess()` false → the game process exits early |

Source of the release notes: `gh release view nsm-v0.1-preview`.

---

## 3. Last successful stage and the failed transition

```
NSM.exe starts
 → native launcher initializes ......................... OK
 → GTA V location / platform detected .................. OK
 → game data analysis starts ("Analyzing game data") .. OK  (citigame/Launcher.cpp PostLoadGame)
 → analysis completes .................................. OK
 → launcher UI receives the result ..................... OK  (shared memory CfxUUIState)
 → local services initialize ........................... OK  (no fixed local port; nui: scheme)
 → NSM masterlist is queried ........................... FAILS HERE
      gsclient:url → GET /api/servers/streamRedir/ → 200, no Location
      → the RPC returns "" → the UI has no server-list URL
 → server data is parsed .............................. never happens
 → server browser becomes visible ...................... shows no servers
```

Separately, the *gameplay* path was broken by 1.3: the game process returned
out of `CitizenGame::Launch` before loading GTA V, and by 1.2 the launcher then
had no way back to the browser.

---

## 4. Endpoint / IP / port investigation

The full map is in [`docs/nsm-endpoints.md`](./nsm-endpoints.md). Summary:

* **No hard-coded localhost API port.** `9987` and `9988` do not appear
  anywhere in the sources (verified by grep over `code/`). The NUI is a CEF
  custom scheme (`nui:` / `https://nui-game-internal`), the launcher↔game IPC
  is nng over a local pipe, and the progress window is shared memory. There was
  therefore no stale-port bug to fix.
* **The masterlist is the only NSM service the browser needs**, and it was the
  one service whose contract was broken.
* **The Rockstar entitlement error is a separate, pre-existing issue** — see
  below — and is *not* the reason the browser stopped working.

### The Rockstar entitlement error

`code/components/legitimacy/release/legitimacy.dll` is a closed-source Cfx
binary. Its strings show:

```
Validating Rockstar Entitlements
https://lambda.fivem.net/api/validate/entitlement
https://lambda.fivem.net/api/validate/entitlement/rosfive
http://ros.citizenfx.internal/launcher/11/launcherservices/entitlements.asmx/%s
Error contacting Rockstar Online Services: %s -> %s.
```

* `ros.citizenfx.internal` resolves to the local Rockstar Launcher through the
  hosts file; the `DoesNotExist` response comes from Rockstar's own service and
  means the Social Club profile has no entitlement for the requested title.
* `lambda.fivem.net` is Cfx's entitlement backend. The NSM rebrand redirected
  the **open-source** `CNL_ENDPOINT` and the **UI bundle**, but the closed
  source `legitimacy.dll` still points at Cfx — it cannot be changed from this
  repository without patching the binary.
* The `legitimacy.dll` shipped with the v0.7.5 client was additionally
  binary-patched to `http://127.0.0.1/l/api/validate/entitlement`
  (commit `15dbc53`). Nothing in the client listens on `127.0.0.1:80` for that
  path, so that variant can never succeed either.

This error is **not** suppressed and no entitlement is faked. The NSM
masterlist deliberately does **not** implement `/api/validate/entitlement`
(a regression test asserts it must answer `404`), because there is no NSM
entitlement store to validate against.

What *is* fixed is the consequence: the entitlement check runs in the **game**
process, the server browser lives in the **launcher** process, and a failed or
stuck entitlement check can no longer leave the player staring at an endless
"Analyzing game data" / "Validating Rockstar Entitlements" window with no way
back to the browser.

---

## 5. Files changed

| File | Change |
|---|---|
| `tools/masterlist/masterlist.py` | `/api/servers/streamRedir/` now answers `302` with a `Location` header; new `GET /api/servers/stream/` serves the protobuf frames; new `--public-base` option; `Location` is derived from the `Host` header by default. |
| `tools/masterlist/masterlist.go` | The same change in the standalone Go implementation that produces the deployed `NSM-Masterlist.exe`. Restored here so the deployed binary can be rebuilt from this repository. |
| `tools/masterlist/masterlist_test.go` | Go regression tests for the redirect contract, the stream framing, the public base and the "no faked entitlement" rule. |
| `tools/masterlist/tests/test_masterlist.py` | Python regression tests (14) for the redirect contract, the protobuf framing, heartbeat/TTL, CORS and the entitlement rule. |
| `tools/masterlist/tests/test_ui_bundle_patch.py` | Regression tests (9) for the UI bundle replacement table and the endpoint map. |
| `code/components/nui-gsclient/src/ServerList.cpp` | `gsclient:url` now falls back to the requested URL when the server answers `2xx` without a `Location` header, so the browser also works against a masterlist that serves the list directly. |
| `code/client/citigame/Launcher.cpp` | Do not overwrite `gamePid` when the `sticky` component is present (only when neither `adhesive` nor `sticky` is). Restores the pre-PR-#5 behaviour. |
| `code/components/citizen-game-main/src/Game.cpp` | Close the stale updater window when the game process exits or disconnects, so the server browser becomes reachable again. |
| `code/client/launcher/Main.cpp` | Updater-window watchdog: close it when the game process is gone, and when a startup stage stops progressing for 5 minutes; periodic trace of the window state. |
| `code/components/nui-core/src/NUIApp.cpp` | `--allow-running-insecure-content`, so the secure NUI origin can talk to the plain-HTTP NSM services. |
| `docs/nsm-endpoints.md` | The endpoint map (new). |
| `docs/server-browser-bugfix.md` | This report (new). |

---

## 6. Tests and build status

| Check | Result |
|---|---|
| `python3 tools/masterlist/tests/test_masterlist.py` | **14/14 pass** (masterlist started locally on an ephemeral port) |
| `python3 tools/masterlist/tests/test_ui_bundle_patch.py` | **9/9 pass** (1 skipped: the rebrand-only patcher is not on this branch) |
| Go masterlist source | parses cleanly (tree-sitter); **`go build` / `go test` could not be run — no Go toolchain in the analysis sandbox** |
| C++ changes | all five modified files parse **identically** to their pre-change versions (tree-sitter C++), i.e. no new syntax errors; **a real MSVC build was not possible in this Linux sandbox** |
| Windows end-to-end (GTA V, server browser, connect/disconnect) | **not performed** — see below |

---

## 7. Remaining blockers

1. **No Windows / GTA V verification.** The client is MSVC-only
   (`BUILD-NSM.md`) and the analysis sandbox is Linux without a Go toolchain.
   The C++ and Go changes are syntax-verified and covered by tests for the
   parts that are testable, but they still need a real build and a real run:
   * build the client and confirm `NSM.exe` starts;
   * confirm the server browser lists the NSM servers;
   * confirm connect and disconnect still work.
2. **The deployed masterlist must be redeployed.** The running service is the
   Go binary (`NSM-Masterlist-v1.1.exe`). It has the same redirect defect and
   must be rebuilt from `tools/masterlist/masterlist.go` (or the identical
   change applied in `platinum-cfx/NSM-masterlist`, which this checkout cannot
   access).
3. **The Rockstar entitlement check still fails**, because the closed-source
   `legitimacy.dll` calls Cfx's `lambda.fivem.net`. It no longer blocks the
   server browser, but it does still prevent authenticated gameplay until the
   DLL is pointed at an NSM entitlement service that can validate a ROS token.
   Nothing in this change fakes that check.
4. **The NSM host should serve the masterlist over HTTPS** if the UI bundle
   ever moves back to a secure scheme; today the mixed-content allowance in
   `nui-core` covers the plain-HTTP deployment.
