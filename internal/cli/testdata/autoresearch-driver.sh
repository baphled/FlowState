#!/usr/bin/env bash
# Deterministic candidate driver for autoresearch loop smoke runs.
#
# The script is invoked once per trial by the autoresearch harness.
# It reads the trial counter from $DATA_DIR/trial-counter (defaults
# to 0), increments it, and copies $DATA_DIR/candidate-N over the
# surface file at $FLOWSTATE_AUTORESEARCH_SURFACE. When no candidate
# file exists for the current trial, the script exits cleanly without
# touching the surface — leaving the driver as a no-op so the harness
# can exercise its fixed-point branch without bespoke wiring.
#
# Contract — required env:
#   DATA_DIR                       directory holding fixture state
#   FLOWSTATE_AUTORESEARCH_SURFACE absolute path to the surface file
#
# Files read under DATA_DIR:
#   trial-counter   integer; created if absent
#   candidate-<N>   replacement body for trial N (optional)
#
# Side effects:
#   - Writes the new trial counter back to trial-counter.
#   - Overwrites the surface file when a candidate-N exists.

set -eu

if [ -z "${DATA_DIR:-}" ]; then
  echo "autoresearch-driver: DATA_DIR not set" >&2
  exit 64
fi
if [ -z "${FLOWSTATE_AUTORESEARCH_SURFACE:-}" ]; then
  echo "autoresearch-driver: FLOWSTATE_AUTORESEARCH_SURFACE not set" >&2
  exit 64
fi

trial_file="$DATA_DIR/trial-counter"
n=$(cat "$trial_file" 2>/dev/null || echo 0)
n=$((n + 1))
echo "$n" > "$trial_file"

candidate="$DATA_DIR/candidate-$n"
if [ -f "$candidate" ]; then
  cp "$candidate" "$FLOWSTATE_AUTORESEARCH_SURFACE"
fi

exit 0
