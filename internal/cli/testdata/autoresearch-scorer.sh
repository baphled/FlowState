#!/usr/bin/env bash
# Deterministic scorer for autoresearch loop smoke runs.
#
# The script is invoked twice per trial cycle: once at run start to
# score the baseline, then once per trial after the candidate edit.
# It reads the trial counter from $DATA_DIR/trial-counter (the same
# counter the driver script maintains). When the counter is 0 the
# script emits the baseline score from $DATA_DIR/baseline-score
# (defaults 0). Otherwise it emits the Nth line of the score sequence
# in $DATA_DIR/score-sequence.
#
# Contract — required env:
#   DATA_DIR  directory holding fixture state
#
# Files read under DATA_DIR:
#   trial-counter    integer; defaults 0 when absent
#   baseline-score   single integer used for the baseline call
#   score-sequence   newline-separated integers, one per trial
#
# Output:
#   stdout — exactly one non-negative integer + trailing newline.
#   exit 0 on success, non-zero on missing fixture state.

set -eu

if [ -z "${DATA_DIR:-}" ]; then
  echo "autoresearch-scorer: DATA_DIR not set" >&2
  exit 64
fi

trial_file="$DATA_DIR/trial-counter"
n=$(cat "$trial_file" 2>/dev/null || echo 0)

if [ "$n" -le 0 ]; then
  if [ -f "$DATA_DIR/baseline-score" ]; then
    cat "$DATA_DIR/baseline-score"
  else
    echo 0
  fi
  exit 0
fi

if [ ! -f "$DATA_DIR/score-sequence" ]; then
  echo "autoresearch-scorer: missing $DATA_DIR/score-sequence" >&2
  exit 65
fi

score=$(sed -n "${n}p" "$DATA_DIR/score-sequence")
if [ -z "$score" ]; then
  echo "autoresearch-scorer: no score for trial $n in score-sequence" >&2
  exit 66
fi
echo "$score"
