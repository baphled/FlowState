#!/usr/bin/env bash
# Enforces that every step-context constructor defined in
# features/support/*_steps.go is registered from InitializeScenario in
# features/support/godog_test.go. An unregistered context silently turns
# its scenarios into "undefined" output, which masked ~39 scenarios
# historically. Exits non-zero listing any unregistered constructors.
#
# Expected:
#   - every exported Register*Steps / *Context func in support step
#     files appears in godog_test.go's InitializeScenario body.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SUPPORT="${REPO_ROOT}/features/support"
REGISTRY="${SUPPORT}/godog_test.go"

status=0
while IFS= read -r -d '' file; do
	[ "$(basename "$file")" = "godog_test.go" ] && continue
	constructors=$(grep -hoE '^func (Register[A-Za-z0-9]+|[A-Za-z0-9]+Context)\(' "$file" || true)
	[ -z "$constructors" ] && continue
	while IFS= read -r ctor; do
		name="${ctor#func }"
		name="${name%%(*}"
		if ! grep -qE "\b${name}\(" "$REGISTRY"; then
			echo "unregistered step context: ${name} (defined in features/support/$(basename "$file"))"
			status=1
		fi
	done <<< "$constructors"
done < <(find "$SUPPORT" -maxdepth 1 -name '*_steps.go' -print0)

if [ "$status" -ne 0 ]; then
	echo "FAIL: register the contexts above in InitializeScenario (features/support/godog_test.go)"
fi
exit "$status"
