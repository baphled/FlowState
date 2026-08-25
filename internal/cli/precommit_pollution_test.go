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
