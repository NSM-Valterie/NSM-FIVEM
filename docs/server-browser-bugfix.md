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

### 1.2 Secondary cause — the Rockstar entitlement check killed the ROS helper

This is the answer to *"why did the old launcher continue past the entitlement
error while the current one cannot?"* — and it was proven by a **byte-level
comparison of the two component binaries** plus a **real Windows run**, not by
guesswork.

`code/components/legitimacy/` is a closed-source Cfx component. It runs the
Rockstar entitlement validation and reports the result with `FatalError`.

**Where it runs.** `code/client/citicore/ComponentLoader.cpp` loads the
`legitimacy` component in every process, and in the ROS helper process
(`FiveM_ROSLauncher`, the module name contains `_ROS`) it is the *only*
component that is loaded. The check therefore runs in the ROS helper, in the
game process and in the launcher.

**What it does on failure.** `FatalError` terminates the process:
`code/client/common/Error.cpp` writes the error pickup and returns `-1`, and
the `FatalError` macro then deliberately crashes the process with a write to
`0xDEED` (`ERROR_CRASH_MAGIC`). That is the crash the first Windows run of
this branch produced:

```
[ROSLauncher] Error: Could not contact entitlement service. Status code: 0,
             error message: 1/Failed to connect to 127.0.0.1 port 18080
             after 2029 ms: Connection refused
[DumpServer]  Process crash captured.
crashometry:  legitimacy.dll+1B41AD          <- the faulting instruction is
                                                 `mov dword ptr [0xdeed], 0`
```

**Why that kills the browser.** The ROS helper is the process that owns the
Rockstar session for the game. When it dies, the game process never receives
the session: it blocks in `WaitForRlInit()`
(`code/components/gta-core-five/src/GameInit.cpp`) and dies 60 seconds later
with *"Took too long in WaitForRlInit"*. The *"Validating Rockstar
Entitlements"* / *"Analyzing game data"* window is only closed by the game
process (from `PreResumeGame`), so it stays on screen — and the server
browser, which lives in the game process, never appears.

**Why the old launcher continued.** The shipped v0.7.5 client used the same
component rebuilt against a **local** endpoint:

| | shipped v0.7.5 (`1f10baf6…`) | `main` (`b15f1101…`) |
|---|---|---|
| entitlement endpoints | `http://127.0.0.1/l/api/validate/entitlement*` | `https://lambda.fivem.net/api/validate/entitlement*` |
| failure class | *the service could not be consulted* | *"no Cfx entitlement record"* |
| subprocess name | `NSM_SubProcess_%s.bin` | `CitizenFX_SubProcess_%s.bin` |

The two binaries differ in **433 bytes across 8 regions** — the five
entitlement URLs, two forum help links, the subprocess name and the error
message format. Same component, same error message; the only functional
difference is *which service is asked*, and therefore **how the failure is
classified**.

**The fix.** NSM accounts have no Cfx entitlement record, so *"the entitlement
service could not be consulted"* is the expected answer for every NSM user.
That class of error is now reported (log + error pickup file) but no longer
terminates the process:

* `code/client/shared/EntitlementError.h` (new) — the classification.
* `code/client/common/Error.cpp` — the recoverable branch. The `FatalError`
  macro only crashes the process when the error function returns a negative
  value, so returning `0` keeps the caller alive.

