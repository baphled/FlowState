#!/usr/bin/env bash
# PR6/C3: drift detector for the flowstate-session-reader SKILL.md path
# references.
#
# Background. The session recording path moved from
# `~/.cache/flowstate/session-recordings/` to
# `~/.local/share/flowstate/sessions/recordings/{uuid}.jsonl` in commit
# `fb1e1fd8`. PR3 corrected the canonical reference at SKILL.md:33 but
# left a stale citation in the "Gotchas" section (line 135) untouched.
# Readers cross-referencing the two sections saw an internal
# contradiction. PR6/C3 closes that gap and ships this detector so a
# future copy-paste of legacy text cannot silently re-introduce the
# inconsistency.
#
# What it does. Greps the in-vault SKILL.md (under ~/.claude/skills/) for
# the literal substring `~/.cache/flowstate/session-recordings` and exits
# non-zero with a diagnostic if any match is found. The literal is
# deliberately the OLD path so a positive match is unambiguously a
# regression; the NEW path is not asserted directly because future
# refactors may legitimately re-organise the canonical location and the
# detector should not lock paths to today's layout.
#
# Usage:
#   tools/skill-doc-path-check.sh                       # default scope
#   tools/skill-doc-path-check.sh --skill-md <path>     # custom path
#   tools/skill-doc-path-check.sh --allow-stale-cache-path  # opt-out
#   tools/skill-doc-path-check.sh --help
#
# Exit codes:
#   0   no stale path references found (or --allow-stale-cache-path was
#       passed and at least one warning was emitted)
#   1   stale path references found and the script was NOT given the
#       opt-out flag — calling CI should treat this as a hard failure
#   2   bad invocation (unknown flag, missing argument, target file not
#       readable)
#
# Required tools: bash, grep, awk. No jq.
#
# Self-test: tools/skill-doc-path-check_test.sh ships fixture-driven
# coverage of all four cases (clean / stale / opt-out / missing file).

set -euo pipefail

SKILL_MD_DEFAULT="${HOME}/.claude/skills/flowstate-session-reader/SKILL.md"
SKILL_MD="${SKILL_MD_DEFAULT}"
ALLOW_STALE=0

# STALE_PATH is the literal substring the detector hunts for. The path
# corresponds to the pre-fb1e1fd8 layout that was retired but lingered in
# the SKILL.md gotchas section. Updating this constant is the right move
# only when a later refactor INTENTIONALLY reintroduces the cache path as
# a documented historical reference — and even then the opt-out flag is
# the documented escape hatch, not constant editing.
readonly STALE_PATH='~/.cache/flowstate/session-recordings'

print_usage() {
    sed -n '2,40p' "$0"
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --skill-md)
            if [[ $# -lt 2 ]]; then
                printf 'skill-doc-path-check: --skill-md requires an argument\n' >&2
                exit 2
            fi
            SKILL_MD="$2"
            shift 2
            ;;
        --allow-stale-cache-path)
            ALLOW_STALE=1
            shift
            ;;
        --help|-h)
            print_usage
            exit 0
            ;;
        *)
            printf 'skill-doc-path-check: unknown argument: %s\n' "$1" >&2
            exit 2
            ;;
    esac
done

if [[ ! -r "${SKILL_MD}" ]]; then
    printf 'skill-doc-path-check: cannot read SKILL.md at %s\n' "${SKILL_MD}" >&2
    exit 2
fi

# grep -F treats the needle as a literal string (no regex backtracking
# surprises on the leading `~/`). -n includes line numbers for the
# diagnostic. Use grep's documented exit code: 0 = match, 1 = no match,
# >=2 = error. Trap the error case so the script does not abort here.
set +e
matches="$(grep -nF "${STALE_PATH}" "${SKILL_MD}")"
grep_exit=$?
set -e

if [[ ${grep_exit} -ge 2 ]]; then
    printf 'skill-doc-path-check: grep failed with exit %d on %s\n' \
        "${grep_exit}" "${SKILL_MD}" >&2
    exit 2
fi

if [[ -z "${matches}" ]]; then
    # No stale references — the desired post-fix state.
    printf 'skill-doc-path-check: ok — no stale `%s` references in %s\n' \
        "${STALE_PATH}" "${SKILL_MD}"
    exit 0
fi

# Found stale references. Decide whether to fail or warn.
if [[ ${ALLOW_STALE} -eq 1 ]]; then
    printf 'skill-doc-path-check: WARN — stale `%s` references present (allowed by --allow-stale-cache-path):\n' \
        "${STALE_PATH}" >&2
    printf '%s\n' "${matches}" >&2
    exit 0
fi

printf 'skill-doc-path-check: FAIL — stale `%s` references found in %s\n' \
    "${STALE_PATH}" "${SKILL_MD}" >&2
printf '%s\n' "${matches}" >&2
printf '\n' >&2
printf 'The canonical session-recording path moved to ~/.local/share/flowstate/sessions/recordings/ in commit fb1e1fd8.\n' >&2
printf 'Fix the citations or pass --allow-stale-cache-path if the reference is intentional historical context.\n' >&2
exit 1
