#!/usr/bin/env bash
# Reference autoresearch live driver — wraps `flowstate run --agent
# default-assistant` so each trial of `flowstate autoresearch run`
# produces a candidate edit to the surface file.
#
# Per the Autoresearch Live Driver Integration plan (April 2026,
# vault commit `11ee9ed`) § 5.2, this is the canonical operator-readable
# example of a live driver. Operators wanting a different driver shape
# (a research model, a local llama, a scripted heuristic edit) copy
# this script and edit the body — the env-var contract and surface-write
# convention are the only load-bearing parts.
#
# ============================================================
# Contract
# ============================================================
#
# Env vars consumed (set by the autoresearch harness; see
# runDriverScript in internal/cli/autoresearch_loop.go):
#   FLOWSTATE_AUTORESEARCH_PROMPT_FILE — absolute path to the synthesised
#       per-trial prompt (4 sections: PROGRAM / SURFACE / HISTORY /
#       INSTRUCTION). MUST exist; absent value is a hard error.
#   FLOWSTATE_AUTORESEARCH_RUN_ID      — the run identifier; combined with
#       FLOWSTATE_AUTORESEARCH_TRIAL to produce a unique session id.
#   FLOWSTATE_AUTORESEARCH_TRIAL       — 1-based trial counter.
#   FLOWSTATE_AUTORESEARCH_SURFACE     — surface path RELATIVE to the
#       worktree (cwd). The driver writes the new content here.
#   FLOWSTATE_AUTORESEARCH_DRIVER_MAX_TURNS — soft cap on agent turns
#       (currently informational; the harness wall-clock --driver-timeout
#       is the hard stop). Default 10 if unset.
#
# Test-only escape hatches (off by default):
#   FLOWSTATE_AUTORESEARCH_DRIVER_OUTPUT — when set, the driver skips
#       the `flowstate run` invocation and reads the agent response from
#       this file. Used by the Slice 2 hermetic spec to exercise the
#       fenced-block parser without provider auth.
#   FLOWSTATE_BIN — explicit path to the flowstate binary; bypasses the
#       PATH / worktree-build fallback chain.
#
# Output convention (parsed back into the surface):
#   The agent's response MUST contain a single fenced block tagged
#   `surface`:
#
#     ```surface
#     <full updated surface contents>
#     ```
#
#   The driver applies the block's contents verbatim as the new surface
#   (atomic write via temp-file + rename). Per the parser robustness
#   contract in plan § 5.2 R1.3:
#     1. Primary    — single ` ```surface ` block.
#     2. Fallback A — single ` ``` ` (no language tag) block, ONLY when
#                     the response contains exactly one fenced block.
#     3. Failure    — multiple fenced blocks with no `surface` tag, OR
#                     no fenced block at all → exit non-zero with a
#                     clear stderr message; the harness records
#                     `validator-io-error`.
#   Fallback B (unified-diff via `patch -p1`) is documented in the plan
#   as an engineer's-call extension; this MVP ships the two cases above.
#
# Exit codes:
#   0  — applied an edit successfully.
#   2  — `flowstate run` invocation failed (non-zero exit, missing
#         binary, etc).
#   3  — fenced-block parse failure (driver-no-edit-produced).
#   4  — surface write failure (atomic rename failed).
#
# Working directory: the harness invokes the script with cwd ==
#   <worktree-root>; FLOWSTATE_AUTORESEARCH_SURFACE is relative to this.
#
# Per-trial session lifecycle: the script names the session
#   `autoresearch-${RUN_ID}-trial-${TRIAL}` so operators can inspect
#   driver turns post-hoc via `flowstate session show`. Sessions are
#   NOT cleaned up — operators run `flowstate session prune` if they
#   want to reclaim them (plan § 12 Q5 v1).

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

require_env FLOWSTATE_AUTORESEARCH_PROMPT_FILE
require_env FLOWSTATE_AUTORESEARCH_RUN_ID
require_env FLOWSTATE_AUTORESEARCH_TRIAL
require_env FLOWSTATE_AUTORESEARCH_SURFACE

PROMPT_FILE="$FLOWSTATE_AUTORESEARCH_PROMPT_FILE"
RUN_ID="$FLOWSTATE_AUTORESEARCH_RUN_ID"
TRIAL="$FLOWSTATE_AUTORESEARCH_TRIAL"
SURFACE_REL="$FLOWSTATE_AUTORESEARCH_SURFACE"

