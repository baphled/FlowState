#!/usr/bin/env bash
# Reference autoresearch evaluator — IN-MEMORY shape (planner manifest
# warning-count wrapper).
#
# Per the April 2026 In-Memory Default plan (Slice 3), the canonical
# `planner-validate.sh` reads the candidate from stdin (or
# FLOWSTATE_AUTORESEARCH_CANDIDATE_FILE), writes it to a tempfile
# under control of this script, and runs the validate-harness against
# the tempfile path. The on-disk surface in the surface repo is never
# touched.
#
# Operators wanting the legacy worktree-cwd behaviour (validator
# reads the on-disk surface from inside the trial worktree) point
# `--evaluator-script` at `planner-validate-commit.sh` AND set
# `--commit-trials` on `flowstate autoresearch run`.
#
# Stdin: the candidate string (full).
# Stdout: one non-negative integer (sum of WARNING: lines emitted by
#   the validate-harness against the candidate manifest).
# Exit 0 on success; non-zero only on validator I/O failure.
# Stderr: human-readable diagnostic output from validate-harness.

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &>/dev/null && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/../.." &>/dev/null && pwd)"

VALIDATOR="$REPO_ROOT/scripts/validate-harness.sh"
[[ -x "$VALIDATOR" ]] || {
  echo "error: validator not found or not executable: $VALIDATOR" >&2
  exit 2
}

# Worktree-binary fallback. validate-harness.sh defaults
# `FLOWSTATE_BIN=$REPO_ROOT/build/flowstate` and dies if the
# binary is missing. Manifest validation does not change Go code, so
# falling back to the operator's host binary on $PATH is correct.
if [[ ! -x "$REPO_ROOT/build/flowstate" ]] && [[ -z "${FLOWSTATE_BIN:-}" ]]; then
  if HOST_BIN="$(command -v flowstate 2>/dev/null)" && [[ -x "$HOST_BIN" ]]; then
    export FLOWSTATE_BIN="$HOST_BIN"
  else
    echo "info: building flowstate binary at $REPO_ROOT/build/flowstate" >&2
    if ! ( cd -- "$REPO_ROOT" && make build >/dev/null 2>&1 ); then
      echo "error: failed to build flowstate binary at $REPO_ROOT" >&2
      exit 2
    fi
  fi
fi

# ------------------------------------------------------------
# Candidate acquisition — stdin OR FLOWSTATE_AUTORESEARCH_CANDIDATE_FILE.
# Both channels are populated by the harness; prefer stdin and fall
# back to the file when stdin is empty.
# ------------------------------------------------------------

CANDIDATE_BODY=""
if [[ ! -t 0 ]]; then
  CANDIDATE_BODY="$(cat)"
fi
if [[ -z "$CANDIDATE_BODY" && -n "${FLOWSTATE_AUTORESEARCH_CANDIDATE_FILE:-}" && -f "${FLOWSTATE_AUTORESEARCH_CANDIDATE_FILE}" ]]; then
  CANDIDATE_BODY="$(cat -- "$FLOWSTATE_AUTORESEARCH_CANDIDATE_FILE")"
fi
if [[ -z "$CANDIDATE_BODY" ]]; then
  echo "planner-validate.sh: no candidate on stdin and FLOWSTATE_AUTORESEARCH_CANDIDATE_FILE unset/empty" >&2
  exit 2
fi

# Stage the candidate in a tempfile under our own control so the
# validator can read it via its existing path-based API. The on-disk
# surface in the surface repo is never touched.
TMP_DIR="$(mktemp -d -t autoresearch-pv-XXXXXX)"
trap 'rm -rf -- "$TMP_DIR"' EXIT

# Honour any operator-supplied agents-dir override; otherwise stage
# the candidate at the standard planner.md path the validate-harness
# expects.
TMP_AGENT_DIR="$TMP_DIR/agents"
mkdir -p "$TMP_AGENT_DIR"
TMP_SURFACE="$TMP_AGENT_DIR/planner.md"
printf '%s' "$CANDIDATE_BODY" > "$TMP_SURFACE"

# `exec` so the validator's exit code propagates directly. The
# validator emits one integer to stdout in `--score` mode and routes
# human-readable noise to stderr, so the autoresearch evaluator
# contract is satisfied without further plumbing.
exec "$VALIDATOR" --score --all --agent-file "$TMP_SURFACE"
