package swarm

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// WorktreeResult reports the location and branch of a scaffolded
// git worktree.
type WorktreeResult struct {
	Path   string
	Branch string
}

// WorktreeError is the typed error for worktree scaffolding
// failures; Op names the failing step.
type WorktreeError struct {
	Op  string
	Err error
}

// Error renders the failing operation and its cause.
func (e *WorktreeError) Error() string {
	return fmt.Sprintf("swarm: worktree %s: %v", e.Op, e.Err)
}

// Unwrap exposes the underlying cause.
func (e *WorktreeError) Unwrap() error { return e.Err }

// validateBranchName uses `git check-ref-format --branch` as the
// authority when git is available (rejecting empty names, "..",
// whitespace, leading "-", path separators, and the bare names
// "." / ".."), with a local fallback of the same rules for
// environments where git cannot be executed. One approach,
// documented here: git is authoritative; the local check only
// runs when the git binary itself is missing.
func validateBranchName(name string) error {
	cmd := exec.Command("git", "check-ref-format", "--branch", name)
	if err := cmd.Run(); err == nil {
		return nil
	} else if !isExecNotFoundError(err) {
		return &WorktreeError{Op: "invalid branch name", Err: fmt.Errorf("%q rejected by git check-ref-format", name)}
	}
	unsafe := name == "" || name == "." || name == ".." ||
		strings.ContainsAny(name, " \t\n\r") || strings.Contains(name, "..") ||
		strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, "-")
	if unsafe {
		return &WorktreeError{Op: "invalid branch name", Err: fmt.Errorf("%q is not a safe branch name", name)}
	}
	return nil
}

func isExecNotFoundError(err error) bool {
	var execErr *exec.Error
	return errors.As(err, &execErr)
}

// ScaffoldWorktree creates a git worktree for branch under repo, at
// repo/.worktrees/<branch-basename>, based on base ref. It creates
// the branch when it does not exist, errors when it does or when
// base is unknown (pruning any partial registration on failure),
// and returns the resolved absolute path.
func ScaffoldWorktree(repo, branch, base string) (WorktreeResult, error) {
	if err := validateBranchName(branch); err != nil {
		return WorktreeResult{}, err
	}
	path := filepath.Join(repo, ".worktrees", filepath.Base(branch))
	add := exec.Command("git", "-C", repo, "worktree", "add", "-b", branch, path, base)
	if out, err := add.CombinedOutput(); err != nil {
		// Best-effort cleanup so a failed scaffold leaves no partial
		// worktree registration behind.
		_ = exec.Command("git", "-C", repo, "worktree", "prune").Run()
		return WorktreeResult{}, &WorktreeError{Op: "add", Err: fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return WorktreeResult{}, &WorktreeError{Op: "resolve path", Err: err}
	}
	return WorktreeResult{Path: abs, Branch: branch}, nil
}
