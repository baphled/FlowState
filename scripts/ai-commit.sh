#!/bin/bash
# Create a commit carrying only a validated external Project-Metadata reference.
# Usage: scripts/ai-commit.sh MESSAGE_FILE EXTERNAL_RECORD_CACHE
# The cache must be fetched from Dropbox and kept outside the checkout.
set -euo pipefail
if [[ $# != 2 || -n ${NO_VERIFY:-} ]]; then
    echo 'Usage: scripts/ai-commit.sh MESSAGE_FILE EXTERNAL_RECORD_CACHE (hooks required)' >&2
    exit 1
fi
if git diff --cached --quiet; then
    echo 'ERROR: No staged changes' >&2
    exit 1
fi
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
final_message=$(mktemp)
trap 'rm -f -- "$final_message"' EXIT
python3 "$script_dir/project_metadata.py" --prepare-message "$1" \
    --external-record "$2" --output "$final_message"
git commit -F "$final_message"
