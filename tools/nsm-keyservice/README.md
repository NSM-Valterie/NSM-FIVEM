# NSM Server Key & Registration Service (`tools/nsm-keyservice`)

Additive, isolated Workstream B candidate implementing the authorized NSM-owned
server key and registration design documented in
[`docs/nsm-audit-v077/KEYMASTER-SERVERKEY-AUDIT.md`](../../docs/nsm-audit-v077/KEYMASTER-SERVERKEY-AUDIT.md) (§3).

Nothing in the locked v0.7.7 release (`tools/local-stack/**`, `tools/release-077/**`)
or existing `tools/masterlist/**` was modified.

## Scope & Security Guardrails

1. **NSM services only:** Authenticates dedicated servers registering and sending
   heartbeats to NSM's own server list (`/api/nsm/servers/register` and
   `/api/serverlist/ingress`).
2. **Fail-closed (`public` mode default):** Missing, malformed, unknown, expired,
   or revoked `nsm_srv_<id>.<secret>` keys return `401`/`403` and **never** list
   an unauthorized server.
3. **Salted hash storage only:** Raw secrets are returned once at issuance and
   never stored or logged; only per-key random 128-bit salts and
   200,000-iteration PBKDF2-HMAC-SHA256 digests are stored, verified with
   `hmac.compare_digest`.
4. **Anti-IDOR & endpoint isolation:**
   - Account B cannot view or revoke Account A's keys (`403`).
   - Account B's valid key cannot overwrite or hijack an active endpoint
     (`ip:port`) registered by Account A (`403 endpoint_owned_by_another_account`).
   - Revoking or expiring a key immediately evicts any active server listing
     bound to that key.
5. **Non-impersonation & entitlement preservation:**
   - Never touches, mocks, or bypasses Rockstar/GTA entitlement validation
     (`lambda.fivem.net/api/validate/entitlement*`).
   - Never impersonates `portal-api.cfx.re` or `cfx.re/api/register`.
   - Never touches or bypasses Cfx escrow/DRM.

## Running the Automated Test Matrix

```bash
python3 -m unittest discover -s tools/nsm-keyservice -v
```

| Test Case | Verified Behavior |
|---|---|
| `test_1_valid_unexpired_key_registers_and_lists_server` | `200`, listed in `/api/servers/single/<ep>` and protobuf `/api/servers/streamRedir/`, raw key never leaked |
| `test_2_unknown_malformed_or_wrong_secret_key_rejected_and_not_listed` | `401`/`403`, never listed, no secret echoed |
| `test_3_expired_key_rejected_and_evicted` | `403 expired_key`, evicted from listing |
| `test_4_revoked_key_rejected_and_immediately_evicted` | `403 revoked_key`, immediately evicted upon revocation |
| `test_5_no_key_in_public_mode_rejected_fail_closed` | `401 missing_key`, never listed |
| `test_6_localtest_mode_allows_no_key_but_rejects_bad_keys` | Loopback keyless allowed only when `mode="localtest"` (labeled `localtest`); invalid keys still fail closed (`403`) |
| `test_7_cross_account_idor_and_endpoint_hijack_prevented` | Cross-account revoke and endpoint hijack rejected with `403` |
| `test_8_entitlement_and_cfx_routes_never_impersonated_and_secrets_redacted` | Upstream entitlement/registration paths return `404 out_of_scope_upstream_only`; log redaction verified |
| `test_9_e2e_dedicated_server_two_player_heartbeat_and_counts` | Dedicated server boot (0 players) -> 2-player session heartbeat -> `/runtime/counts.json` (`[2, 1, 2]`) + `/api/ticket/pubkey` `503` guardrail verified |

## Included Native Server Patch & Config (Reviewable, Opt-In)

- `native-server-key-header.patch`: Clean patch (`git apply --check` verified) for `code/components/citizen-server-impl/src/GameServer.cpp` that reads `sv_nsmServerKey` and attaches `Authorization: Bearer <key>` on HTTP heartbeats without placing key material in the public listing JSON.
- `server-nsm-key.cfg`: Example server configuration template.
