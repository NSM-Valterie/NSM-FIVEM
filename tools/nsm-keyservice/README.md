# NSM server-key service — candidate (not deployed)

Implements the design in `docs/nsm-audit-v077/KEYMASTER-SERVERKEY-AUDIT.md` §3:
an NSM-owned server key that gates **NSM masterlist heartbeats only**.

## Guarantees

- Valid key → heartbeat accepted (200), server listed. Missing key in
  `public` mode → 401, **not listed**. Invalid → 401. Expired/revoked →
  403, **not listed**. Failure is closed; nothing silently accepts.
- Only PBKDF2-HMAC-SHA256 hashes (per-key salt) are stored — never plaintext
  secrets, never in logs. Admin routes require `NSM_KEY_ADMIN_TOKEN` and
  are disabled (503) when it is unset. A key cannot revoke or claim the
  endpoint of another account's key.
- `localtest` mode keeps the v0.7.7 loopback behavior (no key accepted,
  entries labelled `localOnly`) and must never run on a public host.
- This service **does not** touch Rockstar/GTA entitlement, Steam auth,
  Cfx portal/keymaster validation, or Cfx escrow, and impersonates no
  Cfx endpoint.

## Run / test

```bash
NSM_KEY_ADMIN_TOKEN='<admin token>' NSM_KEY_MODE=public \
  python3 tools/nsm-keyservice/nsm_keyservice.py --port 8080

python3 -m unittest discover -s tools/nsm-keyservice -v   # 17 tests
```

Issue a key: `POST /api/keys {"account":"…","ttlSeconds":86400}` with the
admin bearer token — the secret is shown **once**. Heartbeat:
`POST /api/serverlist/ingress` with `Authorization: Bearer nsm_srv_…`.
Revoke: `POST /api/keys/<id>/revoke {"account":"<owner>"}`.

## Not yet wired up (separate reviewed steps)

- Native server heartbeat header: see `native/GameServer-serverkey.patch`
  (**unapplied** design patch adding `sv_nsmServerKey`; requires a Windows
  build + acceptance run before it may ship).
- Public deployment (domain/TLS/hosting): none configured; no production
  endpoint was changed by this candidate.
