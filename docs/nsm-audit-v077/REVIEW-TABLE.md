# Workstream A — line-level REVIEW table (bucket 6)

Scope: the only files eligible for future cosmetic rebrand review, per
`REFERENCE-SCAN-V077.md`. Line numbers are at the **v0.7.7 tree** (`1e5549c`).
**No line below was edited.** Decision key: `KEEP` = functional/legal/runtime
identifier, renaming breaks something · `REBRAND-CANDIDATE` = user-visible
cosmetic text, safe to propose in a separate candidate · `REVIEW` = needs a
runtime check before deciding.

## code/client/launcher/Bootstrap.cpp
| Line | Current | Decision | Reason |
|---|---|---|---|
| 2 | `This file is part of the Cfx project - https://cfx.re/` | KEEP | Legal/provenance header |
| 27, 29 | `"CitizenFX.exe.new"` temp filename | KEEP | Filesystem contract with updater/bootstrap |
| 155 | `Please do not place FiveM.exe in your game folder…` (MessageBox) | **REBRAND-CANDIDATE** | User-visible; product is now `NSM.exe`. Propose: `Please do not place NSM.exe in your game folder…` |
| 283 | `CitizenFX_uninstall_%d.exe` temp name | KEEP | Internal temp artifact name, not branding |

## code/client/launcher/Installer.cpp
| Line | Current | Decision | Reason |
|---|---|---|---|
| 36, 94, 99, 189 | `CitizenFX.` ProgID / `…\Uninstall\CitizenFX_` registry keys | KEEP | Registry identity; renaming orphans existing installs — migration would need its own design |
| 68 | `\FiveM` AppData suffix | KEEP | Matches installed data layout; renaming breaks profile discovery |
| 104, 108 | `HelpLink`/`URLInfoAbout` = `https://cfx.re/` | REVIEW | User-visible in Windows uninstall info, but must only change when an NSM support/about URL actually exists |
| 115, 357 | comments (`FiveM directory`, upstream issue URL) | KEEP | Comments/upstream references, no user effect |
| 378 | `CitizenFX.FiveM.SDK` AppUserModelID | KEEP | Shell identity string |
| 462 | `CitizenFX.ini` existence check | KEEP | On-disk user config filename |

## code/client/launcher/Main.cpp
| Line | Current | Decision | Reason |
|---|---|---|---|
| 165 | `CitizenFX_ToolMode` env var | KEEP | Tooling contract |
| 268 | `CitizenFX.exe.new` | KEEP | Updater temp filename contract |
| 274 | comment `fivem or redm` protocol | KEEP | Protocol names are runtime identifiers (`fivem://` links) |
| 359, 362 | `SOFTWARE\CitizenFX\…`, `CitizenFX.%s…` AppUserModelID | KEEP | Registry/shell identity |
| 660–670, 817–818, 1091–1093 | `CitizenFX_LogMutex`, `CitizenFX.log`, named events/mutexes | KEEP | IPC/kernel object names shared across processes |
| 692 | `CitizenFX.ini` | KEEP | On-disk user config filename |

## code/client/launcher/MiniDump.cpp
| Line | Current | Decision | Reason |
|---|---|---|---|
| 319, 617, 1269, 1663 | `CitizenFX.ini`, `logs/CitizenFX_log_…` | KEEP | On-disk file naming; users/support tooling find logs by these names |
| 454 | `FiveM crashed due to %s.` | **REBRAND-CANDIDATE** | Crash-dialog text. Propose `NSM crashed due to %s.` |
| 525, 956 | `fivem.exe+` module match, `\FiveM.exe` path | KEEP | Matches real module names for crash attribution |
| 682 | MessageBox title `CitizenFX` (crash-dump saved) | **REBRAND-CANDIDATE** | Dialog title. Propose `NSM` |
| 1379 | `FiveM crashed... but we're on it!` | **REBRAND-CANDIDATE** | Crash-dialog headline. Propose `NSM crashed... but we're on it!` |
| 1423 | `A " PRODUCT_NAME " integrity check failed…` | KEEP | Already product-name aware |
| 1696 | `CFX_CRASH_INGRESS_URL "https://crash-ingress.fivem.net"` | KEEP | Functional crash-upload endpoint; no NSM replacement exists |
| 1747 | `\\.\pipe\CitizenFX_Dump` | KEEP | IPC pipe name contract |

**Net for the launcher: 4 genuine cosmetic candidates** (Bootstrap:155,
MiniDump:454/682/1379) + 1 REVIEW (Installer help links, blocked on a real
NSM URL existing). Everything else stays. Even these 4 belong in one small
separate candidate with a Windows smoke test — not in this audit pass.

## docs/prose & data/ (summary, no line edits proposed yet)
- `README.md`, `BUILD-NSM.md`, `docs/**` (107 legacy lines / 19 files):
  historical "FiveM snapshot" wording is *accurate provenance description*;
  propose only wording that distinguishes upstream origin from NSM product,
  in the same candidate as above. Decision: REVIEW (low risk, docs-only).
- `data/**` (177 lines / 26 files): component/update metadata; presumed
  functional (update channels, component names). Decision: KEEP unless a
  per-file check proves a field is display-only. No candidates identified.
