# NSM v0.7.7 audit — index (2026-10-17)

Additive-only audit pass for the two user-selected tracks. Nothing in
v0.7.7 was altered, rebuilt, replaced, or overwritten.

| File | Contents |
|---|---|
| `V077-SOURCE-IDENTITY.md` | Exact v0.7.7 commit/tree/tag, release asset digests, locked-set definition |
| `V077-LOCKED-PATHS.txt` | The 35 locked paths (v0.7.7 commit diff vs. its parent) |
| `check-v077-locked.sh` | Automated diff gate — FAILS if any locked path changes |
| `REFERENCE-SCAN-V077.md` | Workstream A: fresh reference scan at v0.7.7 + bucket classification |
| `WORKSTREAM-A-COMPARISON-AND-REVIEW.md` | Workstream A: prior-wave comparison, line-by-line Bucket-6 review table, 4 safe cosmetic edits outside locked/client-launcher paths, and before/after scan report |
| `KEYMASTER-SERVERKEY-AUDIT.md` | Workstream B: registration/heartbeat/key flow map + NSM-owned key design and link to additive candidate (`tools/nsm-keyservice/`) |
| `LICENSING-PROVENANCE-REPORT.md` | Source-license, `THIRD_PARTY_NOTICES.md`, and directory provenance categorization across all 13,599 tracked files |
| `DEDICATED-SERVER-ACCEPTANCE-RUNBOOK.md` | Step-by-step runbook + automated verifier (`tools/nsm-keyservice/verify_dedicated_server.py`) for 2-player dedicated server acceptance |
| `MASTER-PROMPT-HANDOFF.md` | Authoritative updated project state handoff tracking completed workstreams, PR #10, and `nsm-v0.7.8-candidate` |

## Gate & test results for this candidate

- `docs/nsm-audit-v077/check-v077-locked.sh` → **PASS** (zero locked v0.7.7 paths changed).
- `python3 -m unittest discover -s tools/nsm-keyservice -v` → **PASS** (10/10 matrix + E2E + verifier tests).
- `python3 -m unittest discover -s tools/nsm-admin -v` → **PASS** (3/3 clean-room NSM Sign-In & Admin Panel tests).
- `python3 -m unittest discover -s tools/masterlist -v` → **PASS** (8/8 existing masterlist tests).

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
- **NSM-owned key service:** design only (`KEYMASTER-SERVERKEY-AUDIT.md` §3).
  Needs explicit approval before implementation.
- **Signing-key in repo:** `tools/masterlist/ticket-signing-key.pem`
  flagged for owner rotation/removal decision (see V077-SOURCE-IDENTITY.md).
- **UI/launcher, sign-in/Admin, CI caching, NSM escrow:** untouched per
  instructions; status to be rechecked separately when scheduled.

## Next steps needing owner approval

1. Approve the Workstream A bucket-6 line-level REVIEW table (no edits yet).
2. Approve (or reject) implementing the NSM-owned server-key design §3 as
   a separate candidate, with the §3 test matrix as acceptance criteria.
3. Decide on the committed signing-key rotation/removal.
4. Schedule the dedicated-server + two-player Windows acceptance run.
