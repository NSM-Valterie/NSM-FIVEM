# NSM v0.7.7 — exact source identity (verified 2026-10-17)

This replaces the previous "exact commit unknown / do not guess" status.
Source access was restored through the already-configured GitHub CLI
credential in this environment (`gh auth status`: logged in as the repo
owner). No token was passed through shell commands, files, or logs.

## Verified identity

| Item | Value |
|---|---|
| Tag | `nsm-v0.7.7-localtest` |
| Branch | `release/nsm-v0.7.7-localtest` |
| Commit | `1e5549cbaa39a10fdaadb3c2d9eab6502273389c` |
| Tree | `b3784fcc7dce5e5d9b4b666d0b08d838b6e7ecae` |
| Parent (= current `main`) | `c6248de6cf023e0fc95b7b163440343eab24f65e` |
| Commit subject | "Package NSM 0.7.7 local repair with confirmed entitlement rollback" |
| Commit date | 2026-10-17T23:00:11Z |
| Diff vs. parent | **35 paths, all `A` (added), 0 modified, 0 deleted** |
| Release | `nsm-v0.7.7-localtest` (prerelease, published 2026-10-17) |

Release assets (digests from the GitHub Releases API — **not** re-downloaded
or overwritten; the 237 MB client ZIP was left untouched):

- `NSM-client-v0.7.7-localtest.zip` — 237,446,408 bytes — `sha256:7b1e36a2dcdf58c6aa2cc47ea56ae88957e016cbd8fd293b48e468f85fec8eb3`
- `NSM-v0.7.7-source-and-tests.zip` — 53,559 bytes — `sha256:104e9632107c68e3fccb4ef35a25f1c3998429e0e56367a0a6725c010d13c9a2`
- `SHA256SUMS.txt` — 196 bytes — `sha256:7c578f0a2aaaf088f78c638fc6af8b2ded48f91cc3d55a5adb6dfebac40c832a`

## Locked-path protection

- `V077-LOCKED-PATHS.txt` (this folder) is the exact 35-path list from
  `git diff --name-only c6248de… 1e5549c…`. Those paths are **locked**:
  no edits, renames, deletions, rebuilds-in-place, or release replacement.
- `check-v077-locked.sh` is the automated diff gate. It fails if any locked
  path differs from the v0.7.7 blob. Run it before and after any candidate work.
- All new work in this audit is **additive only**, in `docs/nsm-audit-v077/`,
  on the session branch `arena/1a8f8994-nsm-fivem`. No v0.7.7 path, no
  existing source file, and no release asset was modified in this pass.

## Definition used for "changed for v0.7.7"

The locked set is the diff of the v0.7.7 commit against its parent — the
only defensible, reproducible definition now that the exact commit is known.
The earlier rebrand waves it builds on are already merged into `main`
(`c6248de`, via PRs #1–#5 and #7) and are treated as baseline, not locked;
deliberately they were also not edited in this pass. Any future candidate
that needs to touch a file outside the locked set still requires explicit
review per the master prompt — additive-first.

## Notes / unrelated observations (no action taken)

- Open PR #6 (`fix(client): restore the NSM server browser`, 32 files) is
  **not** part of v0.7.7 and was not touched or merged.
- `tools/masterlist/ticket-signing-key.pem` (in `main`, pre-v0.7.7) is
  private-key material committed to the repo. Its contents were not read,
  printed, or copied. **Recommendation (owner decision, separate approved
  change): treat that key as compromised, rotate it, and remove it from
  history.** Do not do this as part of either current workstream.
- The v0.7.7 release notes themselves state the integrated ZIP was not yet
  run end-to-end on Windows and real server-browser-to-gameplay is not yet
  verified — that backlog item therefore remains open (see audit README).
