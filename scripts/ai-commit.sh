#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 || -n "${NO_VERIFY:-}" ]]; then
    echo 'Usage: PROJECT_METADATA_FILE=/outside/checkout/record.json make ai-commit FILE=/outside/checkout/message.txt' >&2
    echo 'Hooks cannot be skipped.' >&2
    exit 1
fi
if [[ ! -r "$1" || ! -s "$1" || -z "${PROJECT_METADATA_FILE:-}" ]]; then
    echo 'A real commit message and externally stored Dropbox metadata record are required.' >&2
    exit 1
fi
root=$(git rev-parse --show-toplevel)
branch=$(git branch --show-current)
if [[ -z "$branch" || "$branch" == main || "$branch" == next ]]; then
    echo 'Use an isolated feature worktree from origin/next.' >&2
    exit 1
fi
if git diff --cached --quiet; then
    echo 'No staged changes.' >&2
    exit 1
fi
message=$(mktemp)
trap 'rm -f "$message"' EXIT
python3 -B - "$1" "$PROJECT_METADATA_FILE" "$message" <<'CODE'
import json
import re
import sys
from pathlib import Path

text = Path(sys.argv[1]).read_text().strip()
if not re.match(r'^(feat|fix|docs|style|refactor|test|chore|perf|ci|build|revert)(\([a-zA-Z0-9_-]+\))?: \S', text):
    raise SystemExit('A concrete conventional commit subject is required.')
if re.search(r'^(AI-Generated-By|AI-Agent|AI-Model|Reviewed-By|Co-authored-by|Project-Metadata):', text, re.I | re.M):
    raise SystemExit('Keep identity/review claims in Dropbox; the helper adds the neutral reference.')
record = json.loads(Path(sys.argv[2]).read_text())
Path(sys.argv[3]).write_text(text + '\n\nProject-Metadata: ' + record.get('metadata_ref', '') + '\n')
CODE
python3 -B "$root/scripts/project_metadata.py" --staged --message-file "$message" --external-record "$PROJECT_METADATA_FILE"
git commit -F "$message"
