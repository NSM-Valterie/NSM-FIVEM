# Workstream B — keymaster / server-key audit at v0.7.7 (static, no behavior change)

Scope rule (from the master prompt): map the real flows first. Only an
authorized NSM-owned key/registration flow for **NSM services** may ever be
built. Nothing here removes a check, returns "allow" for all keys, bypasses
Rockstar/GTA entitlement, impersonates Cfx services, or defeats Cfx escrow.

## 1. What the v0.7.7 source actually does (verified in-tree)

### a) Cfx server licensing / "keymaster" (`portal-api.cfx.re`)
- `code/components/citizen-server-impl/include/ServerLicensingComponent.h:36`
  still defines `LICENSING_EP "https://portal-api.cfx.re/"`.
- **However:** `LICENSING_EP` has *zero* uses anywhere else in the tree, and
  `ServerLicensingComponent` is *never instantiated* — there is no
  `SetComponent(new ServerLicensingComponent(...))` (or equivalent) in the
  v0.7.7 tree. Its only consumers are:
  - `GameServer.cpp:982` — reads `GetListingToken()` into the heartbeat JSON.
  - `ServerNucleusMock.cpp:78` — reads `GetNucleusToken()`.
  So at v0.7.7 the Cfx licensing component is effectively vestigial: no Cfx
  portal key validation runs on this code path. (Whether `GetComponent<>`
  can return an empty/null container here is a runtime question — flag for
  the dedicated-server acceptance test, do not "fix" speculatively.)
- `sv_licenseKey` survives only as a pre-parse variable name
  (`ServerInstance.cpp:39` setList) and in FXDK vendor/tooling
  (`ext/sdk/**` passes `+set sv_licenseKey …` when launching a server).
  No in-tree consumer validates it. **FXDK paths: do not modify.**

### b) Server registration / nucleus (`cfx.re/api/register`)
- `ServerNucleusMock.cpp` (misnamed — it is the real registration client):
  on tick, if `sv_licenseKeyToken` is set *and* a nucleus token exists,
  POSTs `{token, port, ipOverride}` to `https://cfx.re/api/register/?v=2`,
  then fires `_cfx_internal:nucleusConnected` and downloads promotion
  notices from `https://gss.cfx-services.net/v1/public/promotions-targeting`.
  Because the licensing component is never populated (see a), the nucleus
  token is empty in practice and this registration is inert — but the code
  path, the Cfx endpoints, and the `_cfx_internal:nucleusConnected` event
  name are all still present and live if a token ever appears.

### c) Masterlist announce / heartbeat (`GameServer.cpp`)
- Default master is compiled in: `http://194.104.146.133/api/serverlist/ingress`
  (`GameServer.cpp:54`). Per the master prompt that address is **not**
  established as user-owned — do not contact or reconfigure it.
- Heartbeat logic (lines 966–1076): the gate is already
  `// NSM: always send the masterlist heartbeat, no Cfx license key required`
  with `if (true)` (pre-v0.7.7 change, in `main`). HTTP heartbeats POST
  `{port, listingToken, ipOverride, private, fallbackData{players,info,dynamic}}`
  every 3 min to each `sv_master1..3` plus unconditionally to the compiled
  default. `server-local.cfg` + `local-server-heartbeat.patch` (locked,
  v0.7.7) document that config alone cannot suppress the default heartbeat.
- Client side: `sv_licenseKeyToken` from server info is forwarded into
  connection auth interception (`NetLibrary.cpp:1850`, `ConnectToNative.cpp:597`)
  — i.e. it is a *server-provided* token the client relays, not a client key.

### d) NSM's own local backend (v0.7.7, locked — read-only audit)
- `tools/local-stack/masterlist.go`: `POST /api/serverlist/ingress` accepts
  a heartbeat after `validateHeartbeat` (`local_api.go:48`) and loopback-only
  `ipOverride` enforcement; entries expire after 5 min. There is **no server
  key check** — any well-formed heartbeat from a reachable host lists a
  server. That is acceptable for a loopback local-test backend and is
  **not** acceptable as a public NSM masterlist design (see §3).
- `website.go:144` `/keymaster` is a stub page ("Portal (keymaster)") — no
  key issuance, no validation. Accounts are local (`accounts.go`).
- Ticket/entitlement endpoints are deliberately *not* fabricated:
  the backend returns explanatory 503/404s and v0.7.7 restored the three
  upstream entitlement-validation URLs (`AUTH-STATIC-CHECKS.json`:
  `authentication_bypassed: false`, code bytes unchanged). **Keep it that way.**

### e) Rockstar/GTA entitlement & Cfx escrow — out of scope, untouched
- Entitlement-hash handling (`InitConnectMethod.cpp`, `LicenseIdentityProvider.cpp`)
  and EOS entitlement headers are legitimate ownership validation. No change.
- No `escrow` implementation exists in-tree; third-party Cfx escrow/DRM is
  not present to bypass and must never be defeated. No change.

