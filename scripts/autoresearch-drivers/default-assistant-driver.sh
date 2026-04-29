#!/usr/bin/env bash
# Reference autoresearch live driver — CONTENT shape.
#
# Per the April 2026 In-Memory Default plan (Slice 3), the canonical
# `default-assistant-driver.sh` reads the synthesised per-trial prompt
# from stdin (or FLOWSTATE_AUTORESEARCH_PROMPT_FILE), invokes
# `flowstate run --agent default-assistant`, parses the agent's
# fenced ```surface block, and writes the candidate VERBATIM to
# stdout. The harness owns the candidate string as substrate; this
# script never writes to the surface file on disk.
#
# Operators wanting the legacy git-mediated behaviour (driver writes
# to surface in place, harness commits per trial) point
# `--driver-script` at `default-assistant-driver-commit.sh` (the
# pre-pivot script renamed at Slice 3 of the In-Memory Default plan)
# AND set `--commit-trials` on `flowstate autoresearch run`.
#
# ============================================================
# Contract
# ============================================================
#
# Stdin: the synthesised per-trial prompt (4 sections — PROGRAM /
#   SURFACE / HISTORY / INSTRUCTION). Drivers may also read the same
#   prompt body from FLOWSTATE_AUTORESEARCH_PROMPT_FILE; both channels
#   are populated by the harness so script authors choose by
#   convenience.
#
# Stdout: the candidate, verbatim. The full string emitted to stdout
#   is the candidate the harness scores. No fenced-block wrapping on
#   the way out — the harness applies it verbatim as the new content
#   candidate.
#
# Stderr: free for diagnostics; the harness routes captured stderr
#   to its log.
#
# Env vars consumed (set by the autoresearch harness — see
# runDriverContent in internal/cli/autoresearch_loop.go):
#   FLOWSTATE_AUTORESEARCH_PROMPT_FILE — path to the same prompt body
#       piped on stdin. Either channel is fine.
#   FLOWSTATE_AUTORESEARCH_RUN_ID      — run identifier; combined with
#       FLOWSTATE_AUTORESEARCH_TRIAL to produce a unique session id.
#   FLOWSTATE_AUTORESEARCH_TRIAL       — 1-based trial counter.
#   FLOWSTATE_AUTORESEARCH_SURFACE     — surface path RELATIVE to the
#       operator's invocation cwd. Read-only by contract; the content
#       substrate intentionally does not enforce this at the seam.
#   FLOWSTATE_AUTORESEARCH_DRIVER_MAX_TURNS — soft cap on agent turns.
#       Default 10 if unset.
#
# Test-only escape hatches (off by default):
#   FLOWSTATE_AUTORESEARCH_DRIVER_OUTPUT — when set, the driver SKIPS
#       the `flowstate run` invocation and reads the agent response
#       from this file. Used by hermetic specs to exercise the
#       fenced-block parser without provider auth.
#   FLOWSTATE_BIN — explicit path to the flowstate binary; bypasses
#       the PATH / build fallback chain.
#
# Output convention (parsed back into the candidate):
#   The agent's response MUST contain a single fenced block tagged
#   `surface`:
#
#     ```surface
#     <full candidate contents>
#     ```
#
#   The driver extracts the block's body and writes it to stdout
#   followed by a single newline. Per the parser robustness contract
#   in plan § 5.2 R1.3:
#     1. Primary    — single ` ```surface ` block.
#     2. Fallback A — single ` ``` ` (no language tag) block, ONLY when
#                     the response contains exactly one fenced block.
#     3. Failure    — multiple fenced blocks with no `surface` tag, OR
#                     no fenced block at all → exit non-zero with a
#                     clear stderr message; the harness records
#                     `validator-io-error`.
#
# Exit codes:
#   0  — candidate written to stdout successfully.
#   2  — `flowstate run` invocation failed (non-zero exit, missing
#         binary, etc).
#   3  — fenced-block parse failure (driver-no-edit-produced).
#
# Working directory: the operator's invocation cwd (the harness no
# longer creates a worktree in default content mode); FLOWSTATE_AUTORESEARCH_
# SURFACE is relative to that.
#
# Per-trial session lifecycle: the script names the session
#   `autoresearch-${RUN_ID}-trial-${TRIAL}` so operators can inspect
#   driver turns post-hoc via `flowstate session show`. Sessions are
#   NOT cleaned up — operators run `flowstate session prune` if they
#   want to reclaim them.

set -euo pipefail

# ------------------------------------------------------------
# Env validation
# ------------------------------------------------------------

require_env() {
  local name="$1"
  if [[ -z "${!name:-}" ]]; then
    echo "default-assistant-driver: required env var ${name} is unset" >&2
    exit 2
  fi
}

require_env FLOWSTATE_AUTORESEARCH_RUN_ID
require_env FLOWSTATE_AUTORESEARCH_TRIAL

RUN_ID="$FLOWSTATE_AUTORESEARCH_RUN_ID"
TRIAL="$FLOWSTATE_AUTORESEARCH_TRIAL"

# ------------------------------------------------------------
# Prompt acquisition — stdin OR FLOWSTATE_AUTORESEARCH_PROMPT_FILE.
# Both channels are populated by the harness; the script prefers
# stdin and falls back to the file when stdin is empty (e.g. an
# operator runs the driver standalone for inspection).
# ------------------------------------------------------------

