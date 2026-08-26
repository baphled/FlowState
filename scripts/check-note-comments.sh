#!/usr/bin/env bash

set -euo pipefail

# ============================================================================
# Check NOTE: Comment Placement
# ============================================================================
# Enforces the rule: NOTE: is only permitted inside docblocks.
#
# A docblock is a consecutive block of // comment lines immediately adjacent
# (no blank line) to a declaration (func, type, var, const, package).
#
# NOTE: inside a function body, as a standalone comment, or separated from
# a declaration by a blank line is a VIOLATION.
#
# Usage:
#   bash scripts/check-note-comments.sh
#
# Exit codes:
#   0 - No violations found
#   1 - Violations found
# ============================================================================

violations=0

while IFS= read -r -d '' file; do
    mapfile -t lines < "$file"
    total=${#lines[@]}

    for ((i = 0; i < total; i++)); do
        line="${lines[$i]}"

        if [[ ! "$line" =~ \/\/.*NOTE: ]]; then
            continue
        fi

        line_num=$((i + 1))

        end=$i
        while ((end + 1 < total)); do
            next_line="${lines[$((end + 1))]}"
            if [[ "$next_line" =~ ^[[:space:]]*//.* ]]; then
                end=$((end + 1))
            else
                break
            fi
        done

        blank_count=0
        check=$((end + 1))
        while ((check < total)); do
            check_line="${lines[$check]}"
            if [[ "$check_line" =~ ^[[:space:]]*$ ]]; then
                blank_count=$((blank_count + 1))
                check=$((check + 1))
            else
                break
            fi
        done

        is_valid=false
        if ((blank_count == 0 && check < total)); then
            next_non_blank="${lines[$check]}"
            if [[ "$next_non_blank" =~ ^[[:space:]]*(func|type|var|const|package)[[:space:]] ]]; then
                is_valid=true
            fi
        fi

        if [[ "$is_valid" == false ]]; then
            trimmed="${line#"${line%%[![:space:]]*}"}"
            echo "$file:$line_num: $trimmed"
            violations=$((violations + 1))
        fi
    done
done < <(find . -name "*.go" -not -path "*/vendor/*" -not -name "*_test.go" -not -name "*_generated.go" -print0)

if ((violations > 0)); then
    echo ""
    echo "ERROR: Found $violations NOTE: comment(s) outside docblocks."
    exit 1
fi

echo "NOTE: comment check passed."
exit 0
