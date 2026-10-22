#!/usr/bin/env bash
# Smoke test for tools/agent-todowrite-adherence.sh (D8).
#
# Run:
#   tools/agent-todowrite-adherence_test.sh
#
# Exits 0 if all cases pass; non-zero with a diagnostic if any fail.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="${SCRIPT_DIR}/agent-todowrite-adherence.sh"

if [[ ! -x "${SCRIPT}" ]]; then
    printf 'FAIL: script not executable: %s\n' "${SCRIPT}" >&2
    exit 1
fi

TMPDIR_REAL="$(mktemp -d)"
trap 'rm -rf "${TMPDIR_REAL}"' EXIT

fail=0

assert_eq() {
    local label="$1" expected="$2" actual="$3"
    if [[ "${expected}" != "${actual}" ]]; then
        printf 'FAIL: %s\n  expected: %s\n  actual:   %s\n' \
            "${label}" "${expected}" "${actual}" >&2
        fail=1
    else
        printf 'ok: %s\n' "${label}"
    fi
}

# -- Case 1: no log files ---------------------------------------------------

out="$("${SCRIPT}" --logs "${TMPDIR_REAL}/nonexistent.jsonl*" 2>&1)"
assert_eq "case 1 (no logs) emits canonical zero line" \
    "total=0 multistep=0 compliant=0 ratio=0.00" \
    "${out}"

# -- Case 2: one multi-step session WITH todowrite (compliant) --------------
#
# Three `tool` events for the same session; first is todowrite. Plus a
# `tool.reasoning` to attribute the agent.

cat > "${TMPDIR_REAL}/events.jsonl" <<'EOF'
{"type":"tool.reasoning","data":{"SessionID":"sess-A","AgentID":"executor","ToolName":"todowrite"}}
{"type":"tool","data":{"session_id":"sess-A","tool_name":"todowrite"}}
{"type":"tool","data":{"session_id":"sess-A","tool_name":"bash"}}
{"type":"tool","data":{"session_id":"sess-A","tool_name":"read"}}
EOF

out="$("${SCRIPT}" --logs "${TMPDIR_REAL}/events.jsonl*")"
assert_eq "case 2 (1 multi-step compliant) summary line" \
    "total=1 multistep=1 compliant=1 ratio=1.00" \
    "${out}"

# -- Case 3: one multi-step session WITHOUT todowrite (non-compliant) -------

cat > "${TMPDIR_REAL}/events.jsonl" <<'EOF'
{"type":"tool.reasoning","data":{"SessionID":"sess-B","AgentID":"Senior-Engineer","ToolName":"read"}}
{"type":"tool","data":{"session_id":"sess-B","tool_name":"read"}}
{"type":"tool","data":{"session_id":"sess-B","tool_name":"bash"}}
{"type":"tool","data":{"session_id":"sess-B","tool_name":"read"}}
EOF

out="$("${SCRIPT}" --logs "${TMPDIR_REAL}/events.jsonl*")"
assert_eq "case 3 (1 multi-step non-compliant) summary line" \
    "total=1 multistep=1 compliant=0 ratio=0.00" \
    "${out}"

# -- Case 4: one single-step session (skipped from multi-step ratio) --------

cat > "${TMPDIR_REAL}/events.jsonl" <<'EOF'
{"type":"tool","data":{"session_id":"sess-C","tool_name":"read"}}
{"type":"tool","data":{"session_id":"sess-C","tool_name":"bash"}}
EOF

out="$("${SCRIPT}" --logs "${TMPDIR_REAL}/events.jsonl*")"
assert_eq "case 4 (single-step session) summary line" \
    "total=1 multistep=0 compliant=0 ratio=0.00" \
    "${out}"

# -- Case 5: mixed — A compliant, B non-compliant, C single-step ------------

