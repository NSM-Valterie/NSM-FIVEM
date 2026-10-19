# NSM-FIVEM Licensing & Provenance Report (v0.7.7 Tree)

Completed against commit `1e5549cbaa39a10fdaadb3c2d9eab6502273389c` (`nsm-v0.7.7-localtest`)
and current candidate branch `arena/b9db485b-nsm-fivem`.

---

## 1. Root License & Notice Files (Verified Present and Unmodified)

| File | Status | Purpose / Terms |
|---|---|---|
| `LICENSE` | Present, unmodified (35 lines) | Upstream CitizenFX / Cfx.re platform license terms; travels with all derived source and binaries. |
| `THIRD_PARTY_NOTICES.md` | Present, unmodified (204 lines) | Upstream third-party component notices and attributions. |
| `LICENSES/Apache-2.0.txt` | Present, unmodified | Apache License 2.0 text |
| `LICENSES/BSD-2-Clause.txt` | Present, unmodified | BSD 2-Clause License text |
| `LICENSES/BSD-3-Clause.txt` | Present, unmodified | BSD 3-Clause License text |
| `LICENSES/BSL-1.0.txt` | Present, unmodified | Boost Software License 1.0 text |
| `LICENSES/FTL.txt` | Present, unmodified | FreeType Project License text |
| `LICENSES/ISC.txt` | Present, unmodified | ISC License text |
| `LICENSES/LGPL-2.1.txt` | Present, unmodified | GNU Lesser General Public License v2.1 text |
| `LICENSES/MIT.txt` | Present, unmodified | MIT License text |
| `LICENSES/Zlib.txt` | Present, unmodified | zlib License text |
| `LICENSES/curl.txt` | Present, unmodified | libcurl License text |

---

## 2. Repository Provenance Categorization by Directory (13,599 Tracked Paths)

| Directory / Scope | Tracked Files | Provenance Category | Required Attribution & Handling Rule |
|---|---:|---|---|
| `vendor/` | 7,712 | Third-party dependencies & submodule headers/libs (V8, libnode, nvapi, curl, openssl, protobuf, etc.) | Covered by `THIRD_PARTY_NOTICES.md` and `LICENSES/*.txt`. **Do not rebrand or alter.** |
| `code/deplibs/` | 120 | Prebuilt third-party SDK headers/binaries (EOS SDK, Steam, Discord) | Third-party vendor SDKs. **Do not rebrand or alter.** |
| `code/` (excluding `deplibs/`) | 3,643 | Upstream CitizenFX / Cfx.re native client, server, and component runtime + NSM targeted patches | 557 files carry explicit `This file is part of the Cfx project - https://cfx.re/` headers pointing to `LICENSE`. **Never strip upstream file headers; when updating EXE `LegalCopyright` resources (`launcher.rc`, `server.rc`), retain `Based on the Cfx project (see THIRD_PARTY_NOTICES.md)`.** |
| `ext/` | 1,589 | Upstream extensions, FXDK SDK (`ext/sdk/`), native doc tooling, system resources, UI build scripts | Governed by `LICENSE` and individual package `LICENSE` files inside subprojects. **Keep FXDK vendor paths and package license fields untouched.** |
| `data/` | 450 | Runtime data, client/server `components.json`, localization catalogs (`cfx.po`), redistributable MSVC CRT DLLs | Localization headers and redistributables must preserve original attribution. |
| `tools/local-stack/` & `tools/release-077/` | 35 | NSM-owned v0.7.7 local backend, repair scripts, and tests | NSM-authored (100% locked per v0.7.7 protection rule). |
| `tools/masterlist/` & `tools/nsm-keyservice/` | 8 | NSM-owned masterlist and server-key/registration services | NSM-authored additive services. |

---

## 3. Verification Checks

1. **Upstream Cfx file headers:** `561` tracked files contain `"This file is part of the Cfx project - https://cfx.re/"`. Zero headers were removed or altered across all prior waves or in this candidate (`561` before, `561` after).
2. **Resource copyright attribution:** Both `code/client/launcher/launcher.rc` and `code/server/launcher/server.rc` explicitly state:
   `VALUE "LegalCopyright", "(C) 2026 NSM. Based on the Cfx project (see THIRD_PARTY_NOTICES.md)"`
3. **Rockstar / Take-Two / Cfx entitlement & escrow boundaries:**
   - Rockstar Social Club / Steam ownership and entitlement validation (`lambda.fivem.net/api/validate/entitlement*`, `InitConnectMethod.cpp`, `LicenseIdentityProvider.cpp`, `legitimacy.dll`) are preserved unmodified.
   - No Cfx escrow/DRM bypass exists in the repository or in `tools/nsm-keyservice/`.
