#!/usr/bin/env bash
# Reference autoresearch evaluator — CONTENT shape (Go benchmark
# wrapper).
#
# Per the April 2026 In-Memory Default plan (Slice 3), the canonical
# `bench.sh` reads the candidate from stdin (or
# FLOWSTATE_AUTORESEARCH_CANDIDATE_FILE), benchmarks against it, and
# prints one non-negative integer to stdout. The evaluator never
# reads the on-disk surface — the candidate IS the substrate the
# trial ratchets against.
#
# Operators wanting the legacy worktree-cwd behaviour (evaluator
# invoked from inside the trial worktree, reads files from disk)
# point `--evaluator-script` at `bench-commit.sh` (the pre-pivot
# script renamed at Slice 3 of the In-Memory Default plan) AND set
# `--commit-trials` on `flowstate autoresearch run`.
#
# ============================================================
# Contract
# ============================================================
#
# Stdin: the candidate string (full). The evaluator may also read
#   the same bytes from FLOWSTATE_AUTORESEARCH_CANDIDATE_FILE; both
#   channels are populated by the harness so authors choose by
#   convenience.
#
# Stdout: exactly one non-negative integer in decimal. Trailing
#   newline allowed; nothing else.
#
# Exit 0 on successful scoring; non-zero on evaluator-side failure
#   (the harness records `evaluator-contract-violation`).
#
# Stderr: free for diagnostic output.
#
# Env vars consumed:
#   FLOWSTATE_AUTORESEARCH_RUN_ID       — run identifier.
#   FLOWSTATE_AUTORESEARCH_CANDIDATE_FILE — path to a file holding
#       the same bytes as stdin. Use either channel.
#   FLOWSTATE_AUTORESEARCH_BENCH_PKG    — Go package to bench
#       (default: ./...).
#   FLOWSTATE_AUTORESEARCH_BENCH_NAME   — -bench filter regex
#       (default: .).
#   FLOWSTATE_AUTORESEARCH_BENCH_METRIC — ops_per_sec | ns_per_op
#       (default: ops_per_sec).
#   FLOWSTATE_AUTORESEARCH_BENCH_OUTPUT — path to a captured
#       `go test -bench` output. When set, the script SKIPS running
#       `go test` and parses the file directly. Used by hermetic
#       fixtures.
#   FLOWSTATE_AUTORESEARCH_BENCH_TIMEOUT — seconds passed to
#       `go test -timeout` (default: 60s).
#
# Time budget — the harness caps wall-clock via --evaluator-timeout
#   (default 5m); SIGTERM at deadline, SIGKILL 30s later.
#
# Working directory — the operator's invocation cwd. The harness no
#   longer creates a worktree in default mode; if the bench target
#   needs a tree-on-disk (the typical Go bench shape), the operator
#   stages it themselves OR uses --commit-trials with bench-commit.sh.
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

# ------------------------------------------------------------
# Drain stdin so the harness's pipe-writer side does not block.
# This evaluator does not key the score off the candidate body
# (Go benchmarks key off compiled binaries that operators rebuild
# elsewhere); the candidate IS available to operator-authored
# variants that want to inspect the SHA, parse the manifest, etc.
# ------------------------------------------------------------

if [ ! -t 0 ]; then
  cat > /dev/null
fi

# ------------------------------------------------------------
# Capture bench output. Fixture path bypasses the live
# `go test` invocation — hermetic test fixtures feed a captured file
# so the spec is provider-free.
# ------------------------------------------------------------

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
  bench_output=$(go test -run='^$' -bench="$bench_name" -benchmem -timeout "$bench_timeout" "$bench_pkg" 2>&1)
fi

# Parse ns/op out of the first BenchmarkXxx line.
ns_per_op=$(printf '%s\n' "$bench_output" | awk '
  /^Benchmark[A-Za-z0-9_]+/ {
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

ns_per_op_int=$(printf '%s' "$ns_per_op" | awk -F. '{ print $1 }')

if [ "$ns_per_op_int" -le 0 ] 2>/dev/null; then
  echo "bench.sh: parsed ns/op '$ns_per_op' is zero or negative" >&2
  exit 66
fi

case "$bench_metric" in
  ops_per_sec)
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

echo "$score"
