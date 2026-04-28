#!/usr/bin/env bash
# Reference autoresearch evaluator — planner manifest warning-count wrapper.
#
# Per plan v3.1 § 4.6 (Autoresearch Loop Integration, April 2026), an
# evaluator script must satisfy six rules. This wrapper makes the
# canonical Example A shape — ratchet a planner-class manifest
# against the harness validator's warning count — invokable as a
# single autoresearch evaluator argument. It is a thin shim around
# `scripts/validate-harness.sh --score --all`.
#
# Why this wrapper exists. The autoresearch loop invokes
# `--evaluator-script <path>` as a bare subprocess with no arguments
# (see `runEvaluatorScript` in `internal/cli/autoresearch_loop.go`).
# `validate-harness.sh` without arguments dies with a usage message.
# Operators driving Example A (`--surface internal/app/agents/
# planner.md`, `--metric-direction min`) should point
# `--evaluator-script` at THIS wrapper; the wrapper applies the
# `--score --all` flags the loop cannot itself pass.
#
# Stdout: one non-negative integer (sum of WARNING: lines across the
#   five planner-loop agents). Lower is better (`--metric-direction min`).
# Exit 0 on success; non-zero only on CLI/session I/O failure (mirrors
#   `validate-harness.sh --score --all`'s exit semantics).
# Stderr: human-readable diagnostic output from validate-harness.sh,
#   captured and logged by the autoresearch harness.
# Working directory: invoked from the worktree root by the autoresearch
#   harness; we resolve `validate-harness.sh` relative to this script's
#   location to stay correct under any cwd.

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &>/dev/null && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/../.." &>/dev/null && pwd)"

VALIDATOR="$REPO_ROOT/scripts/validate-harness.sh"
[[ -x "$VALIDATOR" ]] || {
  echo "error: validator not found or not executable: $VALIDATOR" >&2
  exit 2
}

# `exec` so the validator's exit code propagates directly. The validator
# already emits one integer to stdout in `--score` mode (line 257) and
# routes human-readable noise to stderr, so the autoresearch evaluator
# contract is satisfied without any further plumbing here.
exec "$VALIDATOR" --score --all