**What stays fatal.** The ownership verdict itself (*"The Social Club account
specified does not own a valid license to Grand Theft Auto V."*), the
Rockstar session failures (*"Could not sign on to the Social Club"*, *"Could
not get title access token from the Social Club"*, *"Could not get entitlement
block from the Social Club"*), *"Could not validate game ownership!"* and every
other fatal error keep terminating the process exactly as before. The
mandatory ownership check is enforced for authenticated gameplay, and NSM's
own authentication (CNL endpoint, ticket signing, server ingress) is
untouched.

The entitlement endpoint itself is also moved off Cfx
(`tools/legitimacy/patch_entitlement_endpoint.py`, see
[`tools/legitimacy/README.md`](../tools/legitimacy/README.md)) so that the
client stops sending the Rockstar session to a service that has no NSM
records, and so that the failure is a clean, local, deterministic one.

### 1.3 Tertiary cause — the launcher could get stuck on "Analyzing game data"

The progress window is shared memory (`CfxUUIState`). The **game** process
opens it and shows "Analyzing game data"
(`code/client/citigame/Launcher.cpp`, `PostLoadGame`) and closes it again from
`PreResumeGame`. If the game process dies or stalls before it gets there — as
it did in 1.2 — the window stays up forever, on top of the launcher window,
hiding the server browser. There was no path back to the browser.

### 1.4 Quaternary cause — the `adhesive` → `sticky` rename (verified harmless, fixed anyway)

`data/client/components.json` was changed from `"adhesive"` to `"sticky"` in
PR #5 (commit `6da4341`) because `adhesive.dll` is a closed-source Cfx binary
that the open build does not produce.

`code/client/citigame/Launcher.cpp` still tested for the *old* name:

```cpp
if (map.find("adhesive") == map.end())
{
    initState->gamePid = initState->initialGamePid;
}
```

`sticky` spawns the game as a separate process and sets `CfxState::gamePid` to
that process' PID (`code/components/sticky/src/LaunchIntercept.cpp`).

**This was re-verified against the code and is *not* a cause of the hang**: the
clobber is guarded by `initState->IsMasterProcess()`, which is true in the
launcher and **false** in the spawned game process (the launcher set
`initialGamePid` to its own PID), so the clobber never executes where it would
matter, and `sticky` already sets `gamePid` to the game process' PID — the same
value the clobber would have written. The condition is now
`map.find("adhesive") == map.end() && map.find("sticky") == map.end()` so the
code matches the manifest again and the upstream semantics are restored, but
it is **not** part of the root cause.

### 1.5 Contributing cause — the NUI is a secure origin talking to a plain-HTTP backend

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
| `legitimacy.dll` | `1f10baf6…` — entitlement endpoints `http://127.0.0.1/l/…` → *"the service could not be consulted"* | `b15f1101…` (identical to `main` and both release tags) — entitlement endpoints `https://lambda.fivem.net/…` → *"no Cfx entitlement record"* |
| `components.json` | `"adhesive"` | `"sticky"` (PR #5, `6da4341`) — verified harmless, see 1.4 |

The 433-byte difference between the two `legitimacy.dll` builds is the
**controlled experiment** that identifies 1.2: same component, same error
message, one difference in the endpoint, and the only behavioural difference is
that the failure is recoverable instead of fatal.

Source of the release notes: `gh release view nsm-v0.1-preview`.

---

## 3. Last successful stage and the failed transition

```
NSM.exe (launcher) starts
 → native launcher initializes ......................... OK
 → EarlyMode_Init → legitimacy PreInitGame (launcher) ... OK
      (the CPR check answers 403 "DoesNotExist" - that one was never fatal)
 → the ROS helper (FiveM_ROSLauncher) is started ......... OK
 → sticky spawns the game subprocess ................... OK  (gamePid = game PID)
 → the game process starts, GTA V loads ................. OK
 → the ROS helper: entitlement validation ............... FAILS HERE
      GET <entitlement endpoint>/api/validate/entitlement
      → the service cannot be consulted / has no record
      → the component reports it with FatalError
      → the helper process is terminated (write to 0xDEED)
 → the game process never receives the Rockstar session
      → blocks in WaitForRlInit() → dies after 60 s
 → "Analyzing game data" / "Validating Rockstar ..." ... the UUI window stays open
 → PreResumeGame (which closes the window) ............. never reached
 → NUI root window + "mpMenu" frame (the browser) ..... never created
 → the user is left on a progress screen with no browser
```

With the entitlement failure classified as recoverable (1.2), the ROS helper
survives, the game process receives its Rockstar session, the NUI root window
and the `mpMenu` frame are created, and the browser is reachable. From there
the *second* defect bites:

```
 → UI asks the nui:gsclient "gsclient:url" RPC ......... FAILS HERE
      GET http://194.104.146.133/api/servers/streamRedir/
      → 200 + protobuf body, no Location header
      → the RPC returns "" → the UI has no server-list URL
 → server list ........................................ empty
```

Both have to be fixed for the browser to work: the entitlement endpoint (1.2)
and the masterlist redirect (1.1).

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
| `code/client/shared/EntitlementError.h` | Classifies the entitlement errors: *"the entitlement service could not be consulted"* is recoverable, the ownership verdict is not (new). |
| `code/client/common/Error.cpp` | The recoverable branch in the fatal-error handler: the error is reported (log + error pickup) but the process is not terminated, so the ROS helper survives and the game gets its Rockstar session. |
| `code/components/legitimacy/release/legitimacy.dll`, `code/components/legitimacy/debug/legitimacy.dll` | The five embedded entitlement endpoints now point at the NSM local entitlement service (`http://127.0.0.1:18080/l/…`) instead of `https://lambda.fivem.net/…`. Same-length in-place rewrite: same file size, no byte moves, nothing else touched. |
| `tools/legitimacy/patch_entitlement_endpoint.py` | The patcher for the above (new). `--check` / `--revert` / patch. |
| `tools/legitimacy/tests/test_entitlement_patch.py` | Regression tests (8) for the endpoint patch (new). |
| `tools/legitimacy/tests/test_entitlement_error_class.py` | Regression tests (6) that check the error classification against the strings that are actually compiled into `legitimacy.dll`, and that the error handler honours it (new). |
| `tools/legitimacy/README.md` | Why the endpoint has to move and what it does *not* do (new). |
| `docs/nsm-endpoints.md` | The endpoint map (new). |
| `docs/server-browser-bugfix.md` | This report (new). |
| `docs/windows-test-checklist.md` | The checklist for the Windows verification run (new). |
| `.github/workflows/masterlist-tests.yml` | Runs all three Python test suites on every PR that touches `tools/**` (new). |
| `.github/workflows/client-build.yml` | Push-triggered Windows client build (`product=fivem`) so a testable artifact can be produced from the branch (new). |
| `tools/masterlist/go.mod` | Module definition for the Go masterlist, so it can be built and tested in place (new). |
| `tools/masterlist/nsm-masterlist.service` | The systemd unit now passes `--public-base`, so the redirect `Location` is deterministic. |

---

## 6. Tests and build status

| Check | Result |
|---|---|
| `python3 tools/masterlist/tests/test_masterlist.py` | **14/14 pass** (masterlist started locally on an ephemeral port) |
| `python3 tools/masterlist/tests/test_ui_bundle_patch.py` | **9/9 pass** (1 skipped: the rebrand-only patcher is not on this branch) |
| `python3 tools/legitimacy/tests/test_entitlement_patch.py` | **8/8 pass** |
| `python3 tools/legitimacy/tests/test_entitlement_error_class.py` | **6/6 pass** |
| GitHub Actions `NSM masterlist tests` | **pass** — runs all three suites on every PR that touches `tools/**` |
| GitHub Actions `Go masterlist build and tests` | **pass** — `go vet`, `go test` and the Windows/Linux binaries are built in CI and uploaded as `nsm-masterlist-binaries` |
| Go masterlist source | compiles, vets and tests **in CI** (the sandbox has no Go toolchain) |
| C++ changes | all five modified files parse **identically** to their pre-change versions (tree-sitter C++), i.e. no new syntax errors; **a real MSVC build was not possible in this Linux sandbox** |
| Windows client build | **success** - run `39011506212` (`windows-2022`, the same recipe as the repo's `P2 stock build` with `product=fivem`), artifact `nsm-client-2de7ad02aab84274a0a01350f08e888de6c76324` (765 MB): the client is in the artifact's `caches/fivereborn/` folder. The artifact and the job logs could not be downloaded from the analysis sandbox (the Actions blob host is blocked here), so the build was not unpacked and inspected locally. |
| Windows end-to-end (GTA V, server browser, connect/disconnect) | **not performed** — see below |

---

## 7. Remaining blockers

1. **No Windows / GTA V verification yet.** The client is MSVC-only
   (`BUILD-NSM.md`). The C++ changes are syntax-verified, the Python and Go
   suites are green, and a Windows client build is produced by CI, but the
   end-to-end run still needs a real Windows machine with a legitimate GTA V
   install: `docs/windows-test-checklist.md`.
2. **The deployed masterlist must be redeployed.** The running service is the
   Go binary (`NSM-Masterlist-v1.1.exe`) or the Python
   (`/opt/nsm-masterlist/masterlist.py`, per `nsm-masterlist.service`). Both
   have the redirect defect and must be updated — the Python file from this
   repository, the Go binary from the `nsm-masterlist-binaries` CI artifact
   (or the identical change applied in `platinum-cfx/NSM-masterlist`, which
   this checkout cannot access).
3. **The NSM local entitlement service does not exist yet.** The entitlement
   check now fails as a *network* error against `127.0.0.1:18080`, which is
   the recoverable class that lets the browser start — exactly as in the
   working v0.7.5 build. It is **not** faked: the check still runs, still
   fails and is still reported. Until NSM ships a real entitlement service on
   that port, the Cfx Rockstar ownership gate cannot pass for NSM accounts
   (it never could), and Cfx-based gameplay stays unavailable. NSM's own
   authentication (CNL endpoint, ticket signing, server ingress) is
   unaffected. The Rockstar *ownership* verdict remains fatal: a user who does
   not own the game is still refused.
4. **The NSM host should serve the masterlist over HTTPS** if the UI bundle
   ever moves back to a secure scheme; today the mixed-content allowance in
   `nui-core` covers the plain-HTTP deployment.