PROMPT_BODY=""
if [[ ! -t 0 ]]; then
  PROMPT_BODY="$(cat)"
fi
if [[ -z "$PROMPT_BODY" && -n "${FLOWSTATE_AUTORESEARCH_PROMPT_FILE:-}" && -f "${FLOWSTATE_AUTORESEARCH_PROMPT_FILE}" ]]; then
  PROMPT_BODY="$(cat -- "$FLOWSTATE_AUTORESEARCH_PROMPT_FILE")"
fi
if [[ -z "$PROMPT_BODY" ]]; then
  echo "default-assistant-driver: no prompt on stdin and FLOWSTATE_AUTORESEARCH_PROMPT_FILE unset/empty" >&2
  exit 2
fi

# ------------------------------------------------------------
# Binary resolution — three-tier fallback (FLOWSTATE_BIN env, PATH,
# repo build artefact). The repo build is keyed off this script's
# location so the driver works without a configured worktree.
# ------------------------------------------------------------

resolve_flowstate_bin() {
  if [[ -n "${FLOWSTATE_BIN:-}" && -x "${FLOWSTATE_BIN}" ]]; then
    echo "${FLOWSTATE_BIN}"
    return 0
  fi
  if HOST_BIN="$(command -v flowstate 2>/dev/null)" && [[ -x "$HOST_BIN" ]]; then
    echo "$HOST_BIN"
    return 0
  fi
  local script_dir
  script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &>/dev/null && pwd)"
  local repo_root
  repo_root="$(cd -- "$script_dir/../.." &>/dev/null && pwd)"
  if [[ -x "$repo_root/build/flowstate" ]]; then
    echo "$repo_root/build/flowstate"
    return 0
  fi
  echo "default-assistant-driver: building flowstate binary at $repo_root/build/flowstate" >&2
  if ! ( cd -- "$repo_root" && make build >/dev/null 2>&1 ); then
    echo "default-assistant-driver: failed to build flowstate binary at $repo_root" >&2
    return 1
  fi
  echo "$repo_root/build/flowstate"
}

# ------------------------------------------------------------
# Agent invocation
# ------------------------------------------------------------

RESPONSE=""

if [[ -n "${FLOWSTATE_AUTORESEARCH_DRIVER_OUTPUT:-}" ]]; then
  if [[ ! -f "$FLOWSTATE_AUTORESEARCH_DRIVER_OUTPUT" ]]; then
    echo "default-assistant-driver: FLOWSTATE_AUTORESEARCH_DRIVER_OUTPUT does not exist: $FLOWSTATE_AUTORESEARCH_DRIVER_OUTPUT" >&2
    exit 2
  fi
  RESPONSE="$(cat -- "$FLOWSTATE_AUTORESEARCH_DRIVER_OUTPUT")"
else
  if ! FLOWSTATE_BIN_PATH="$(resolve_flowstate_bin)"; then
    exit 2
  fi
  SESSION_ID="autoresearch-${RUN_ID}-trial-${TRIAL}"
  if ! RESPONSE="$(
    "$FLOWSTATE_BIN_PATH" run \
      --agent default-assistant \
      --prompt "$PROMPT_BODY" \
      --session "$SESSION_ID" \
      2>/dev/null
  )"; then
    echo "default-assistant-driver: flowstate run failed for session $SESSION_ID" >&2
    exit 2
  fi
fi

if [[ -z "$RESPONSE" ]]; then
  echo "default-assistant-driver: agent response was empty" >&2
  exit 3
fi

# ------------------------------------------------------------
# Fenced-block parser — same shape as the legacy commit-mode driver
# (see default-assistant-driver-commit.sh). The output convention is
# identical; only the destination differs (stdout in default content
# mode, surface file in commit mode).
# ------------------------------------------------------------

extract_surface_block() {
  awk '
    BEGIN { in_block = 0; saw_block = 0; warned = 0 }
    /^[[:space:]]*```surface[[:space:]]*$/ {
      if (saw_block) {
        if (!warned) {
          print "default-assistant-driver: warning — multiple ```surface blocks found, using first" > "/dev/stderr"
          warned = 1
        }
        next
      }
      in_block = 1
      saw_block = 1
      next
    }
    /^[[:space:]]*```[[:space:]]*$/ {
      if (in_block) {
        in_block = 0
        next
      }
    }
    in_block == 1 { print }
  ' <<< "$RESPONSE"
}

extract_bare_block() {
  local fence_count
  fence_count="$(awk '/^[[:space:]]*```[[:space:]]*$/ { count++ } END { print count+0 }' <<< "$RESPONSE")"
  if [[ "$fence_count" != "2" ]]; then
    echo ""
    return 0
  fi
  awk '
    BEGIN { in_block = 0 }
    /^[[:space:]]*```[[:space:]]*$/ {
      if (in_block) { in_block = 0; next }
      in_block = 1
      next
    }
    in_block == 1 { print }
  ' <<< "$RESPONSE"
}

CANDIDATE="$(extract_surface_block)"
if [[ -z "$CANDIDATE" ]]; then
  CANDIDATE="$(extract_bare_block)"
fi

if [[ -z "$CANDIDATE" ]]; then
  echo "default-assistant-driver: driver-no-edit-produced (no fenced block found in response)" >&2
  exit 3
fi

# ------------------------------------------------------------
# Emit candidate to stdout — that is the entire output contract.
# ------------------------------------------------------------

printf '%s\n' "$CANDIDATE"
exit 0
