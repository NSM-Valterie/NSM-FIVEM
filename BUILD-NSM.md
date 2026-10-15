# Building NSM-FIVEM (A1) on Windows

Phase P2 of the A1 plan: build the stock client + server UNMODIFIED from this repo to
prove the toolchain, before any rebrand edits land on `a1-rebrand`.

## Requirements (one-time)

1. **Visual Studio 2022** (Community or higher) with workloads:
   - .NET desktop development
   - Desktop development with C++
   - Windows application development
   - Individual components: **.NET Framework 4.6 targeting pack**, **Windows 11 SDK (10.0.22000.0)**
2. **PowerShell 7+**, **Python 3.8+** (with `py` launcher; if Python >= 3.12: `pip install setuptools`)
3. **MSYS2** installed at `C:\msys64`
4. **Node.js + Yarn classic** in PATH (`node`, `yarn`)
5. ~60+ GB free disk, a legitimately installed GTA V, and `git` with symlink support enabled

## Build steps (cmd.exe, from the repo root)

```bat
git config core.symlinks true
git submodule update --jobs=16 --init

fxd get-chrome
prebuild

:: client
fxd gen -game five
fxd vs -game five     :: opens the solution -> Build (Debug or Release)

:: server
fxd gen -game server
fxd vs -game server   :: -> Build
```

Outputs land in `code\bin\five\debug\` (client) and `code\bin\server\` (server).

## Dev data setup (client run-testing)

- `v8.dll` etc. are copied automatically after the build.
- Optional: symlink `game-storage` to save disk:
  `mklink /d game-storage "%localappdata%\NSM\NSM.app\data\game-storage"`
  (inside `code\bin\five\debug\data\`).
- If the game starts but shows only background + cursor + F8 console:
  copy `citizen\ui` from an installed NSM (`%LocalAppData%\NSM\NSM.app\citizen\ui`)
  into `code\bin\five\debug\citizen\ui`.

## Known issues (from upstream docs)

- Error 126 / DLL issues: a step was missed — redo from `submodule update`.
- Error 127: add an antivirus exclusion for the repo, rebuild.
- CS0234/CS0246 errors: `fxd gen` ran before `prebuild` — close solution, re-run
  `fxd gen`, reopen, rebuild.

## P2 acceptance

- [ ] Client solution builds (log the output)
- [ ] Server solution builds (log the output)
- [ ] Stock client runs against a legitimately installed GTA V (one window from boot)
- [ ] Logs + results reported back before starting the rebrand pass
