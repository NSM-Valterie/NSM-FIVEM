# NSM-FIVEM Master Prompt — Current Project State (Updated After v0.7.8 Candidate)

**Updated:** 2026-10-17 / 2026-10-18 UTC  
**Active Session Branch:** `arena/b9db485b-nsm-fivem`  
**Open Pull Request:** [#10](https://github.com/platinum-cfx/NSM-FIVEM/pull/10)  
**Isolated Candidate Release:** [`nsm-v0.7.8-candidate`](https://github.com/platinum-cfx/NSM-FIVEM/releases/tag/nsm-v0.7.8-candidate) (prerelease; `nsm-v0.7.7-localtest` untouched)

---

## 1. Mandatory v0.7.7 Protection Status — VERIFIED & ENFORCED

- **v0.7.7 Source Tag / Commit:** `nsm-v0.7.7-localtest` → `1e5549cbaa39a10fdaadb3c2d9eab6502273389c` (tree `b3784fcc7dce5e5d9b4b666d0b08d838b6e7ecae`).
- **Locked-Path List:** `docs/nsm-audit-v077/V077-LOCKED-PATHS.txt` (35 paths under `tools/local-stack/**` and `tools/release-077/**`).
- **Automated Diff Gate:** `docs/nsm-audit-v077/check-v077-locked.sh` → **PASS** (`0 / 35` locked v0.7.7 paths changed).
- **Client Launcher & UI:** `code/client/launcher/**` and `ext/ui-build/**` remain 100% untouched.

---

## 2. Completed Workstreams in `nsm-v0.7.8-candidate` (PR #10)

### Workstream A — Remaining Cfx/FiveM Reference Sweep
- **Deliverables:**
  - `docs/nsm-audit-v077/REFERENCE-SCAN-V077.md`
  - `docs/nsm-audit-v077/WORKSTREAM-A-COMPARISON-AND-REVIEW.md`
- **Completed:**
  - Compared prior waves (`9251764..c6248de`, 36 files) and v0.7.7 (`1e5549c`, 35 locked files) against the full 13,599-file tree.
  - Produced the line-by-line Bucket-6 review table separating safe cosmetic strings from functional `gettext` `msgid` keys, crash-hash stack normalizers, Windows Registry/install-root paths, and IPC mutexes.
  - Applied 4 confirmed cosmetic/user-visible server-launcher and modal changes outside locked paths and outside the client launcher/UI (`code/server/launcher/server.rc`, `code/server/launcher/src/Main.cpp`, `code/server/launcher/src/Server.cpp`, `code/components/conhost-v2/src/UrlConfirmationModal.cpp`) and recorded the before/after scan report (`-7` legacy lines, `0` locked paths touched).

### Workstream B — Keymaster/Server-Key Portal & Validation in `tools/local-stack/` (`NSM-Masterlist.exe`)
- **Deliverables:**
  - `docs/nsm-audit-v077/KEYMASTER-SERVERKEY-AUDIT.md`
  - `tools/local-stack/keymaster.go` & `tools/local-stack/site/templates/keymaster.tmpl`
  - `tools/local-stack/accounts.go`, `tools/local-stack/masterlist.go`, `tools/local-stack/website.go`, `tools/local-stack/local_api_test.go`
  - `tools/local-stack/site/templates/servers.tmpl` & `tools/local-stack/site/templates/server.tmpl` (`fivem://connect/` → `nsm://connect/`)
- **Completed:**
  - Replaced the `/keymaster` stub in `tools/local-stack/` with the live NSM Keymaster Portal and JSON APIs (`/api/keymaster/keys`, `/api/keymaster/issue`, `/api/keymaster/revoke`, `/api/nsm/servers/register`).
  - Added salted 100,000-round HMAC-SHA256 server-key storage (`nsm_srv_<16-hex-id>.<secret>`, `subtle.ConstantTimeCompare`), fail-closed key validation on `POST /api/serverlist/ingress` (supporting `Authorization: Bearer`, `X-NSM-Server-Key`, `listingToken`, and `sets sv_nsmServerKey` in `server.cfg` for unmodified `nsm-server.exe`), automatic stripping of key vars from public listings, cross-account IDOR & endpoint-hijack prevention (`403`), immediate eviction on key revocation/expiration, and `-require-keys` (`NSM_REQUIRE_KEYS=1`).
  - Verified with `go test -race -v ./...` and `go vet ./...` in `tools/local-stack/` (**12/12 test suites + 20 route subtests PASS**, including `TestKeymasterFullMatrix`), and cross-compiled `GOOS=windows GOARCH=amd64` `NSM-Masterlist.exe`.

### Backlog Track — Licensing & Provenance Report
- **Deliverable:** `docs/nsm-audit-v077/LICENSING-PROVENANCE-REPORT.md`
- **Completed:** Categorized all 13,599 tracked files across `vendor/`, `code/deplibs/`, `code/`, `ext/`, `data/`, and `tools/`, confirming 100% preservation of `LICENSE`, `THIRD_PARTY_NOTICES.md`, `LICENSES/*.txt`, and all 561 upstream `This file is part of the Cfx project` headers.

---

## 3. Remaining Windows Runtime / Infrastructure Backlog

1. **Windows Dedicated Server + Two-Player Runtime Acceptance:**
   - Runbook and automated verifier ready at `docs/nsm-audit-v077/DEDICATED-SERVER-ACCEPTANCE-RUNBOOK.md` and `tools/nsm-keyservice/verify_dedicated_server.py`. Requires a live two-player Windows session to mark runtime-verified.
2. **Committed Sample Signing Key (`tools/masterlist/ticket-signing-key.pem`):**
   - `tools/nsm-keyservice/keyservice.py` refuses to serve the committed sample key (`/api/ticket/pubkey` returns `503` until an external rotated key is configured). Removing `tools/masterlist/ticket-signing-key.pem` from `main` awaits explicit owner approval.
3. **Production NSM Domain/TLS Infrastructure:**
   - Default masterlist endpoint in `GameServer.cpp` (`194.104.146.133`) remains untouched per instructions until owner-verified NSM domain/TLS infrastructure is approved.