cat > "${TMPDIR_REAL}/events.jsonl" <<'EOF'
{"type":"tool.reasoning","data":{"SessionID":"sess-A","AgentID":"executor","ToolName":"todowrite"}}
{"type":"tool","data":{"session_id":"sess-A","tool_name":"todowrite"}}
{"type":"tool","data":{"session_id":"sess-A","tool_name":"bash"}}
{"type":"tool","data":{"session_id":"sess-A","tool_name":"read"}}
{"type":"tool.reasoning","data":{"SessionID":"sess-B","AgentID":"researcher","ToolName":"read"}}
{"type":"tool","data":{"session_id":"sess-B","tool_name":"read"}}
{"type":"tool","data":{"session_id":"sess-B","tool_name":"bash"}}
{"type":"tool","data":{"session_id":"sess-B","tool_name":"read"}}
{"type":"tool.reasoning","data":{"SessionID":"sess-C","AgentID":"writer","ToolName":"read"}}
{"type":"tool","data":{"session_id":"sess-C","tool_name":"read"}}
EOF

out="$("${SCRIPT}" --logs "${TMPDIR_REAL}/events.jsonl*" | head -1)"
assert_eq "case 5 (mixed) summary line" \
    "total=3 multistep=2 compliant=1 ratio=0.50" \
    "${out}"

# -- Case 6: --per-agent breakdown (uses case-5 fixture) --------------------

full="$("${SCRIPT}" --per-agent --logs "${TMPDIR_REAL}/events.jsonl*")"
expected_full="total=3 multistep=2 compliant=1 ratio=0.50
agent=executor multistep=1 compliant=1 ratio=1.00
agent=researcher multistep=1 compliant=0 ratio=0.00"
assert_eq "case 6 (--per-agent) full output" "${expected_full}" "${full}"

# -- Case 7: malformed JSON line is tolerated -------------------------------

cat > "${TMPDIR_REAL}/events.jsonl" <<'EOF'
{"type":"tool","data":{"session_id":"sess-A","tool_name":"todowrite"}}
this is not json
{"type":"tool","data":{"session_id":"sess-A","tool_name":"bash"}}
{"type":"tool","data":{"session_id":"sess-A","tool_name":"read"}}
EOF

out="$("${SCRIPT}" --logs "${TMPDIR_REAL}/events.jsonl*")"
assert_eq "case 7 (malformed lines skipped) summary line" \
    "total=1 multistep=1 compliant=1 ratio=1.00" \
    "${out}"

# -- Case 8: idempotent — re-running produces identical output --------------

cat > "${TMPDIR_REAL}/events.jsonl" <<'EOF'
{"type":"tool","data":{"session_id":"sess-X","tool_name":"todowrite"}}
{"type":"tool","data":{"session_id":"sess-X","tool_name":"bash"}}
{"type":"tool","data":{"session_id":"sess-X","tool_name":"read"}}
EOF

out1="$("${SCRIPT}" --logs "${TMPDIR_REAL}/events.jsonl*")"
out2="$("${SCRIPT}" --logs "${TMPDIR_REAL}/events.jsonl*")"
assert_eq "case 8 (idempotent re-run)" "${out1}" "${out2}"

# -- Case 9: rotated siblings — events.jsonl + events.jsonl.1 ---------------

cat > "${TMPDIR_REAL}/events.jsonl" <<'EOF'
{"type":"tool","data":{"session_id":"sess-A","tool_name":"todowrite"}}
{"type":"tool","data":{"session_id":"sess-A","tool_name":"bash"}}
EOF
cat > "${TMPDIR_REAL}/events.jsonl.1" <<'EOF'
{"type":"tool","data":{"session_id":"sess-A","tool_name":"read"}}
EOF

out="$("${SCRIPT}" --logs "${TMPDIR_REAL}/events.jsonl*")"
assert_eq "case 9 (rotated siblings folded into same session)" \
    "total=1 multistep=1 compliant=1 ratio=1.00" \
    "${out}"

# -- summary ----------------------------------------------------------------

if [[ ${fail} -ne 0 ]]; then
    printf '\nSMOKE FAIL\n' >&2
    exit 1
fi
printf '\nall smoke cases pass\n'
