// Package cli — pre-commit pollution-check subcommand.
//
// The .git-hooks/pre-commit script delegates its untracked-file scan
// here so the shell hook and the BDD glue share one policy: the
// exported swarm.PreCommitPollutionCheck. Before this command the
// hook reimplemented the pattern list in bash with divergent
// exemptions (e.g. skipping *.feature files) that the Go policy
// never honoured, so the two could disagree on the same tree.
package cli

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/baphled/flowstate/internal/swarm"
)

// newPreCommitPollutionCmd builds the hidden `precommit-pollution`
// command. It is intentionally read-only and needs no bootstrap: it
// scans git's untracked list and exits non-zero when the Go policy
// finds repo-root pollution files.
//
// Expected:
//   - None (command build only).
//
// Returns:
//   - A configured *cobra.Command.
//
// Side effects:
//   - None (command build only).
func newPreCommitPollutionCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "precommit-pollution",
		Short:  "Run the pre-commit filesystem pollution check",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			repoRoot, err := cmd.Flags().GetString("repo-root")
			if err != nil {
				return err
			}
			blocked, reason, err := runPreCommitPollution(repoRoot)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "ERROR: %v\n", err)
				return fmt.Errorf("pre-commit pollution check could not run: %w", err)
			}
			if blocked {
				fmt.Fprintln(cmd.ErrOrStderr(), "BLOCKED: "+reason)
				fmt.Fprintln(cmd.ErrOrStderr(),
					"Inter-agent and scratch data must flow through the coordination store.")
				return fmt.Errorf("filesystem pollution detected")
			}
			return nil
		},
	}
}

// runPreCommitPollution gathers git's untracked list for repoRoot and
// applies swarm.PreCommitPollutionCheck. When git is unavailable or
// the directory is not a work tree the check is skipped (nil result),
// mirroring the hook's historic behaviour outside a repository.
//
// Expected:
//   - repoRoot is the repository root path (may be empty for CWD).
//
// Returns:
//   - blocked and reason from the shared Go policy; a non-nil error
//     when git itself fails, so callers surface the failure instead
//     of silently passing.
//
// Side effects:
//   - Runs `git ls-files --others --exclude-standard` in repoRoot.
func runPreCommitPollution(repoRoot string) (bool, string, error) {
	untracked, err := gitUntracked(repoRoot)
	if err != nil {
		return false, "", fmt.Errorf("git ls-files failed: %w", err)
	}
	blocked, reason := swarm.PreCommitPollutionCheck(repoRoot, untracked)
	return blocked, reason, nil
}

// gitUntracked returns the untracked file list from git. A nil slice
// (not an empty one) signals "no git metadata" so the shared policy
// skips the check, matching its nil-untracked contract.
//
// Expected:
//   - repoRoot is the repository root path (may be empty for CWD).
//
// Returns:
//   - The relative untracked paths, or nil when git fails.
//
// Side effects:
//   - Executes git as a subprocess.
func gitUntracked(repoRoot string) ([]string, error) {
	gitCmd := exec.Command("git", "ls-files", "--others", "--exclude-standard")
	if repoRoot != "" {
		gitCmd.Dir = repoRoot
	}
	out, err := gitCmd.Output()
	if err != nil {
		return nil, err
	}
	var files []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			files = append(files, line)
		}
	}
	if files == nil {
		files = []string{}
	}
	return files, nil
}

// init registers the hidden subcommand on the root command set. The
// hook invokes it as `flowstate precommit-pollution --repo-root $PWD`.
func init() {
	rootExtraCommands = append(rootExtraCommands, func(root *cobra.Command) {
		cmd := newPreCommitPollutionCmd()
		cmd.Flags().String("repo-root", "", "repository root (defaults to CWD)")
		root.AddCommand(cmd)
	})
}

// rootExtraCommands carries extra subcommands appended to every root
// command built by newRootCmd. Held as a package-level slice so an
// init() in any file of this package can contribute a command without
// an import cycle into root.go.
var rootExtraCommands []func(*cobra.Command)
