# Workstream A — remaining Cfx/FiveM reference sweep, refreshed at v0.7.7

Static scan only. No source file was changed. Scan target is the exact
v0.7.7 tree (`1e5549c`, 13,593 tracked files), read via `git grep` so the
working tree and the locked release were never modified.

## Fresh numbers (2026-10-17, v0.7.7 tree)

Reproduce: `git grep -I -n -E -e '<pattern>' nsm-v0.7.7-localtest --`

| Pattern | Lines | Files |
|---|---|---|
| `cfx\.re` | 1,268 | 620 |
| `Cfx` | 1,122 | 689 |
| `fivem` (lower) | 699 | 116 |
| `FiveM` | 364 | 158 |
| `citizenfx` (any case) | 110 | 67 |
| Combined legacy set (`cfx.re`/CitizenFX/FiveM/`Cfx`) | 3,432 | 1,069 |
| `content.cfx.re` | 590 | 9 |
| `runtime.fivem.net` | 65 | 5 |
| `portal-api` | 1 | 1 |
| `servers-frontend` | 1 | 1 |

Historical comparison: the retained inventory recorded 2,143 files /
6,394 lines at the older `d6f7212` scan. The fresh combined figure
(1,069 files / 3,432 lines) is **not directly comparable** — pattern set
and classification differ — so it must not be quoted as "X% cleaned."
The correct next step is a per-file REVIEW list (below), not a bulk replace.

## Classification of what remains (buckets, with evidence)

1. **Functional download/update/cache URLs — KEEP until an NSM replacement exists**
   - `code/client/launcher/GameCache.cpp` — 641 legacy lines, 579 of the
     `content.cfx.re` hits: DLC/game-cache manifest URLs (`runtime.fivem.net/patches/…`).
   - `runtime.fivem.net` (65 lines / 5 files), `static.cfx.re/natives/…`,
     `aka.cfx.re/mono-rt2-preview`, `cfx.re/join/`, translations endpoints.
   - These break the client if rebranded blindly. **No action.**

2. **Runtime / engine / API identifiers — KEEP (compatibility)**
   - The bulk of `code/` hits (2,946 lines / 931 files): `Cfx` type/namespace
     names (`CfxRGBA`, `CfxState`…), CitizenFX component names, natives and
     CLR API surface (`code/client/clrcore/**`). Renaming breaks resources,
     scripts, and the FXDK SDK. **No action.**

3. **Vendor / third-party — KEEP**
   - `vendor/` (8 lines), `code/deplibs/` EOS SDK, `ext/` SDK/system-resources
     (160 lines), yarn bundles (the `licenseKey` hits there are package-license
     bookkeeping, not server keys). **No action.**

4. **Legal / provenance headers — KEEP (required attribution)**
   - The "This file is part of the Cfx project — https://cfx.re/" header
     (565 `https://cfx.re/` hits are mostly this) travels with the upstream
     license (`LICENSE`, `THIRD_PARTY_NOTICES.md`). **No action.**

5. **NSM-owned tooling/docs — already NSM, minor residue only**
   - `tools/` (34 lines / 13 files), docs/root (107 lines / 19 files).
     Examples: the v0.7.7 local site still labels its stub page
     "Portal (keymaster)" (`tools/local-stack/website.go`, **locked** —
     leave it; renaming is a UI change and v0.7.7 UI is reported working).

6. **REVIEW candidates (user-visible, cosmetic) — small, explicit list**
   Only these are even eligible for a future rebrand candidate, each
   outside the locked set, each needing line-by-line review:
   - `code/client/launcher/Main.cpp`, `Installer.cpp`, `MiniDump.cpp`,
     `Bootstrap.cpp` — check for user-visible "FiveM" strings in dialogs/
     crash text (23/21/21/8 legacy lines respectively; many will be
     functional identifiers, so expect most to stay).
   - Root/docs prose (`README.md`, `BUILD-NSM.md`, `docs/**`) describing
     the project as a FiveM snapshot — documentation accuracy, low risk.
   - `data/` (177 lines / 26 files) — inspect per file; component/update
     metadata is likely functional.
   Nothing in this bucket was changed in this pass.

## Explicit non-goals (reaffirmed)

- No global search-and-replace. No URL re-pointing without a lawful,
  working NSM replacement. No touching runtime IDs, vendor code, or
  license headers. No edits inside the locked v0.7.7 paths.

## Deliverable status

- Changed paths in this pass: **only** new files under `docs/nsm-audit-v077/`.
- Locked-path diff: **empty** (gate: `check-v077-locked.sh` → PASS).
- Next step (needs owner approval): turn bucket 6 into a line-level
  REVIEW table (file:line, current text, proposed text, risk) before any
  rebrand edit is proposed.