## 2. Classification summary

| Flow | Endpoint / key | Status at v0.7.7 | Action |
|---|---|---|---|
| Cfx portal licensing | `portal-api.cfx.re`, `LICENSING_EP` | Defined, unused; component never created | Leave code; candidate may *document*, not delete blindly |
| Cfx nucleus registration | `cfx.re/api/register/?v=2` | Inert (empty token), code live | Leave; NSM replacement = §3, separate approval |
| Masterlist heartbeat | compiled default `194.104.146.133` + `sv_master*` | Ungated by any key; always sent | Backlog: re-point only to owner-verified NSM infra |
| NSM local backend ingress | `127.0.0.1/api/serverlist/ingress` | Shape-validated, no server key | Fine for localtest; harden before any public use |
| NSM "keymaster" page | `/keymaster` (local site) | Stub, issues nothing | Design §3 before building |
| Rockstar entitlement | `lambda.fivem.net/api/validate/entitlement*` | Restored upstream, verified working | **Never alter** |
| Cfx escrow | — | Not in tree | **Never bypass** |

## 3. Design for an authorized NSM-owned server key (proposal — NOT implemented)

For NSM's *own* masterlist/services only:

1. **Issuance:** NSM account (existing local accounts design, extended)
   issues a per-server key: `nsm_srv_<public-id>.<secret>`; only a salted
   hash (e.g. Argon2id/bcrypt) + metadata (owner, hostname hint, created,
   expires, revoked flag) is stored server-side. Secret shown once.
2. **Heartbeat auth:** server sends the key in an `Authorization` header
   (never in the listing JSON). Masterlist rejects missing/invalid/expired/
   revoked keys with `401/403` + `lastError`, and does **not** list the
   server. Failure is closed: no silent acceptance, no fallback listing.
3. **Revocation/expiry:** owner can revoke from their account page; revoked
   keys fail on the next heartbeat and the entry expires within the 5-min TTL.
4. **What it must never do:** gate or replace Rockstar entitlement, Steam
   auth, or any Cfx validation; impersonate `portal-api.cfx.re`/`cfx.re`;
   unlock third-party escrow content. It attests "this server is registered
   with NSM," nothing more.
5. **Rollout:** new code in a *new* path (nothing locked), behind a config
   flag defaulting to current behavior for localtest; native server header
   support is a separate reviewed change to `GameServer.cpp` (outside the
   locked set, but behavior-changing — needs explicit owner approval).

### Required test matrix (before any acceptance package)
| Case | Expected |
|---|---|
| Valid, unexpired key | Heartbeat 200, server listed |
| Unknown / malformed key | 401/403, not listed, error logged (no key material in log) |
| Expired key | 403, not listed |
| Revoked key | 403, entry expires ≤ TTL, not listed |
| No key (public mode) | Rejected, not listed |
| No key (localtest mode) | Current behavior preserved, clearly labelled local-only |
| Key of another account | Accepted for listing only under its own registration; ownership shown correctly; cannot mutate others' entries (IDOR check) |

## 4. Direct Implementation in `tools/local-stack/` (`NSM-Masterlist.exe`)

Per explicit user instruction, §3 above is implemented directly inside the
v0.7.7 Go masterlist & website (`tools/local-stack/`):

- `tools/local-stack/keymaster.go` + `tools/local-stack/site/templates/keymaster.tmpl`:
  Replaces the `/keymaster` stub page with the live NSM Keymaster Portal
  (`GET /keymaster`, `POST /keymaster/create`, `POST /keymaster/{id}/revoke`)
  and JSON APIs (`GET /api/keymaster/keys`, `POST /api/keymaster/issue`,
  `POST /api/keymaster/revoke`, `POST /api/nsm/servers/register`).
- `tools/local-stack/accounts.go`: Stores `ServerKeys` with per-key random
  128-bit salts and 100,000-round HMAC-SHA256 digests (`subtle.ConstantTimeCompare`);
  plaintext secrets are shown once at issuance and never persisted or logged.
- `tools/local-stack/masterlist.go`: Validates `nsm_srv_<id>.<secret>` keys
  on `POST /api/serverlist/ingress` (accepted via `Authorization: Bearer`,
  `X-NSM-Server-Key`, `listingToken`, or `sets sv_nsmServerKey` in `server.cfg`),
  strips secret vars from public server data, enforces per-account endpoint
  ownership (anti-IDOR), immediately evicts listings when a key is revoked
  or expires, and supports `-require-keys` (`NSM_REQUIRE_KEYS=1`) for public
  fail-closed deployments.
- `tools/local-stack/site/templates/{servers,server}.tmpl`: Rebranded join
  links from `fivem://connect/` to `nsm://connect/` matching `LauncherConfig.h`.
- Verified with `go test -race -v ./...` in `tools/local-stack/` (12/12 test
  suites PASS, including `TestKeymasterFullMatrix`) and cross-compiled to
  Windows x64 `NSM-Masterlist.exe`.
