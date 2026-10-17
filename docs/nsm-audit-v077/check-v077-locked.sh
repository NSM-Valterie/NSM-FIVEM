#!/usr/bin/env bash
# NSM v0.7.7 locked-path diff gate.
#
# FAILS (exit 1) if any path listed in V077-LOCKED-PATHS.txt was changed
# relative to the exact v0.7.7 source commit. PASSes otherwise.
#
# Two checks:
#  1. Committed + uncommitted changes vs. the merge-base with v0.7.7
#     (for candidate branches based on the v0.7.7 commit, this is exact).
#  2. Content check: any locked path present in the working tree whose
#     blob differs from the v0.7.7 blob (catches edits on branches based
#     on the v0.7.7 *parent*, where locked files are simply absent —
#     absence on a parent-based branch is not a modification and passes).
#
# Usage:  docs/nsm-audit-v077/check-v077-locked.sh
# Requires: git, and the nsm-v0.7.7-localtest tag/commit present locally
#           (git fetch origin --tags).
set -u
cd "$(git rev-parse --show-toplevel)" || exit 2

V077_COMMIT="1e5549cbaa39a10fdaadb3c2d9eab6502273389c"
LOCKED_FILE="docs/nsm-audit-v077/V077-LOCKED-PATHS.txt"

if ! git rev-parse --verify "$V077_COMMIT^{commit}" >/dev/null 2>&1; then
  echo "GATE ERROR: v0.7.7 commit $V077_COMMIT not present locally. Run: git fetch origin --tags" >&2
  exit 2
fi
if [ ! -f "$LOCKED_FILE" ]; then
  echo "GATE ERROR: $LOCKED_FILE missing" >&2
  exit 2
fi

# locked paths, comments/blank lines stripped
mapfile -t LOCKED < <(grep -v -e '^\s*#' -e '^\s*$' "$LOCKED_FILE")
echo "Checking ${#LOCKED[@]} locked v0.7.7 paths against $V077_COMMIT"

fail=0

# --- Check 1: diff merge-base..HEAD plus working tree -------------------
BASE="$(git merge-base HEAD "$V077_COMMIT" 2>/dev/null || echo "")"
CHANGED="$( { git diff --name-only "$BASE" -- 2>/dev/null; git diff --name-only HEAD -- 2>/dev/null; git ls-files --others --exclude-standard; } | sort -u )"
for p in "${LOCKED[@]}"; do
  if printf '%s\n' "$CHANGED" | grep -qx "$p"; then
    # On a parent-based branch a locked path is absent from HEAD; an
    # untracked/modified entry only counts if the file actually exists
    # and differs (handled precisely in check 2). For branches whose
    # merge-base IS v0.7.7, any diff entry is a real change.
    if [ "$BASE" = "$V077_COMMIT" ]; then
      echo "LOCKED PATH CHANGED (committed/worktree diff): $p"
      fail=1
    fi
  fi
done

# --- Check 2: content comparison for locked paths present in worktree ---
for p in "${LOCKED[@]}"; do
  if [ -f "$p" ]; then
    want="$(git rev-parse "$V077_COMMIT:$p" 2>/dev/null || echo "")"
    got="$(git hash-object "$p" 2>/dev/null || echo "")"
    if [ -n "$want" ] && [ "$want" != "$got" ]; then
      echo "LOCKED PATH CONTENT DIFFERS FROM v0.7.7: $p"
      fail=1
    fi
  fi
done

if [ "$fail" -ne 0 ]; then
  echo "GATE: FAIL — a locked v0.7.7 path was modified. v0.7.7 must not be touched."
  exit 1
fi
echo "GATE: PASS — zero locked v0.7.7 paths changed."
exit 0
