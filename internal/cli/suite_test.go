package cli_test

import (
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestCLI(t *testing.T) {
	// The suite shells out to git, and the 2026-08-14 "make check
	// self-poisoning" incident showed two ways a spec can land its
	// commits on the live FlowState worktree:
	//
	//  1. A git call with no cmd.Dir / -C inherits the process cwd.
	//     `make check` runs the suite from inside the repo, so
	//     repository discovery resolves to the checked-out branch.
	//  2. When the gate runs from the pre-commit hook, git exports
	//     GIT_DIR / GIT_WORK_TREE / GIT_INDEX_FILE into the hook
	//     environment. Those override cwd for every git subprocess
	//     the suite spawns, so even a Dir-pinned call commits into
	//     the live repo's index. This vector destroyed three repair
	//     attempts before the root cause was found.
	//
	// The sandbox below closes both: the process cwd moves to a
	// scratch directory with no .git ancestor, and the inherited
	// plumbing variables are removed so subprocess git resolves
	// repositories by directory only. Pinned by git_sandbox_test.go.
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("cli suite cwd: %v", err)
	}

	sandbox, err := os.MkdirTemp("", "flowstate-cli-suite-")
	if err != nil {
		t.Fatalf("cli suite sandbox: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sandbox) })

	if err := os.Chdir(sandbox); err != nil {
		t.Fatalf("cli suite chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	for _, key := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_QUARANTINE_PATH"} {
		t.Setenv(key, "")
		os.Unsetenv(key)
	}
	// Belt and braces: even if discovery walked up from the
	// sandbox, it must stop before reaching the worktree the gate
	// executes in.
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Clean(filepath.Join(prev, "..", "..")))

	// Disable commit signing for every git subprocess the suite
	// spawns: the developer's global config may enable GPG signing,
	// which fails ("gpg failed to sign the data") in sandboxed
	// environments. Test repos are throwaway and never signed.
	t.Setenv("GIT_CONFIG_COUNT", "2")
	t.Setenv("GIT_CONFIG_KEY_0", "commit.gpgsign")
	t.Setenv("GIT_CONFIG_VALUE_0", "false")
	t.Setenv("GIT_CONFIG_KEY_1", "tag.gpgsign")
	t.Setenv("GIT_CONFIG_VALUE_1", "false")

	RegisterFailHandler(Fail)
	RunSpecs(t, "CLI Suite")
}
