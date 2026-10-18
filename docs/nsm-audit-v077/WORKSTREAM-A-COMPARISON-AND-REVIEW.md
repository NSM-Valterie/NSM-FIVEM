# Workstream A — Prior-Wave Comparison, Line-Level Review Table & Before/After Scan

Target baseline: `nsm-v0.7.7-localtest` (`1e5549cbaa39a10fdaadb3c2d9eab6502273389c`).
Locked-path gate (`docs/nsm-audit-v077/check-v077-locked.sh`): **PASS** (0 of 35 locked v0.7.7 paths touched).

---

## 1. Comparison with Prior Waves (`a1-rebrand` PRs #1–#5, #7, and v0.7.7 PR #8)

Between the restored FiveM base (`9251764`) and v0.7.7's parent (`c6248de`), prior waves modified **36 files**:

| Prior Wave | Commits | Paths Changed | Summary |
|---|---|---|---|
| A1 Rebrand 1/N–7/N | `819f297`, `b7183f7`, `67460a4`, `7187874`, `b4c6d04`, `bbe05c1` | `code/client/launcher/premake5.lua`, `code/client/launcher/LauncherConfig.h`, `code/client/launcher/launcher.rc`, `code/server/launcher/premake5.lua`, `code/tools/ci/build_server_docker_alpine.sh`, `code/tools/ci/build_server_proot_alpine.sh`, `code/tools/ci/psm1/cfxBuildContext.psm1` | Client target `FiveM.exe` → `NSM.exe`; `PRODUCT_NAME` → `NSM`; `CONTENT_NAME` → `nsm`; `LINK_PROTOCOL` → `nsm`; `FXServer` → `nsm-server`; client `launcher.rc` metadata → `NSM` (preserving Cfx attribution). |
| Endpoint & Masterlist Wave | `9179aec`, `4b33199`, `a874d21`, `6da4341`, `f391cc8` | `code/client/shared/CnlEndpointOverride.h`, `code/components/citizen-server-impl/src/GameServer.cpp`, `code/components/citizen-server-impl/src/InitConnectMethod.cpp`, `code/components/pool-sizes-state/src/PoolSizesState.cpp`, `ext/ui-build/build.cmd`, `ext/ui-build/patch_ui_urls.ps1`, `data/client/components.json`, `data/server/components.json`, `tools/masterlist/*` | Masterlist/CNL/UI endpoints + Python masterlist + heartbeat validation. |
| v0.7.7 Local Repair (**LOCKED**) | `1e5549c` (`nsm-v0.7.7-localtest`) | 35 paths under `tools/local-stack/**` and `tools/release-077/**` | Confirmed entitlement rollback (`legitimacy.dll` upstream HTTPS URLs restored), Go local backend (`0.7.7-local`), startup scripts, and tests. **All 35 paths are locked.** |

---

## 2. Bucket-6 Line-Level Review Table (Outside Locked v0.7.7 Paths)

Per the Master Prompt:
- Do **not** alter the working v0.7.7 client launcher/UI (`code/client/launcher/**`, `ext/ui-build/**`, `tools/local-stack/**`, `tools/release-077/**`).
- Do **not** modify registry mappings, installation folders, functional download/cache URLs (`content.cfx.re`, `runtime.fivem.net`), runtime/API IDs, vendor code, or `LICENSE` / `THIRD_PARTY_NOTICES.md`.
- Rebrand only confirmed cosmetic/user-visible items outside the locked v0.7.7 set and outside the working client launcher/UI.

