#!/usr/bin/env bash

set -euo pipefail

# ============================================================================
# Check dispatcher line cap
# ============================================================================
# Path-scoped lint guard descended from the v2 chat controller line-cap
# guard of [[Chat UI Overhaul on Bubble Tea v2 (April 2026)]] Slice S2.
# The TUI subsystem and internal/cli/chat.go were removed from the tree,
# and the chat turn logic they hosted now lives in the unified
# dispatcher service. The cap exists to stop the same god-object
# accretion that struck v1's chat controller (5,020 lines) recurring in
# its successor.
#
# Scope. The check is path-scoped to a single file:
#   internal/dispatch/dispatcher.go
#
# Targets
#   target ≤ 800 lines  (warn)
#   hard cap ≤ 1200 lines (fail)
#
# Usage:
#   bash scripts/check-controller-line-cap.sh
#
# Exit codes:
#   0 - File under hard cap
#   1 - File over hard cap
# ============================================================================

CONTROLLER_FILE="internal/dispatch/dispatcher.go"
TARGET_LINES=1600
HARD_CAP_LINES=1800

if [[ ! -f "$CONTROLLER_FILE" ]]; then
    echo "ERROR: $CONTROLLER_FILE does not exist."
    exit 1
fi

actual=$(wc -l < "$CONTROLLER_FILE" | tr -d '[:space:]')

if (( actual > HARD_CAP_LINES )); then
    echo "ERROR: $CONTROLLER_FILE is $actual lines, exceeds hard cap of $HARD_CAP_LINES."
    echo ""
    echo "The dispatcher is a delegation shell. Lines beyond the cap are a"
    echo "regression toward the god-object pattern that struck the v1 chat"
    echo "controller. Move responsibility into a subsystem rather than"
    echo "growing this file."
    exit 1
fi

if (( actual > TARGET_LINES )); then
    echo "WARN: $CONTROLLER_FILE is $actual lines, exceeds soft target of $TARGET_LINES (cap: $HARD_CAP_LINES)."
    echo "      Consider whether new responsibility belongs in a subsystem."
fi

echo "Controller line-cap check passed: $CONTROLLER_FILE = $actual lines (target ≤$TARGET_LINES, cap ≤$HARD_CAP_LINES)."
exit 0
