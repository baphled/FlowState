#!/usr/bin/env bash
# Hermetic smoke for scripts/autoresearch-drivers/default-assistant-driver-commit.sh
# (the legacy git-substrate driver renamed in Slice 3 of the April 2026
# In-Memory Default plan).
#
# Per the Autoresearch Live Driver Integration plan (April 2026, vault
# commit 11ee9ed) § 5.2 arbiter: with the FLOWSTATE_AUTORESEARCH_DRIVER_OUTPUT
# escape hatch fed a canned response, the driver applies the response to
# the surface and exits 0. Run cases:
#   1. Primary    — fenced ```surface block.
#   2. Fallback A — single bare ``` block (no language tag).
#   3. Failure    — no fenced block at all → exit 3.
#
# This is a dev-loop smoke. It is not in CI and is invoked manually by
# operators or by the Ginkgo smoke wrapper.

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &>/dev/null && pwd)"
DRIVER="$SCRIPT_DIR/autoresearch-drivers/default-assistant-driver-commit.sh"

[[ -x "$DRIVER" ]] || {
  echo "default-assistant-driver smoke: driver not executable at $DRIVER" >&2
  exit 2
}

WORKTREE="$(mktemp -d)"
trap 'rm -rf "$WORKTREE"' EXIT

mkdir -p "$WORKTREE/internal/app/agents"
SURFACE_REL="internal/app/agents/planner.md"
SURFACE_ABS="$WORKTREE/$SURFACE_REL"

echo "old surface body" > "$SURFACE_ABS"

PROMPT_FILE="$WORKTREE/.autoresearch/trial-1-prompt.txt"
mkdir -p "$(dirname -- "$PROMPT_FILE")"
echo "synthesised prompt body" > "$PROMPT_FILE"

run_case() {
  local label="$1"
  local response="$2"
  local expected_exit="$3"
  local expected_surface="$4"

  local response_file="$WORKTREE/response-${label}.txt"
  printf '%s' "$response" > "$response_file"

  # Reset the surface for each case.
  echo "old surface body" > "$SURFACE_ABS"

  local actual_exit=0
  ( cd -- "$WORKTREE" && \
      FLOWSTATE_AUTORESEARCH_PROMPT_FILE="$PROMPT_FILE" \
      FLOWSTATE_AUTORESEARCH_RUN_ID="smoke" \
      FLOWSTATE_AUTORESEARCH_TRIAL="1" \
      FLOWSTATE_AUTORESEARCH_SURFACE="$SURFACE_REL" \
      FLOWSTATE_AUTORESEARCH_DRIVER_OUTPUT="$response_file" \
      "$DRIVER" >/dev/null 2>"$WORKTREE/stderr-${label}.log"
  ) || actual_exit=$?

  if [[ "$actual_exit" != "$expected_exit" ]]; then
    echo "FAIL [${label}]: expected exit ${expected_exit}, got ${actual_exit}" >&2
    echo "stderr:" >&2
    cat "$WORKTREE/stderr-${label}.log" >&2
    return 1
  fi

  if [[ -n "$expected_surface" ]]; then
    local actual_surface
    actual_surface="$(cat -- "$SURFACE_ABS")"
    if [[ "$actual_surface" != "$expected_surface" ]]; then
      echo "FAIL [${label}]: surface mismatch" >&2
      echo "expected: ${expected_surface}" >&2
      echo "actual:   ${actual_surface}" >&2
      return 1
    fi
  fi

  echo "PASS [${label}] exit=${actual_exit}"
}

# --------------------------------------------------------------------
# Case 1 — primary fenced ```surface block.
# --------------------------------------------------------------------
RESP1=$'Here is the new surface:\n\n```surface\nnew planner body line one\nnew planner body line two\n```\n\nDone.\n'
run_case "primary-fenced-surface" "$RESP1" 0 \
  $'new planner body line one\nnew planner body line two'

# --------------------------------------------------------------------
# Case 2 — Fallback A: single bare ``` fenced block.
# --------------------------------------------------------------------
RESP2=$'Updated:\n\n```\nbare-fenced new surface\n```\n'
run_case "fallback-bare-fenced" "$RESP2" 0 \
  $'bare-fenced new surface'

# --------------------------------------------------------------------
# Case 3 — Failure: no fenced block at all.
# --------------------------------------------------------------------
RESP3=$'I did not produce a fenced block, sorry.\n'
run_case "no-fenced-block" "$RESP3" 3 ""

echo "default-assistant-driver smoke: all 3 cases passed"
