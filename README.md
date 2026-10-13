# NSM-FIVEM

NSM Multiplayer client — **A1 build**: the full FiveM (CitizenFX) client/server stack,
rebranded and re-pointed at NSM infrastructure.

- **Origin:** snapshot of `citizenfx/fivem` (imported via `platinum-cfx/fivem-source` @ `06215ba`).
- **This commit:** base import only — no code changes. License files (`LICENSE`,
  `THIRD_PARTY_NOTICES.md`, `LICENSES/*`) restored verbatim from upstream, where they
  had been blanked in the mirror.
- **Branches:** `main` = pristine base + restored notices · `a1-rebrand` = the rebrand
  work (NSM.exe naming, masterlist wiring, cfx-ui replacement).
- **License:** see `LICENSE` — the upstream terms travel with the code (Take-Two/Cfx.re
  terms for the platform, LGPL-2.0 for the "Files" carve-out listed there).
- **Building:** see `BUILD-NSM.md` (Windows + Visual Studio 2022 required).
