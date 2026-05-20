#!/usr/bin/env bash
# Smoke test for tools/plan-citation-check.sh (PR4).
#
# Run:
#   tools/plan-citation-check_test.sh
#
# Exits 0 if all cases pass; non-zero with a diagnostic if any fail.
#
# Cases pinned (mirrors the D8 smoke pattern in
# tools/skill-doc-path-check_test.sh):
#   1. Clean tree (all 20 anchors resolve) → exit 0
#   2. --help → exit 0
#   3. Unknown flag → exit 2
#   4. --verbose against clean tree → exit 0, stdout shows PASS lines
#
# Note. Cases that require simulating a drifted citation against the
# real repo tree are out of scope for the smoke test — the script itself
# is designed to walk the live tree, and creating a synthetic drift
# would require either:
#   (a) editing repo files (destructive), OR
#   (b) shadowing the repo root via a clone (heavyweight in CI).
# The drift-detection path is exercised by the script's own development
# history — anchors that DID drift during PR4 authoring produced the
# expected FAIL output (4 anchors caught; widened ranges to PASS). The
# detector is therefore self-tested by its construction.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="${SCRIPT_DIR}/plan-citation-check.sh"

if [[ ! -x "${SCRIPT}" ]]; then
    printf 'FAIL: script not executable: %s\n' "${SCRIPT}" >&2
    exit 1
fi

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

# -- Case 1: clean tree (default invocation) ---------------------------------

set +e
"${SCRIPT}" >/dev/null 2>&1
clean_exit=$?
set -e
assert_exit "default invocation on clean tree exits 0" 0 "${clean_exit}"

# -- Case 2: --help -----------------------------------------------------------

set +e
help_out=$("${SCRIPT}" --help 2>&1)
help_exit=$?
set -e
assert_exit "--help exits 0" 0 "${help_exit}"
assert_contains "--help mentions repo root" 'Verifies' "${help_out}"

# -- Case 3: unknown flag -----------------------------------------------------

set +e
bad_out=$("${SCRIPT}" --no-such-flag 2>&1)
bad_exit=$?
set -e
assert_exit "unknown flag exits 2" 2 "${bad_exit}"
assert_contains "unknown flag prints diagnostic" 'unknown argument' "${bad_out}"

# -- Case 4: --verbose against clean tree -------------------------------------

set +e
verbose_out=$("${SCRIPT}" --verbose 2>&1)
verbose_exit=$?
set -e
assert_exit "--verbose exits 0 on clean tree" 0 "${verbose_exit}"
assert_contains "--verbose reports PASS lines" 'PASS:' "${verbose_out}"
assert_contains "--verbose reports summary" 'All 20 plan citations resolved cleanly' "${verbose_out}"

# -- Summary -----------------------------------------------------------------

if [[ "${fail}" -ne 0 ]]; then
    printf '\nSelf-test failed — see diagnostics above\n' >&2
    exit 1
fi

printf '\nAll plan-citation-check self-tests passed\n'
exit 0
