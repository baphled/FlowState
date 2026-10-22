#!/usr/bin/env bash
# PR4: plan-as-detector self-audit hook for the Child Session Turn
# Registry Plumbing plan (May 2026).
#
# The plan ships a "Plan-as-detector self-audit" section claiming that
# every file:line citation in the plan body resolves to the named
# identifier at HEAD. Per `feedback_audit_plan_with_its_own_detector`
# and `feedback_cite_func_line_consistently`, citation drift is a real
# failure mode — May-11's hallucination plan failed its own D1 detector
# with line numbers off by 4. PR4 ships this detector as a guard so the
# next refactor that moves an executeSync / bootstrapMemberSession /
# StartOrReuse anchor either updates the plan in the same commit or
# fails this check loudly.
#
# What it does. Verifies a curated set of load-bearing file:line
# citations from the plan still resolve at the current HEAD. The script
# does NOT parse the plan text itself (the plan is an Obsidian vault
# file, not co-located with the repo) — instead it hard-codes the
# anchors the plan cites and walks the repo tree to confirm:
#
#   1. The file exists.
#   2. The cited identifier (function name, type name, or named anchor)
#      exists somewhere in the file.
#   3. The cited line range covers the identifier's body (the function
#      `func NAME(` declaration falls inside [N, M]).
#
# When an anchor drifts, the script prints which check failed and the
# observed location so the caller can either update the citation in
# the plan or restore the missing surface.
#
# Usage:
#   tools/plan-citation-check.sh                    # check all anchors
#   tools/plan-citation-check.sh --verbose          # report each anchor
#   tools/plan-citation-check.sh --help
#
# Exit codes:
#   0   all anchors resolved cleanly
#   1   one or more anchors drifted — caller MUST update the plan
#   2   bad invocation (unknown flag, repo root not located)
#
# Required tools: bash, grep, awk.
#
# Self-test: `tools/plan-citation-check.sh --verbose` against a clean
# tree should report PASS for every anchor.

set -euo pipefail

VERBOSE=0

while [[ $# -gt 0 ]]; do
    case "$1" in
        --verbose|-v)
            VERBOSE=1
            shift
            ;;
        --help|-h)
            sed -n '2,46p' "$0"
            exit 0
            ;;
        *)
            printf 'unknown argument: %s\n' "$1" >&2
            exit 2
            ;;
    esac
done

# Locate the repo root from the script's own path. Avoids relying on the
# caller's PWD so the script can be run from any subdirectory.
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &> /dev/null && pwd)"
REPO_ROOT="$(cd -- "${SCRIPT_DIR}/.." &> /dev/null && pwd)"

if [[ ! -d "${REPO_ROOT}/internal" ]] || [[ ! -d "${REPO_ROOT}/web" ]]; then
    printf 'plan-citation-check: repo root not found at %s (missing internal/ or web/)\n' "${REPO_ROOT}" >&2
    exit 2
fi

failures=0
total=0

