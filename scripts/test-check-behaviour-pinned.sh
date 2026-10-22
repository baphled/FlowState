#!/bin/bash
# Sanity tests for scripts/check-behaviour-pinned.sh — Guard 2 of the
# review-pattern guards (B2).
#
# The guard inspects a staged diff and a commit-message file. When the
# diff adds a new exported Go function AND its accompanying *_test.go
# file in the same commit, the message body MUST contain a
# "Behaviour-Pinned: <reason>" trailer. The trailer is a flag for the
# next reviewer to challenge — its presence does not gate, only its
# absence on a func+test diff does.
#
# Tests use synthetic diffs and message files so the suite stays
# independent of the live git index.

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GUARD="${SCRIPT_DIR}/check-behaviour-pinned.sh"

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

run_case() {
    local diff="$1"
    local msg="$2"
    local diff_file
    local msg_file
    diff_file=$(mktemp)
    msg_file=$(mktemp)
    printf '%s' "$diff" > "$diff_file"
    printf '%s' "$msg" > "$msg_file"
    "$GUARD" --diff-file "$diff_file" --message-file "$msg_file" >/dev/null 2>&1
    local rc=$?
    rm -f "$diff_file" "$msg_file"
    return $rc
}

# -------------------------------------------------------------------
# Case 1: diff adds an exported func + a test, message lacks trailer.
# -> guard fires, exit 1.
# -------------------------------------------------------------------
DIFF1=$(cat <<'EOF'
diff --git a/internal/foo/foo.go b/internal/foo/foo.go
new file mode 100644
index 0000000..1111111
--- /dev/null
+++ b/internal/foo/foo.go
@@ -0,0 +1,3 @@
+package foo
+
+func DoSomething() {}
diff --git a/internal/foo/foo_test.go b/internal/foo/foo_test.go
new file mode 100644
index 0000000..2222222
--- /dev/null
+++ b/internal/foo/foo_test.go
@@ -0,0 +1,3 @@
+package foo
+
+func TestDoSomething(t *testing.T) {}
EOF
)
MSG1='feat(foo): add DoSomething'
run_case "$DIFF1" "$MSG1" && rc=0 || rc=$?
assert_exit "func+test without trailer exits 1" 1 "$rc"

# -------------------------------------------------------------------
# Case 2: same diff, message includes Behaviour-Pinned trailer.
# -> guard passes, exit 0.
# -------------------------------------------------------------------
MSG2='feat(foo): add DoSomething

Behaviour-Pinned: this test pins the contract for the new ADR-X policy.'
run_case "$DIFF1" "$MSG2" && rc=0 || rc=$?
assert_exit "func+test with trailer exits 0" 0 "$rc"

# -------------------------------------------------------------------
# Case 3: diff adds a test only (no new exported func). Out of scope.
# -> guard passes, exit 0.
# -------------------------------------------------------------------
DIFF3=$(cat <<'EOF'
diff --git a/internal/foo/foo_test.go b/internal/foo/foo_test.go
index 1111111..2222222 100644
--- a/internal/foo/foo_test.go
+++ b/internal/foo/foo_test.go
@@ -1,3 +1,4 @@
 package foo

+func TestExtra(t *testing.T) {}
EOF
)
MSG3='test(foo): add extra coverage'
run_case "$DIFF3" "$MSG3" && rc=0 || rc=$?
assert_exit "test-only diff (no new exported func) exits 0" 0 "$rc"

# -------------------------------------------------------------------
# Case 4: diff adds an unexported func (lowercase) + test. Out of scope.
# -> guard passes, exit 0. The pattern of concern is exported surfaces.
# -------------------------------------------------------------------
DIFF4=$(cat <<'EOF'
diff --git a/internal/foo/foo.go b/internal/foo/foo.go
index 1111111..2222222 100644
--- a/internal/foo/foo.go
+++ b/internal/foo/foo.go
@@ -1,3 +1,4 @@
 package foo

+func helper() {}
diff --git a/internal/foo/foo_test.go b/internal/foo/foo_test.go
index 1111111..2222222 100644
--- a/internal/foo/foo_test.go
+++ b/internal/foo/foo_test.go
@@ -1,3 +1,4 @@
 package foo

+func TestHelper(t *testing.T) {}
EOF
)
MSG4='feat(foo): add helper'
run_case "$DIFF4" "$MSG4" && rc=0 || rc=$?
assert_exit "unexported func + test exits 0" 0 "$rc"

# -------------------------------------------------------------------
# Case 5: diff adds an exported func only (no test). Out of scope.
# -> guard passes, exit 0. The pattern of concern is the func+test pair.
# -------------------------------------------------------------------
DIFF5=$(cat <<'EOF'
diff --git a/internal/foo/foo.go b/internal/foo/foo.go
index 1111111..2222222 100644
--- a/internal/foo/foo.go
+++ b/internal/foo/foo.go
@@ -1,3 +1,4 @@
 package foo

+func NewExported() {}
EOF
)
MSG5='feat(foo): add NewExported'
run_case "$DIFF5" "$MSG5" && rc=0 || rc=$?
assert_exit "exported func without test exits 0" 0 "$rc"

# -------------------------------------------------------------------
# Case 6: exported method on a receiver + test, no trailer.
# -> guard fires, exit 1. Methods are equally a behaviour surface.
# -------------------------------------------------------------------
DIFF6=$(cat <<'EOF'
diff --git a/internal/foo/foo.go b/internal/foo/foo.go
index 1111111..2222222 100644
--- a/internal/foo/foo.go
+++ b/internal/foo/foo.go
@@ -1,3 +1,4 @@
 package foo

+func (b *Bar) DoThing() {}
diff --git a/internal/foo/foo_test.go b/internal/foo/foo_test.go
index 1111111..2222222 100644
--- a/internal/foo/foo_test.go
+++ b/internal/foo/foo_test.go
@@ -1,3 +1,4 @@
 package foo

+func TestBar_DoThing(t *testing.T) {}
EOF
)
MSG6='feat(foo): add Bar.DoThing'
run_case "$DIFF6" "$MSG6" && rc=0 || rc=$?
assert_exit "exported method + test without trailer exits 1" 1 "$rc"

echo
echo "Summary: ${PASS} passed, ${FAIL} failed"
[ "$FAIL" -eq 0 ]