| File : Line | Current Text (at `1e5549c`) | Classification & Risk Analysis | Action Taken in Candidate |
|---|---|---|---|
| `code/server/launcher/server.rc:47-53` | `CompanyName: "Cfx.re"`, `FileDescription: "Cfx.re Platform Server (FXServer)"`, `LegalCopyright: "(C) 2017-2020 Cfx.re"`, `OriginalFilename: "FXServer.exe"`, `ProductName: "CitizenFX"` | **Cosmetic / Server EXE VERSIONINFO metadata.** `code/server/launcher/premake5.lua` was already renamed to `nsm-server` in A1 3/N, and `code/client/launcher/launcher.rc` was updated in A1 5/N, but `server.rc` was missed. Zero runtime/client impact. Preserves Cfx attribution in `LegalCopyright` (`Based on the Cfx project (see THIRD_PARTY_NOTICES.md)`) and top header. | **APPLIED** |
| `code/server/launcher/src/Main.cpp:55,64` | `"The Cfx.re Platform Server requires support for x86-64-v2 instructions (such as POPCNT).\n"` | **Cosmetic / Server CLI error prose.** Pure human-readable message on unsupported pre-2010 CPUs; preserves the functional `https://aka.cfx.re/fxs-kvm64` troubleshooting link on line 66. | **APPLIED** (`The NSM Platform Server...`) |
| `code/server/launcher/src/Server.cpp:69` | `fmt::printf("Cfx.re Platform Server (FXServer) %s\n", GIT_DESCRIPTION);` | **Cosmetic / Server `--version` banner.** Printed only when invoked with `--version` / `-V`. Preserves terms/docs links on lines 72 and 81. | **APPLIED** (`NSM Platform Server (nsm-server) %s\n`) |
| `code/components/conhost-v2/src/UrlConfirmationModal.cpp:17` | `#define PRODUCT_NAME "FiveM"` | **Cosmetic / External URL modal display text.** Substituted only into localized warning text (`"You are now leaving {0} and visiting an unaffiliated external site..."`). Zero effect on URL parsing or security check. | **APPLIED** (`#define PRODUCT_NAME "NSM"`) |
| `code/client/launcher/Bootstrap.cpp:155` | `L"Please do not place FiveM.exe in your game folder..."` | **Client launcher dialog.** Cosmetic string, but resides in `code/client/launcher/`. Deferred to avoid touching any client launcher source while v0.7.7 launcher is reported working. | **DEFERRED** (keep client launcher untouched) |
| `code/client/launcher/GameSelect.cpp:164,408` | `L"...Enhanced edition of GTA V, which is currently not supported by FiveM.\n\n"` | **Client launcher dialog.** Resides in `code/client/launcher/`. Deferred per rule not to alter the working client launcher. | **DEFERRED** (keep client launcher untouched) |
| `code/client/launcher/MiniDump.cpp:454` | `L"FiveM crashed due to %s.\n%s"` | **Client launcher crash dialog.** Resides in `code/client/launcher/`. Deferred per rule not to alter the working client launcher. | **DEFERRED** (keep client launcher untouched) |
| `code/client/launcher/MiniDump.cpp:1379` | `gettext(L"FiveM crashed... but we're on it!")` | **Localization lookup key (`msgid`).** Changing this string without regenerating all `.mo` binary catalogs in `data/client/citizen/locales/*/LC_MESSAGES/cfx.po` breaks translation lookup. | **KEEP** (functional `gettext` key) |
| `code/client/launcher/MiniDump.cpp:525,956` | `_wcsnicmp(hash.c_str(), L"fivem.exe+", 10)` / `wcscpy(filename, L"\\FiveM.exe")` | **Crash hash / minidump stack normalization.** Used to compute canonical crash signatures matching `crash-data.json`. | **KEEP** (functional crash-hash logic) |
| `code/client/launcher/Installer.cpp:36,68,94,99,189,378` | `L"\\FiveM"`, `Uninstall\\CitizenFX_`, `CitizenFX.FiveM.SDK` | **Install root path, Windows Registry keys, and AppUserModelID.** Master prompt explicitly forbids modifying user installation or registry mappings. | **KEEP** (functional install/registry paths) |
| `code/client/launcher/Main.cpp:359,362,660,670,692,817,1091` | `SOFTWARE\\CitizenFX\\`, `CitizenFX_LogMutex`, `CitizenFX.ini`, `CitizenFX_GameMutex` | **Registry keys, named Win32 mutexes/events, config filenames.** Required for cross-process synchronization between launcher and game/CEF subprocesses. | **KEEP** (functional IPC/config identifiers) |
| `data/client/citizen/locales/*/LC_MESSAGES/cfx.po` | Translated strings & Weblate headers | **Localization source catalogs.** Must stay aligned with compiled `gettext` `msgid` keys until a full localization rebuild wave is scheduled. | **KEEP** |

---

## 3. Changed-Path List & Before/After Scan Report

### Changed Source Paths (Workstream A)
1. `code/server/launcher/server.rc`
2. `code/server/launcher/src/Main.cpp`
3. `code/server/launcher/src/Server.cpp`
4. `code/components/conhost-v2/src/UrlConfirmationModal.cpp`

- **Locked v0.7.7 paths modified:** `0 / 35` (`docs/nsm-audit-v077/check-v077-locked.sh` → **PASS**)
- **Prior A1/masterlist baseline paths (`9251764..c6248de`) modified:** `0 / 36`

### Before/After Scan Table (excluding `docs/nsm-audit-v077/` and `tools/nsm-keyservice/`)

Reproduce command:
`git grep -I -n -E -e '<pattern>' -- ':!docs/nsm-audit-v077/' ':!tools/nsm-keyservice/'`

| Pattern | v0.7.7 (`1e5549c`) Lines / Files | Candidate (`HEAD`) Lines / Files | Delta (Lines / Files) |
|---|---|---|---|
| `[Cc]fx\.re` | 1,308 / 633 | 1,302 / 633 | **-6 lines** / 0 files (top `Cfx project` headers preserved in those files) |
| `\bCfx\b` | 639 / 601 | 634 / 601 | **-5 lines** / 0 files |
| `fivem` (lower) | 699 / 116 | 699 / 116 | **0 lines** / 0 files (functional URLs/identifiers preserved) |
| `FiveM` (cased) | 364 / 158 | 363 / 157 | **-1 line / -1 file** (`UrlConfirmationModal.cpp`) |
| `[Cc]itizen[Ff][Xx]` | 1,196 / 371 | 1,195 / 370 | **-1 line / -1 file** (`server.rc` `ProductName`) |
| **Combined legacy set** | **2,953 / 1,012** | **2,946 / 1,011** | **-7 lines / -1 file** |
