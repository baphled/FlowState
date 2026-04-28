#!/usr/bin/env bash
# Reference autoresearch evaluator — Go benchmark wrapper.
#
# Per plan v3.1 § 4.6 (Autoresearch Loop Integration, April 2026), an
# evaluator script must:
#
#   1. Stdout — exactly one non-negative integer in decimal. Trailing
#      newline allowed; nothing else on stdout.
#   2. Exit 0 on successful scoring; non-zero on evaluator-side
#      failure (the harness records `evaluator-contract-violation`).
#   3. Stderr — free for diagnostic output.
#   4. Working directory — invoked from the worktree root.
#   5. Environment —
#         FLOWSTATE_AGENT_DIR=<worktree>/internal/app/agents
#         FLOWSTATE_AUTORESEARCH_RUN_ID=<runID>
#         FLOWSTATE_AUTORESEARCH_SURFACE=<path-to-surface>
#         (this script also honours FLOWSTATE_AUTORESEARCH_BENCH_*
#         knobs documented below; either is fair game)
#   6. Time budget — capped by the harness via --evaluator-timeout
#      (default 5m); SIGTERM at deadline, SIGKILL 30s later.
#
# This reference wraps `go test -bench=<name> -run=^$ -benchmem <pkg>`,
# parses the `ns/op` value out of the first BenchmarkXxx line, and
# emits ops/sec (= 1_000_000_000 / ns_per_op) so it pairs with
# `--metric-direction max`. To pair with `--metric-direction min`,
# emit ns/op directly — flip a knob below.
#
# Operators are expected to copy this script into their own repo and
# adapt the BENCH_PKG / BENCH_NAME / BENCH_METRIC variables (or the
# parse step) for their surface. The contract is the same; only the
# benchmark target changes.
#
# Knobs (env, all optional):
#
#   FLOWSTATE_AUTORESEARCH_BENCH_PKG     Go package path to bench
#                                        (default: ./...).
#   FLOWSTATE_AUTORESEARCH_BENCH_NAME    -bench filter regex
#                                        (default: .).
#   FLOWSTATE_AUTORESEARCH_BENCH_METRIC  ops_per_sec | ns_per_op
#                                        (default: ops_per_sec; pair
#                                        with --metric-direction max).
#   FLOWSTATE_AUTORESEARCH_BENCH_OUTPUT  Path to a captured
#                                        `go test -bench` output. When
#                                        set, the script SKIPS running
#                                        `go test` and parses the file
#                                        directly. Used by the seam
#                                        spec under
#                                        internal/cli/testdata/.
#   FLOWSTATE_AUTORESEARCH_BENCH_TIMEOUT Seconds passed to
#                                        `go test -timeout`
#                                        (default: 60s).
#
# Exit codes:
#   0   stdout has the integer score.
#   64  required tooling missing (go, awk).
#   65  bench output produced no parseable ns/op line.
#   66  parsed ns/op was zero or negative (cannot derive ops/sec).
#   67  unrecognised FLOWSTATE_AUTORESEARCH_BENCH_METRIC.

set -eu

bench_pkg="${FLOWSTATE_AUTORESEARCH_BENCH_PKG:-./...}"
bench_name="${FLOWSTATE_AUTORESEARCH_BENCH_NAME:-.}"
bench_metric="${FLOWSTATE_AUTORESEARCH_BENCH_METRIC:-ops_per_sec}"
bench_timeout="${FLOWSTATE_AUTORESEARCH_BENCH_TIMEOUT:-60s}"
bench_output_fixture="${FLOWSTATE_AUTORESEARCH_BENCH_OUTPUT:-}"

# 1. Capture bench output. Fixture path bypasses the live
#    `go test` invocation — the seam test feeds a captured file so
#    the spec is hermetic.
if [ -n "$bench_output_fixture" ]; then
  if [ ! -f "$bench_output_fixture" ]; then
    echo "bench.sh: FLOWSTATE_AUTORESEARCH_BENCH_OUTPUT=$bench_output_fixture not found" >&2
    exit 65
  fi
  bench_output=$(cat "$bench_output_fixture")
else
  if ! command -v go >/dev/null 2>&1; then
    echo "bench.sh: 'go' not found on PATH" >&2
    exit 64
  fi
  if ! command -v awk >/dev/null 2>&1; then
    echo "bench.sh: 'awk' not found on PATH" >&2
    exit 64
  fi
  # `-run=^$` skips ordinary tests; `-benchmem` keeps the output
  # shape identical whether or not the operator wants allocation
  # counts (we only parse ns/op).
  bench_output=$(go test -run='^$' -bench="$bench_name" -benchmem -timeout "$bench_timeout" "$bench_pkg" 2>&1)
fi

# 2. Parse ns/op out of the first BenchmarkXxx line. Standard
#    `go test -bench` output format:
#
#       BenchmarkDemo-8   1000000   123 ns/op   0 B/op   0 allocs/op
#
#    Field 3 is the integer ns/op. Operators with custom benchmark
#    formats can swap the awk parse for `benchstat` or a similar
#    tool — emit one non-negative integer to stdout and the
#    contract is satisfied.
ns_per_op=$(printf '%s\n' "$bench_output" | awk '
  /^Benchmark[A-Za-z0-9_]+/ {
    # Walk the fields looking for "ns/op"; the integer immediately
    # before it is the ns/op value.
    for (i = 1; i <= NF; i++) {
      if ($i == "ns/op" && i > 1) {
        print $(i-1)
        exit
      }
    }
  }
')

if [ -z "$ns_per_op" ]; then
  echo "bench.sh: no ns/op line found in bench output" >&2
  printf '%s\n' "$bench_output" >&2
  exit 65
fi

# Strip a fractional component if `go test` emitted one (e.g.
# "12.34"); the contract is integer ops/sec or ns/op, so we round
# down to the nearest integer before dividing.
ns_per_op_int=$(printf '%s' "$ns_per_op" | awk -F. '{ print $1 }')

if [ "$ns_per_op_int" -le 0 ] 2>/dev/null; then
  echo "bench.sh: parsed ns/op '$ns_per_op' is zero or negative" >&2
  exit 66
fi

case "$bench_metric" in
  ops_per_sec)
    # 1_000_000_000 ns / ns_per_op = ops/sec.
    score=$(awk -v n="$ns_per_op_int" 'BEGIN { printf("%d", 1000000000 / n) }')
    ;;
  ns_per_op)
    score="$ns_per_op_int"
    ;;
  *)
    echo "bench.sh: unrecognised FLOWSTATE_AUTORESEARCH_BENCH_METRIC=$bench_metric" >&2
    exit 67
    ;;
esac

# 3. Emit one non-negative integer + trailing newline. That is the
#    entire stdout contract.
echo "$score"
