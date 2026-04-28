#!/bin/bash
# Sanity tests for scripts/validate-harness.sh --score — pins the
# autoresearch evaluator contract introduced in Slice 0d of the
# Autoresearch Loop Integration plan.
#
# The --score flag MUST:
#   - Emit exactly one non-negative integer on stdout, nothing else.
#   - Route all human-readable diagnostics (turns, tool_calls,
#     WARNING: lines, skip lines) to stderr.
#   - Sum WARNING: counts across `--all` and emit the total.
#   - Exit 0 on successful scoring.
#   - Leave non-`--score` invocations entirely unchanged in shape.
#
# Tests stub validate_one with synthetic warning streams so the suite
# stays independent of the live flowstate binary, z.ai auth, and the
# operator's coord-store. The fixture stub is sourced into a copy of
# validate-harness.sh so the real main() runs end-to-end.

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TARGET="${SCRIPT_DIR}/validate-harness.sh"

if [ ! -x "$TARGET" ]; then
    echo "FAIL: ${TARGET} missing or not executable"
    exit 1
fi

PASS=0
FAIL=0

assert_eq() {
    local label="$1" expected="$2" got="$3"
    if [ "$expected" = "$got" ]; then
        echo "PASS: ${label}"
        PASS=$((PASS + 1))
    else
        echo "FAIL: ${label} (want=${expected} got=${got})"
        FAIL=$((FAIL + 1))
    fi
}

# Build a stubbed validator that replaces validate_one with a deterministic
# warning emitter keyed by agent name. The stub keeps the rest of the
# script intact so the score-mode plumbing in main() is exercised verbatim.
# Warning counts are read from $AGENT_WARNINGS_FILE (a simple "agent count"
# table) so they survive bash -c subshells reliably.
#
# Implementation note: the stub is concatenated into a separate file then
# inserted via a marker-line replacement. Using shell-only string ops
# avoids awk-inside-awk escape hazards.
make_stub() {
    local out="$1"
    local stub_body
    stub_body=$(cat <<'STUB'
# Stub validate_one — emits a fixed number of WARNING: lines per agent.
# Counts come from $AGENT_WARNINGS_FILE which the test rig writes.
validate_one() {
    local agent="$1"
    echo "== harness-validate: $agent =="
    local n=0
    if [ -n "${AGENT_WARNINGS_FILE:-}" ] && [ -f "$AGENT_WARNINGS_FILE" ]; then
        while read -r a count; do
            if [ "$a" = "$agent" ]; then n="$count"; break; fi
        done <"$AGENT_WARNINGS_FILE"
    fi
    local i=0
    while [ "$i" -lt "$n" ]; do
        echo "WARNING: synthetic warning $((i+1)) for $agent"
        i=$((i+1))
    done
    echo
}
STUB
)
    # Split TARGET into the slice before main() and from main() onward,
    # then concatenate with the stub between them. Avoids awk escape pain.
    local before_file="$WORKDIR/before.sh"
    local after_file="$WORKDIR/after.sh"
    awk '/^main\(\) \{/ { found=1 } { if (!found) print > "/dev/stdout"; else print > "/dev/stderr" }' \
        "$TARGET" >"$before_file" 2>"$after_file"
    {
        cat "$before_file"
        printf '\n%s\n\n' "$stub_body"
        cat "$after_file"
    } >"$out"
    chmod +x "$out"
}

# Set up a temp dir mirroring the real repo layout: the stubbed script
# lives at $WORKDIR/scripts/validate-harness.sh so its REPO_ROOT
# resolution (which is `cd $SCRIPT_DIR/..`) lands on $WORKDIR. Symlink
# the agents and prompts dirs back to the real repo so resolve_agent_name
# and resolve_prompt_file see live files.
WORKDIR=$(mktemp -d)
trap 'rm -rf "$WORKDIR"' EXIT

mkdir -p "$WORKDIR/scripts" "$WORKDIR/internal/app"
ln -s "$SCRIPT_DIR/../internal/app/agents" "$WORKDIR/internal/app/agents"
ln -s "$SCRIPT_DIR/harness-prompts" "$WORKDIR/scripts/harness-prompts"

STUBBED_SCRIPT="$WORKDIR/scripts/validate-harness.sh"
make_stub "$STUBBED_SCRIPT"

# Force coord-store + sessions dir into the temp tree so the test never
# touches the operator's data dir.
export XDG_DATA_HOME="$WORKDIR/data"
export FLOWSTATE_SESSIONS_DIR="$WORKDIR/data/flowstate/sessions"
export FLOWSTATE_DATA_DIR="$WORKDIR/data/flowstate"

# Force a flowstate binary path that exists so the early `[[ -x ]]` check
# passes; the stubbed validate_one never invokes it.
export FLOWSTATE_BIN="$(command -v bash)"

# Synthetic warning counts the stub will emit per agent. Written to a
# table file so bash -c subshells can read them deterministically.
AGENT_WARNINGS_FILE="$WORKDIR/agent_warnings.tsv"
cat >"$AGENT_WARNINGS_FILE" <<'TSV'
planner 2
plan-writer 1
plan-reviewer 3
executor 0
tech-lead 1
TSV
export AGENT_WARNINGS_FILE

