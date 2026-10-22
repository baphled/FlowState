package cli

import (
	"strings"
	"testing"
)

// TestRunPreCommitPollutionSurfacesGitFailure verifies that a git
// failure is surfaced as an error instead of silently passing the
// pollution check (fail loudly, not silently). git is made
// unfindable via PATH so the failure is deterministic regardless of
// the host's directory layout.
func TestRunPreCommitPollutionSurfacesGitFailure(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	blocked, reason, err := runPreCommitPollution(t.TempDir())
	if err == nil {
		t.Fatal("expected git failure to be surfaced as an error, got nil")
	}
	if !strings.Contains(err.Error(), "git ls-files") {
		t.Fatalf("error should mention the failing git command, got: %v", err)
	}
	if blocked || reason != "" {
		t.Fatalf("git failure should not fabricate a violation, got blocked=%v reason=%q", blocked, reason)
	}
}

// TestUnaccountedWorktreeWritesFlagsOmittedPaths verifies ADR-0001
// gap 2: any porcelain-reported worktree change absent from the
// gate's self-report is an unaccounted write, listed in the reason.
func TestUnaccountedWorktreeWritesFlagsOmittedPaths(t *testing.T) {
	porcelain := []string{
		" M internal/swarm/gates.go",
		"?? stray-report.json",
		"?? docs/notes.md",
	}
	reported := []string{"internal/swarm/gates.go"}
	blocked, reason := UnaccountedWorktreeWrites(porcelain, reported)
	if !blocked {
		t.Fatal("omitted write present in porcelain output must be flagged")
	}
	if !strings.Contains(reason, "stray-report.json") || !strings.Contains(reason, "docs/notes.md") {
		t.Fatalf("reason should list unaccounted paths, got: %s", reason)
	}
	if strings.Contains(reason, "gates.go") {
		t.Fatalf("reported path must not be flagged, got: %s", reason)
	}
}

// TestUnaccountedWorktreeWritesPassesWhenReconciled verifies a full
// self-report reconciles cleanly against the porcelain ground truth.
func TestUnaccountedWorktreeWritesPassesWhenReconciled(t *testing.T) {
	blocked, reason := UnaccountedWorktreeWrites(
		[]string{" M internal/swarm/gates.go", "?? notes.md"},
		[]string{"internal/swarm/gates.go", "notes.md"},
	)
	if blocked {
		t.Fatalf("fully accounted tree should pass, got: %s", reason)
	}
}
