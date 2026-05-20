#!/usr/bin/env bash
# Smoke test for tools/skill-doc-path-check.sh (PR6/C3).
#
# Run:
#   tools/skill-doc-path-check_test.sh
#
# Exits 0 if all cases pass; non-zero with a diagnostic if any fail.
#
# Cases pinned (matching the D8 smoke pattern in
# tools/agent-todowrite-adherence_test.sh):
#   1. Clean SKILL.md fixture → exit 0
#   2. Stale-path SKILL.md fixture → exit 1, diagnostic mentions stale path
#   3. Stale-path fixture + --allow-stale-cache-path → exit 0
#   4. Missing SKILL.md path → exit 2
#   5. --help → exit 0
#   6. Unknown flag → exit 2
#   7. --skill-md without argument → exit 2

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="${SCRIPT_DIR}/skill-doc-path-check.sh"

if [[ ! -x "${SCRIPT}" ]]; then
    printf 'FAIL: script not executable: %s\n' "${SCRIPT}" >&2
    exit 1
fi

TMPDIR_REAL="$(mktemp -d)"
trap 'rm -rf "${TMPDIR_REAL}"' EXIT

fail=0

assert_exit() {
    local label="$1" expected="$2" actual="$3"
    if [[ "${expected}" != "${actual}" ]]; then
        printf 'FAIL: %s\n  expected exit: %s\n  actual exit:   %s\n' \
            "${label}" "${expected}" "${actual}" >&2
        fail=1
    else
        printf 'ok: %s\n' "${label}"
    fi
}

assert_contains() {
    local label="$1" needle="$2" haystack="$3"
    if [[ "${haystack}" != *"${needle}"* ]]; then
        printf 'FAIL: %s\n  needle:   %s\n  haystack: %s\n' \
            "${label}" "${needle}" "${haystack}" >&2
        fail=1
    else
        printf 'ok: %s\n' "${label}"
    fi
}

# -- Case 1: clean SKILL.md ------------------------------------------------

CLEAN="${TMPDIR_REAL}/clean-SKILL.md"
cat > "${CLEAN}" <<'EOF'
# Skill: flowstate-session-reader

Session recordings live at ~/.local/share/flowstate/sessions/recordings/{uuid}.jsonl.

## Gotchas

- Abbreviated IDs: use full UUIDs from filenames in ~/.local/share/flowstate/sessions/recordings/.
EOF

set +e
out="$("${SCRIPT}" --skill-md "${CLEAN}" 2>&1)"
rc=$?
set -e
assert_exit "case 1 (clean fixture) exits 0" "0" "${rc}"
assert_contains "case 1 (clean fixture) reports ok" "ok" "${out}"

# -- Case 2: stale path present (default fails) ----------------------------

STALE="${TMPDIR_REAL}/stale-SKILL.md"
cat > "${STALE}" <<'EOF'
# Skill: flowstate-session-reader

Session recordings live at ~/.local/share/flowstate/sessions/recordings/{uuid}.jsonl.

## Gotchas

- Abbreviated IDs: use full UUIDs from filenames in ~/.cache/flowstate/session-recordings/.
EOF

set +e
out="$("${SCRIPT}" --skill-md "${STALE}" 2>&1)"
rc=$?
set -e
assert_exit "case 2 (stale fixture) exits 1" "1" "${rc}"
assert_contains "case 2 (stale fixture) diagnostic names the stale path" \
    "~/.cache/flowstate/session-recordings" \
    "${out}"
assert_contains "case 2 (stale fixture) suggests fix" "fb1e1fd8" "${out}"

# -- Case 3: stale path present + opt-out flag -----------------------------

set +e
out="$("${SCRIPT}" --skill-md "${STALE}" --allow-stale-cache-path 2>&1)"
rc=$?
set -e
assert_exit "case 3 (stale + opt-out) exits 0" "0" "${rc}"
assert_contains "case 3 (stale + opt-out) emits WARN" "WARN" "${out}"

# -- Case 4: missing file --------------------------------------------------

set +e
out="$("${SCRIPT}" --skill-md "${TMPDIR_REAL}/does-not-exist.md" 2>&1)"
rc=$?
set -e
assert_exit "case 4 (missing file) exits 2" "2" "${rc}"
assert_contains "case 4 (missing file) diagnostic mentions cannot read" \
    "cannot read" \
    "${out}"

# -- Case 5: --help --------------------------------------------------------

set +e
out="$("${SCRIPT}" --help 2>&1)"
rc=$?
set -e
assert_exit "case 5 (--help) exits 0" "0" "${rc}"
assert_contains "case 5 (--help) prints usage line" "drift detector" "${out}"

# -- Case 6: unknown flag --------------------------------------------------

set +e
out="$("${SCRIPT}" --not-a-real-flag 2>&1)"
rc=$?
set -e
assert_exit "case 6 (unknown flag) exits 2" "2" "${rc}"
assert_contains "case 6 (unknown flag) diagnostic" "unknown argument" "${out}"

# -- Case 7: --skill-md without argument -----------------------------------

set +e
out="$("${SCRIPT}" --skill-md 2>&1)"
rc=$?
set -e
assert_exit "case 7 (--skill-md missing argument) exits 2" "2" "${rc}"
assert_contains "case 7 (--skill-md missing argument) diagnostic" \
    "requires an argument" \
    "${out}"

# -- summary ---------------------------------------------------------------

if [[ ${fail} -ne 0 ]]; then
    printf '\nSMOKE FAIL\n' >&2
    exit 1
fi
printf '\nall smoke cases pass\n'