# -------------------------------------------------------------------
# Case 1: --score --all emits one integer (sum) on stdout.
#
# Note: `--all` iterates LITERAL lowercase names per plan § 5.3
# ("default --all loop is unchanged"). The lowercase `tech-lead.md`
# manifest does not exist on disk (the manifest is PascalCase
# `Tech-Lead.md`), so the --all loop skips it — its warning count is
# excluded from the expected total. The case-insensitive resolver
# from Slice 0c only fires for explicit-name invocations.
# -------------------------------------------------------------------
EXPECTED_TOTAL=$((2 + 1 + 3 + 0))
STDOUT_FILE=$(mktemp)
STDERR_FILE=$(mktemp)
bash -c "\"$STUBBED_SCRIPT\" --score --all" \
    >"$STDOUT_FILE" 2>"$STDERR_FILE"
RC=$?

STDOUT=$(<"$STDOUT_FILE")
assert_eq "case1: --score --all stdout is single integer" "$EXPECTED_TOTAL" "$STDOUT"
assert_eq "case1: --score --all exit code 0"             "0"               "$RC"

# Stderr MUST contain the human-readable diagnostics.
if grep -q '^== harness-validate:' "$STDERR_FILE"; then
    echo "PASS: case1: human diagnostics routed to stderr"
    PASS=$((PASS + 1))
else
    echo "FAIL: case1: stderr missing harness-validate header"
    FAIL=$((FAIL + 1))
fi

# Stdout MUST NOT contain WARNING: lines (those belong on stderr in score mode).
if grep -q '^WARNING:' "$STDOUT_FILE"; then
    echo "FAIL: case1: stdout leaked WARNING: line"
    FAIL=$((FAIL + 1))
else
    echo "PASS: case1: stdout free of WARNING: lines"
    PASS=$((PASS + 1))
fi

rm -f "$STDOUT_FILE" "$STDERR_FILE"

# -------------------------------------------------------------------
# Case 2: --score <agent> emits the per-agent count.
# -------------------------------------------------------------------
STDOUT_FILE=$(mktemp)
STDERR_FILE=$(mktemp)
bash -c "\"$STUBBED_SCRIPT\" --score plan-reviewer" \
    >"$STDOUT_FILE" 2>"$STDERR_FILE"
RC=$?
STDOUT=$(<"$STDOUT_FILE")
assert_eq "case2: --score plan-reviewer stdout is per-agent count" "3"   "$STDOUT"
assert_eq "case2: --score plan-reviewer exit code 0"               "0"   "$RC"
rm -f "$STDOUT_FILE" "$STDERR_FILE"

# -------------------------------------------------------------------
# Case 3: zero warnings yields integer 0, not blank, not negative.
# -------------------------------------------------------------------
STDOUT_FILE=$(mktemp)
STDERR_FILE=$(mktemp)
bash -c "\"$STUBBED_SCRIPT\" --score executor" \
    >"$STDOUT_FILE" 2>"$STDERR_FILE"
RC=$?
STDOUT=$(<"$STDOUT_FILE")
assert_eq "case3: --score executor (0 warnings) stdout is '0'" "0"  "$STDOUT"
assert_eq "case3: --score executor exit code 0"                "0"  "$RC"
rm -f "$STDOUT_FILE" "$STDERR_FILE"

# -------------------------------------------------------------------
# Case 4: non-`--score` invocation is unchanged (stdout has the
# human-readable header, no trailing integer).
# -------------------------------------------------------------------
STDOUT_FILE=$(mktemp)
STDERR_FILE=$(mktemp)
bash -c "\"$STUBBED_SCRIPT\" planner" \
    >"$STDOUT_FILE" 2>"$STDERR_FILE"
RC=$?
if grep -q '^== harness-validate: planner' "$STDOUT_FILE"; then
    echo "PASS: case4: legacy invocation keeps human-readable stdout"
    PASS=$((PASS + 1))
else
    echo "FAIL: case4: legacy invocation lost its stdout header"
    FAIL=$((FAIL + 1))
fi
LAST_LINE=$(tail -n1 "$STDOUT_FILE" | tr -d '[:space:]')
if [[ "$LAST_LINE" =~ ^[0-9]+$ ]]; then
    echo "FAIL: case4: legacy invocation leaked a trailing integer"
    FAIL=$((FAIL + 1))
else
    echo "PASS: case4: legacy invocation has no trailing integer"
    PASS=$((PASS + 1))
fi
rm -f "$STDOUT_FILE" "$STDERR_FILE"

# -------------------------------------------------------------------
# Case 5: --score --all stdout has exactly one trailing newline-terminated line.
# Confirms evaluator-contract shape: integer + newline, nothing else.
# -------------------------------------------------------------------
STDOUT_FILE=$(mktemp)
bash -c "\"$STUBBED_SCRIPT\" --score --all" \
    >"$STDOUT_FILE" 2>/dev/null
LINE_COUNT=$(wc -l <"$STDOUT_FILE" | tr -d '[:space:]')
assert_eq "case5: --score --all stdout is exactly 1 line" "1" "$LINE_COUNT"
rm -f "$STDOUT_FILE"

echo
echo "Summary: ${PASS} passed, ${FAIL} failed"
[ "$FAIL" -eq 0 ]
