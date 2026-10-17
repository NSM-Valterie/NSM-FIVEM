# NSM v0.7.7 audit — index (2026-10-17)

Additive-only audit pass for the two user-selected tracks. Nothing in
v0.7.7 was altered, rebuilt, replaced, or overwritten.

| File | Contents |
|---|---|
| `V077-SOURCE-IDENTITY.md` | Exact v0.7.7 commit/tree/tag, release asset digests, locked-set definition |
| `V077-LOCKED-PATHS.txt` | The 35 locked paths (v0.7.7 commit diff vs. its parent) |
| `check-v077-locked.sh` | Automated diff gate — FAILS if any locked path changes |
| `REFERENCE-SCAN-V077.md` | Workstream A: fresh reference scan at v0.7.7 + classification |
| `KEYMASTER-SERVERKEY-AUDIT.md` | Workstream B: registration/heartbeat/key flow map + NSM-owned key design |
| `REVIEW-TABLE.md` | Workstream A bucket-6 line-level REVIEW table (4 cosmetic candidates, rest KEEP) |
| `WINDOWS-ACCEPTANCE-RUNBOOK.md` | Dedicated-server + two-player test — ready to run on Windows, not yet executed |

## Gate result for this pass

`check-v077-locked.sh` → **PASS** (zero locked paths changed; this pass
adds files only under `docs/nsm-audit-v077/`).

## Status of the wider backlog (rechecked, not assumed)

- **Dedicated server + two-player acceptance:** still open. v0.7.7's own
  release notes say the integrated ZIP was not yet run end-to-end on
  Windows and real gameplay is unverified.
- **Masterlist announce/heartbeat end-to-end:** partially verified locally
  by the v0.7.7 tests (per its release notes); production/upstream behavior
  still undocumented. The compiled-in default master in `GameServer.cpp`
  (`194.104.146.133`) is not established as user-owned — untouched.
- **Licensing/provenance report:** headers/notices preserved; the per-file
  categorization is started in `REFERENCE-SCAN-V077.md` buckets 3–4, not finished.
- **NSM-owned key service:** implemented as a standalone candidate in
  `tools/nsm-keyservice/` (17 tests passing; native heartbeat patch
  unapplied, needs Windows build). Approved 2026-10-17, not deployed.
- **Signing-key in repo:** ROTATED 2026-10-17 — `ticket-signing-key.pem`
  removed, compromised public key no longer served (503 until owner
  configures `NSM_TICKET_PUBKEY_B64`), `*.pem`/`*.key` gitignored.
  Remaining owner step: purge the old key from git history (`git filter-repo`,
  separate approval, requires force-push) and generate the new pair offline.
- **UI/launcher, sign-in/Admin, CI caching, NSM escrow:** untouched per
  instructions; status to be rechecked separately when scheduled.

## Next steps

1. Owner: run `WINDOWS-ACCEPTANCE-RUNBOOK.md` on Windows (closes the
   two-player backlog item); optionally include its step 7 (key candidate).
2. Owner: generate the replacement ticket keypair offline + decide on the
   git-history purge of the old key.
3. Separate small candidate: the 4 cosmetic strings in `REVIEW-TABLE.md`,
   with a Windows smoke test. Public NSM deployment (domain/TLS) for the
   key service remains undeployed by design.
