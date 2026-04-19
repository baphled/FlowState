#!/bin/bash
# Guard 2 of the review-pattern guards (B2).
#
# Required as part of make ai-commit. When the staged diff adds a new
# exported Go function or method AND a *_test.go file in the same
# commit, the commit message body MUST contain a "Behaviour-Pinned:
# <reason>" trailer. The trailer's presence does not block; its
# absence on a func+test diff does. It exists to flag the next
# reviewer that a behaviour is being pinned by the same author who
# introduced it — the exact pattern that produced commits b960869 and
# 8d76420.
#
# Usage (production, called from scripts/ai-commit.sh):
#   scripts/check-behaviour-pinned.sh --message-file <path>
#       diffs the staged changes against HEAD; reads the candidate
#       commit message from <path>.
#
# Usage (tests):
#   scripts/check-behaviour-pinned.sh --diff-file <d> --message-file <m>
#       reads the unified diff from <d> instead of the live index.

set -e

DIFF_FILE=""
MSG_FILE=""

while [ "$#" -gt 0 ]; do
    case "$1" in
        --diff-file)
            DIFF_FILE="$2"
            shift 2
            ;;
        --message-file)
            MSG_FILE="$2"
            shift 2
            ;;
        *)
            echo "ERROR: unknown argument: $1" >&2
            exit 2
            ;;
    esac
done

if [ -z "$MSG_FILE" ] || [ ! -r "$MSG_FILE" ]; then
    echo "ERROR: --message-file is required and must be readable" >&2
    exit 2
fi

if [ -n "$DIFF_FILE" ]; then
    if [ ! -r "$DIFF_FILE" ]; then
        echo "ERROR: --diff-file not readable: $DIFF_FILE" >&2
        exit 2
    fi
    DIFF=$(cat "$DIFF_FILE")
else
    DIFF=$(git diff --cached --unified=0)
fi

if [ -z "$DIFF" ]; then
    exit 0
fi

# Detect: did the diff add a new exported function or method to a
# non-test .go file, AND a *_test.go file in the same diff?
current_file=""
saw_exported_func=0
saw_test_file=0

while IFS= read -r line; do
    if [[ "$line" =~ ^\+\+\+\ b/(.+)$ ]]; then
        current_file="${BASH_REMATCH[1]}"
        if [[ "$current_file" == *_test.go ]]; then
            saw_test_file=1
        fi
        continue
    fi

    [[ "$line" =~ ^\+\+\+ ]] && continue
    [[ "$line" =~ ^---  ]] && continue
    [[ ! "$line" =~ ^\+ ]] && continue

    # Only count exported funcs in production *.go files.
    if [[ "$current_file" != *.go ]]; then
        continue
    fi
    if [[ "$current_file" == *_test.go ]]; then
        continue
    fi

    body="${line:1}"
    # Match an added Go function declaration whose name starts with an
    # uppercase letter (i.e. exported). Covers free functions and
    # methods on a receiver.
    #
    #   func DoThing(...)            -> exported free func
    #   func (b *Bar) DoThing(...)   -> exported method
    if echo "$body" | grep -Eq '^[[:space:]]*func[[:space:]]+(\([^)]+\)[[:space:]]+)?[A-Z][A-Za-z0-9_]*\('; then
        saw_exported_func=1
    fi
done <<<"$DIFF"

if [ "$saw_exported_func" -ne 1 ] || [ "$saw_test_file" -ne 1 ]; then
    exit 0
fi

# We have an exported func + a test file. Demand the trailer.
if grep -Eq '^[[:space:]]*Behaviour-Pinned:[[:space:]]*\S' "$MSG_FILE"; then
    exit 0
fi

cat >&2 <<'EOF'
ERROR: Guard 2 (check-behaviour-pinned) tripped.

This commit adds a new exported Go function (or method) AND a test
file in the same diff. That is the same shape that produced b960869
("MCP bypass") and 8d76420 ("silent strip") — author-as-only-reviewer
pinning behaviour with a test in the same commit.

Add a "Behaviour-Pinned:" trailer to the commit body explaining what
the test pins and why a future reviewer should examine it carefully:

    Behaviour-Pinned: <one-line reason>

The trailer is a flag for the next reviewer to challenge — the
trailer being present does not gate review, only its absence on a
func+test diff does.
EOF
exit 1
