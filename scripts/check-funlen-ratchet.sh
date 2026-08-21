#!/usr/bin/env bash
# Funlen ratchet (50 lines / 40 statements).
# Counts funlen violations across the repo using the standalone config
# scripts/funlen-ratchet.yml (no exclusions) and fails if the count
# exceeds the baseline in .funlen-baseline. Refactor long functions and
# lower the baseline — never raise it.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BASELINE_FILE="${REPO_ROOT}/.funlen-baseline"
CONFIG="${REPO_ROOT}/scripts/funlen-ratchet.yml"

if [[ ! -f "${BASELINE_FILE}" ]]; then
  echo "FAIL: missing ${BASELINE_FILE}"
  exit 1
fi

baseline="$(head -1 "${BASELINE_FILE}" | tr -d '[:space:]')"

count="$(GOTOOLCHAIN=go1.26.1 golangci-lint run --config "${CONFIG}" ./... 2>/dev/null | grep -c 'funlen' || true)"

echo "funlen violations: ${count} (baseline: ${baseline})"
if (( count > baseline )); then
  echo "FAIL: funlen violations increased above the ratchet baseline."
  echo "Refactor the offending functions or lower the baseline, never raise it."
  exit 1
fi
exit 0