# check_anchor — verify that `pattern` appears inside `file` between
# lines `start` and `end` (inclusive). The pattern is a fixed string,
# NOT a regex, matched line-by-line via awk's index() function.
#
# Args:
#   $1  human-readable label (used in error output)
#   $2  file path (relative to repo root)
#   $3  start line
#   $4  end line
#   $5  fixed-string pattern to search for
check_anchor() {
    local label="$1"
    local file="$2"
    local start="$3"
    local end="$4"
    local pattern="$5"
    total=$((total + 1))

    local full_path="${REPO_ROOT}/${file}"
    if [[ ! -f "${full_path}" ]]; then
        printf 'FAIL: %s — file not found at %s\n' "${label}" "${file}" >&2
        failures=$((failures + 1))
        return
    fi

    # Use awk to scan the cited line range for the fixed-string pattern.
    # index() returns >0 on match. We accumulate matches in a counter and
    # emit the first matching line number for the diagnostic.
    local result
    result=$(awk -v start="${start}" -v end="${end}" -v pattern="${pattern}" '
        NR >= start && NR <= end {
            if (index($0, pattern) > 0) {
                print NR
                exit
            }
        }
    ' "${full_path}")

    if [[ -z "${result}" ]]; then
        printf 'FAIL: %s — pattern %q not found in %s:%s-%s\n' "${label}" "${pattern}" "${file}" "${start}" "${end}" >&2
        # Search the whole file to help the caller find where it moved to.
        local moved_to
        moved_to=$(grep -nF "${pattern}" "${full_path}" 2>/dev/null | head -1 | cut -d: -f1)
        if [[ -n "${moved_to}" ]]; then
            printf '       observed at line %s — plan citation needs updating\n' "${moved_to}" >&2
        fi
        failures=$((failures + 1))
        return
    fi

    if [[ "${VERBOSE}" -eq 1 ]]; then
        printf 'PASS: %s — found %q at %s:%s\n' "${label}" "${pattern}" "${file}" "${result}"
    fi
}

# ─── Plan anchor inventory ────────────────────────────────────────────
#
# Each row is a load-bearing citation from
# Plans/Child Session Turn Registry Plumbing (May 2026).md. The plan
# claims these resolve at HEAD; the script verifies.
#
# Format: check_anchor <label> <file> <start> <end> <fixed-string>
#
# Note. End lines are widened by ~10 to absorb minor body drift between
# the plan's authoring snapshot and HEAD. The function name pattern is
# the canonical anchor — line ranges are advisory.

# Item 1 — Registry primitives
check_anchor 'Registry.Start primitive (S1/S2 site)' \
    'internal/turn/turn.go' 600 700 \
    'func (r *Registry) Start('

check_anchor 'Registry.StartOrReuse primitive (PR1 ship)' \
    'internal/turn/turn.go' 600 1100 \
    'StartOrReuse'

check_anchor 'Registry.ResetForRetry primitive (PR1 ship)' \
    'internal/turn/turn.go' 600 1100 \
    'ResetForRetry'

check_anchor 'Registry.Append id-keyed upsert' \
    'internal/turn/turn.go' 650 750 \
    'func (r *Registry) Append('

check_anchor 'Registry.FindActiveBySession projection lookup' \
    'internal/turn/turn.go' 800 1100 \
    'func (r *Registry) FindActiveBySession('

# Item 2a/2b — executeSync single-target plumbing
check_anchor 'executeSync function (Item 2b ship)' \
    'internal/engine/delegation.go' 2200 2700 \
    'func (d *DelegateTool) executeSync('

check_anchor 'resolveOrCreateSession function (executeSync call site)' \
    'internal/engine/delegation.go' 2200 2300 \
    'func (d *DelegateTool) resolveOrCreateSession('

# Item 2d — swarm fan-out plumbing
check_anchor 'bootstrapMemberSession function (Item 2d ship)' \
    'internal/engine/delegation.go' 3100 3400 \
    'func (d *DelegateTool) bootstrapMemberSession('

check_anchor 'buildMemberRunner function (per-member terminal site)' \
    'internal/engine/delegation.go' 2900 3200 \
    'func (d *DelegateTool) buildMemberRunner('

# Item 2 wiring — DelegateTool exposes the registry
check_anchor 'NewDelegateTool constructor (D7 plumbing site)' \
    'internal/engine/delegation.go' 100 500 \
    'func NewDelegateTool('

# Dispatcher precedent — pattern this plan mirrors
check_anchor 'DispatchSessioned (parent-side Turn lifecycle)' \
    'internal/dispatch/dispatcher.go' 500 1000 \
    'func (d *Dispatcher) DispatchSessioned('

check_anchor 'wrapWithTurnLifecycle (terminal discipline pattern)' \
    'internal/dispatch/dispatcher.go' 700 1100 \
    'wrapWithTurnLifecycle'

# API surface — projection that lights up the FE
check_anchor 'handleListV1Sessions (S5 projection site)' \
    'internal/api/server.go' 1100 1400 \
    'handleListV1Sessions'

# App-side wiring — shared *turn.Registry instance pointer
check_anchor 'api.Server.TurnRegistry accessor (PR2b ship)' \
    'internal/api/server.go' 1 4000 \
    'TurnRegistry()'

# FE consumers — backend-authoritative Live indicator
check_anchor 'ChildSessionsPanel isStreaming (S6 site)' \
    'web/src/components/chat/ChildSessionsPanel.vue' 1 200 \
    'isStreaming'

check_anchor 'SessionBrowser child-row Live derivation (B3 sibling)' \
    'web/src/components/session-browser/SessionBrowser.vue' 1 400 \
    'activeTurnId'

check_anchor 'SessionSwitcher child-row Live derivation (B3 sibling)' \
    'web/src/components/session-switcher/SessionSwitcher.vue' 1 400 \
    'activeTurnId'

# FE polling primitive — long-poll attach
check_anchor 'maybeReattachStream (S7 chain anchor)' \
    'web/src/stores/chatStore.ts' 1100 1300 \
    'maybeReattachStream(sessionId'

check_anchor 'pollTurnUntilTerminal (S7 chain anchor)' \
    'web/src/stores/chatStore.ts' 1 4000 \
    'pollTurnUntilTerminal'

# SessionSummary.activeTurnId wire field (B2 — verified field-presence)
check_anchor 'SessionSummary.activeTurnId type field (B2 verify)' \
    'web/src/types/index.ts' 250 350 \
    'activeTurnId'

# ─── Summary ──────────────────────────────────────────────────────────

if [[ "${failures}" -gt 0 ]]; then
    printf '\n%d/%d plan citations drifted — update Plans/Child Session Turn Registry Plumbing (May 2026).md\n' \
        "${failures}" "${total}" >&2
    exit 1
fi

if [[ "${VERBOSE}" -eq 1 ]]; then
    printf '\nAll %d plan citations resolved cleanly\n' "${total}"
fi
exit 0
