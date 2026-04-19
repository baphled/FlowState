#!/bin/bash
# Sanity tests for scripts/check-keyword-adr.sh — Guard 1 of the
# review-pattern guards (B1).
#
# The guard fails commits that introduce unconditionally/always/bypass
# keywords into internal/**/*.go (excluding *_test.go) without an ADR
# file added in the same diff under the vault ADR path.
#
# Tests exercise the PASS and FAIL paths against synthetic git diffs
# fed via stdin so the test stays independent of the live git index.

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GUARD="${SCRIPT_DIR}/check-keyword-adr.sh"

if [ ! -x "$GUARD" ]; then
    echo "FAIL: ${GUARD} missing or not executable"
    exit 1
fi

PASS=0
FAIL=0

assert_exit() {
    local label="$1"
    local want="$2"
    local got="$3"
    if [ "$got" = "$want" ]; then
        echo "PASS: ${label}"
        PASS=$((PASS + 1))
    else
        echo "FAIL: ${label} (want exit=${want}, got exit=${got})"
        FAIL=$((FAIL + 1))
    fi
}

# -------------------------------------------------------------------
# Case 1: clean diff, no flagged keywords -> guard passes (exit 0).
# -------------------------------------------------------------------
DIFF1=$(cat <<'EOF'
diff --git a/internal/foo/foo.go b/internal/foo/foo.go
index 1111111..2222222 100644
--- a/internal/foo/foo.go
+++ b/internal/foo/foo.go
@@ -1,3 +1,4 @@
 package foo

+func Bar() {}

EOF
)
echo "$DIFF1" | "$GUARD" --stdin >/dev/null 2>&1 && rc=0 || rc=$?
assert_exit "clean diff exits 0" 0 "$rc"

# -------------------------------------------------------------------
# Case 2: flagged keyword in production go code, no ADR -> fails (1).
# -------------------------------------------------------------------
DIFF2=$(cat <<'EOF'
diff --git a/internal/engine/engine.go b/internal/engine/engine.go
index 1111111..2222222 100644
--- a/internal/engine/engine.go
+++ b/internal/engine/engine.go
@@ -1,3 +1,4 @@
 package engine

+// All MCP server tools unconditionally bypass manifest filtering.

EOF
)
echo "$DIFF2" | "$GUARD" --stdin >/dev/null 2>&1 && rc=0 || rc=$?
assert_exit "unconditionally without ADR exits 1" 1 "$rc"

# -------------------------------------------------------------------
# Case 3: flagged keyword + ADR added in same diff -> passes (0).
# The ADR path matches the vault location used by the project.
# -------------------------------------------------------------------
DIFF3=$(cat <<'EOF'
diff --git a/internal/engine/engine.go b/internal/engine/engine.go
index 1111111..2222222 100644
--- a/internal/engine/engine.go
+++ b/internal/engine/engine.go
@@ -1,3 +1,4 @@
 package engine

+// MCP tools always pass through the allowlist.

diff --git a/Documentation/Architecture/ADR/ADR - MCP Bypass Policy.md b/Documentation/Architecture/ADR/ADR - MCP Bypass Policy.md
new file mode 100644
index 0000000..3333333
--- /dev/null
+++ b/Documentation/Architecture/ADR/ADR - MCP Bypass Policy.md
@@ -0,0 +1,3 @@
+# ADR - MCP Bypass Policy
+
+Decision body.
EOF
)
echo "$DIFF3" | "$GUARD" --stdin >/dev/null 2>&1 && rc=0 || rc=$?
assert_exit "keyword with ADR in same diff exits 0" 0 "$rc"

# -------------------------------------------------------------------
# Case 4: flagged keyword in *_test.go only -> exempt, passes (0).
# -------------------------------------------------------------------
DIFF4=$(cat <<'EOF'
diff --git a/internal/engine/engine_test.go b/internal/engine/engine_test.go
index 1111111..2222222 100644
--- a/internal/engine/engine_test.go
+++ b/internal/engine/engine_test.go
@@ -1,3 +1,4 @@
 package engine_test

+// fixture: this comment uses bypass on purpose

EOF
)
echo "$DIFF4" | "$GUARD" --stdin >/dev/null 2>&1 && rc=0 || rc=$?
assert_exit "keyword only in *_test.go exits 0" 0 "$rc"

# -------------------------------------------------------------------
# Case 5: flagged keyword outside internal/ -> exempt, passes (0).
# -------------------------------------------------------------------
DIFF5=$(cat <<'EOF'
diff --git a/cmd/flowstate/main.go b/cmd/flowstate/main.go
index 1111111..2222222 100644
--- a/cmd/flowstate/main.go
+++ b/cmd/flowstate/main.go
@@ -1,3 +1,4 @@
 package main

+// always-on diagnostics

EOF
)
echo "$DIFF5" | "$GUARD" --stdin >/dev/null 2>&1 && rc=0 || rc=$?
assert_exit "keyword outside internal/ exits 0" 0 "$rc"

# -------------------------------------------------------------------
# Case 6: replay of the b960869 docstring change -> guard fires.
# This is the explicit cherry-revert sanity check called for in the
# Guard 1 acceptance criteria.
# -------------------------------------------------------------------
DIFF6=$(cat <<'EOF'
diff --git a/internal/engine/engine.go b/internal/engine/engine.go
index 1111111..2222222 100644
--- a/internal/engine/engine.go
+++ b/internal/engine/engine.go
@@ -461,7 +461,8 @@ func (e *Engine) appendDelegationSections(base string) string {
 // Returns:
 //   - A map of allowed tool names, or nil when the manifest does not restrict tools
 //     (empty Capabilities.Tools means all tools are allowed for backward compatibility).
-//   - Tools from declared MCPServers are merged into the allowed set when Tools is non-empty.
+//   - All MCP server tools unconditionally bypass manifest filtering because they are
+//     user-configured external tools; the manifest only controls built-in tool access.
 //
 // Side effects:
 //   - None.
EOF
)
echo "$DIFF6" | "$GUARD" --stdin >/dev/null 2>&1 && rc=0 || rc=$?
assert_exit "b960869 docstring replay exits 1 (catches the original bug)" 1 "$rc"

echo
echo "Summary: ${PASS} passed, ${FAIL} failed"
[ "$FAIL" -eq 0 ]