if [[ ! -f "$PROMPT_FILE" ]]; then
  echo "default-assistant-driver: prompt file does not exist: $PROMPT_FILE" >&2
  exit 2
fi

SURFACE_ABS="$(pwd)/$SURFACE_REL"
if [[ ! -f "$SURFACE_ABS" ]]; then
  echo "default-assistant-driver: surface file does not exist: $SURFACE_ABS" >&2
  exit 2
fi

# ------------------------------------------------------------
# Binary resolution — mirror planner-validate-commit.sh's three-tier fallback.
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
  # Worktree-build fallback. The autoresearch harness scores each
  # trial from a freshly-cloned worktree; if the operator has no
  # `flowstate` on PATH and FLOWSTATE_BIN is unset, build inside the
  # worktree on first invocation. The build is invariant per trial
  # for manifest/skill surfaces (no Go code changes), so the cost is
  # paid once and reused.
  local repo_root
  repo_root="$(pwd)"
  echo "default-assistant-driver: building flowstate binary inside worktree at $repo_root/build/flowstate" >&2
  if ! ( cd -- "$repo_root" && make build >/dev/null 2>&1 ); then
    echo "default-assistant-driver: failed to build flowstate binary at $repo_root" >&2
    return 1
  fi
  echo "$repo_root/build/flowstate"
}

# ------------------------------------------------------------
# Agent invocation
# ------------------------------------------------------------

# Capture the agent's response text in $RESPONSE.
RESPONSE=""

if [[ -n "${FLOWSTATE_AUTORESEARCH_DRIVER_OUTPUT:-}" ]]; then
  # Test-only escape hatch — read the canned agent response from a
  # file. The Slice 2 hermetic spec uses this to exercise the
  # fenced-block parser without provider auth.
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

  # Read the synthesised prompt and pass it via --prompt. flowstate
  # run does not currently support --prompt-file, so the prompt body
  # is shell-substituted; the prompt file is bounded by the synthesiser
  # so a few KiB is the realistic ceiling.
  PROMPT_BODY="$(cat -- "$PROMPT_FILE")"

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
# Fenced-block parser
# ------------------------------------------------------------
#
# Strategy:
#   1. Try to extract a ` ```surface ` ... ` ``` ` block (primary).
#   2. If none found, count the bare ` ``` ` fences in the response;
#      if exactly two (i.e. one fenced block with no language tag),
#      extract its body (Fallback A).
#   3. Otherwise → exit 3 (driver-no-edit-produced).
#
# The parser uses awk to keep the dependencies POSIX. The opening
# fence regex matches `^[[:space:]]*` + "```surface" + optional
# trailing whitespace; the closing fence matches `^[[:space:]]*` +
# "```" + optional trailing whitespace. Multiple `surface` blocks
# emit the first one and a stderr warning (per plan § 5.2 — engineer
# may tighten to a hard error in a follow-up).

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
  # Used when no ```surface block was found AND there is exactly one
  # bare ``` fenced block in the response.
  awk '
    BEGIN { count = 0 }
    /^[[:space:]]*```[[:space:]]*$/ { count++ }
    END { print count }
  ' <<< "$RESPONSE" > /tmp/.fence-count.$$ 2>/dev/null
  local fence_count
  fence_count="$(awk '/^[[:space:]]*```[[:space:]]*$/ { count++ } END { print count+0 }' <<< "$RESPONSE")"
  rm -f /tmp/.fence-count.$$
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

NEW_SURFACE="$(extract_surface_block)"
if [[ -z "$NEW_SURFACE" ]]; then
  NEW_SURFACE="$(extract_bare_block)"
fi

if [[ -z "$NEW_SURFACE" ]]; then
  echo "default-assistant-driver: driver-no-edit-produced (no fenced block found in response)" >&2
  exit 3
fi

# ------------------------------------------------------------
# Atomic surface write
# ------------------------------------------------------------

TMP_SURFACE="${SURFACE_ABS}.driver-tmp.$$"
trap 'rm -f -- "$TMP_SURFACE"' EXIT

if ! printf '%s\n' "$NEW_SURFACE" > "$TMP_SURFACE"; then
  echo "default-assistant-driver: failed to write temp surface at $TMP_SURFACE" >&2
  exit 4
fi

if ! mv -- "$TMP_SURFACE" "$SURFACE_ABS"; then
  echo "default-assistant-driver: failed to rename $TMP_SURFACE → $SURFACE_ABS" >&2
  exit 4
fi

trap - EXIT
exit 0
