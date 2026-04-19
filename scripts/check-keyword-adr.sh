#!/bin/bash
# Guard 1 of the review-pattern guards (B1).
#
# Fails when added lines in internal/**/*.go (excluding *_test.go) contain
# any of the high-risk policy keywords:
#
#   unconditionally  always  bypass
#
# unless an ADR file is also added in the same diff under
# Documentation/Architecture/ADR/ (the vault path).
#
# Why: commit b960869 ("wire MCP tools to bypass manifest whitelist")
# stripped the manifest gate and changed the docstring to say MCP tools
# "unconditionally bypass manifest filtering" — landed by an
# author-as-only-reviewer with the test pinned to the new (broken)
# behaviour. This guard makes any future change of that shape impossible
# to land without an explicit ADR record forcing reviewer attention.
#
# Usage (production, runs as a make check sub-target):
#   scripts/check-keyword-adr.sh
#       diffs the staged changes against HEAD.
#
# Usage (tests):
#   echo "<diff body>" | scripts/check-keyword-adr.sh --stdin
#       reads a unified diff from stdin; same evaluation rules.

set -e

KEYWORDS_REGEX='\b(unconditionally|always|bypass)\b'
ADR_PATH_PREFIX='Documentation/Architecture/ADR/'

read_diff() {
    if [ "${1:-}" = "--stdin" ]; then
        cat
    else
        git diff --cached --unified=0
    fi
}

DIFF=$(read_diff "${1:-}")

if [ -z "$DIFF" ]; then
    exit 0
fi

# Walk the diff line-by-line. Track the current target file from the
# +++ header so we can decide whether each + line is in scope.
current_file=""
flagged_files=()
adr_added=0

while IFS= read -r line; do
    # +++ b/path/to/file
    if [[ "$line" =~ ^\+\+\+\ b/(.+)$ ]]; then
        current_file="${BASH_REMATCH[1]}"
        # Detect ADR added in same diff. The diff header --- /dev/null
        # signals a new file but we don't strictly need that: any new or
        # modified ADR file under the prefix is enough. The pattern fix
        # of choice for an unconditionally/always/bypass keyword is to
        # land the ADR alongside.
        if [[ "$current_file" == "${ADR_PATH_PREFIX}"* ]]; then
            adr_added=1
        fi
        continue
    fi

    # Skip diff metadata; only inspect added lines.
    [[ "$line" =~ ^\+\+\+ ]] && continue
    [[ "$line" =~ ^---  ]] && continue
    [[ ! "$line" =~ ^\+ ]] && continue

    # Scope: internal/**/*.go, excluding *_test.go.
    if [[ "$current_file" != internal/* ]]; then
        continue
    fi
    if [[ "$current_file" != *.go ]]; then
        continue
    fi
    if [[ "$current_file" == *_test.go ]]; then
        continue
    fi

    # Strip the leading + then test for any flagged keyword.
    body="${line:1}"
    if echo "$body" | grep -Eqi "$KEYWORDS_REGEX"; then
        flagged_files+=("${current_file}")
    fi
done <<<"$DIFF"

if [ "${#flagged_files[@]}" -eq 0 ]; then
    exit 0
fi

if [ "$adr_added" -eq 1 ]; then
    exit 0
fi

# Deduplicate the flagged file list for the operator message.
unique_files=$(printf '%s\n' "${flagged_files[@]}" | sort -u)

cat >&2 <<EOF
ERROR: Guard 1 (check-keyword-adr) tripped.

The following file(s) added one of the high-risk policy keywords
(unconditionally / always / bypass) without a matching ADR in the
same diff under ${ADR_PATH_PREFIX}:

${unique_files}

These keywords historically introduced security-relevant behaviour
changes (see commit b960869 for the precedent). Land an ADR explaining
the decision in the same diff, or use less absolute language.
EOF
exit 1
