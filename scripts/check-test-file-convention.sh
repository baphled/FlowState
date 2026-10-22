#!/usr/bin/env bash
# Enforces the test-file convention ratchet:
#   1. A canonical source file may have at most two matching test files.
#   2. No NEW orphan test files (test file with no matching source file)
#      may appear beyond the committed baseline in
#      scripts/test-file-baseline.txt. The baseline is a shrinking ratchet:
#      remove entries as orphan test files are consolidated into canonical
#      <source>_test.go files. Never add entries.
# Exits non-zero with a report on violation.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BASELINE="${REPO_ROOT}/scripts/test-file-baseline.txt"
COUNT_BASELINE="${REPO_ROOT}/.test-file-baseline"
SCAN_DIRS=(internal cmd tools)

fail=0

# Canonical bases (source file with at least one matching <base>_test.go).
canonical="$(mktemp)"
for dir in "${SCAN_DIRS[@]}"; do
  find "${REPO_ROOT}/${dir}" -name '*_test.go' -type f 2>/dev/null |
    sed "s|${REPO_ROOT}/||" |
    while IFS= read -r testfile; do
      base="${testfile%_test.go}"
      if [[ -f "${REPO_ROOT}/${base}.go" ]]; then
        echo "${base}"
      fi
    done
done | sort > "${canonical}"

# --- Rule 1: at most two test files per canonical source --------------------
while IFS= read -r line; do
  count="${line%% *}"
  base="${line#* }"
  if (( count > 2 )); then
    echo "FAIL: ${base}.go has ${count} test files (max 2 per source file)"
    fail=1
  fi
done < <(uniq -c "${canonical}")

# --- Build the current orphan list ------------------------------------------
current="$(mktemp)"
for dir in "${SCAN_DIRS[@]}"; do
  find "${REPO_ROOT}/${dir}" -name '*_test.go' -type f 2>/dev/null |
    sed "s|${REPO_ROOT}/||" |
    while IFS= read -r testfile; do
      base="${testfile%_test.go}"
      if [[ ! -f "${REPO_ROOT}/${base}.go" ]]; then
        echo "${testfile}"
      fi
    done
done | sort -u > "${current}"

# --- Rule 2: orphan ratchet — count must not exceed the baseline count -----
baseline_count="$(head -1 "${COUNT_BASELINE}" 2>/dev/null | tr -d '[:space:]')"
if [[ -z "${baseline_count}" ]]; then
  echo "FAIL: missing count baseline ${COUNT_BASELINE}"
  exit 1
fi
orphan_count="$(wc -l < "${current}" | tr -d ' ')"
echo "orphan test files: ${orphan_count} (count baseline: ${baseline_count})"
if (( orphan_count > baseline_count )); then
  echo "FAIL: orphan test file count exceeds the ratchet baseline."
  echo "Consolidate orphan test files into canonical <source>_test.go files"
  echo "and lower the baseline — never raise it."
  fail=1
fi

# --- Rule 3: no new orphans vs baseline list -------------------------------
extra="$(comm -23 "${current}" <(grep -v '^\s*$' "${BASELINE}" | grep -v '^\s*#' | sort -u))"
if [[ -n "${extra}" ]]; then
  echo "FAIL: new orphan test files detected (no matching source file):"
  echo "${extra}" | sed 's/^/  /'
  echo "Consolidate these into the canonical <source>_test.go instead."
  fail=1
fi

rm -f "${canonical}" "${current}"
exit "${fail}"
