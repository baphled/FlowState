package swarm_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/swarm"
)

// seedGitRepo initialises dir as a throwaway git repo with
// committer identity and one seed commit. It disables repo hooks
// so the fixture stays independent of this checkout's commit-time
// wiring. It is fixture setup only.
func seedGitRepo(dir string) {
	for _, args := range [][]string{
		{"git", "init", "-q", "-b", "main"},
		{"git", "config", "user.email", "spec@example.com"},
		{"git", "config", "user.name", "Spec"},
		{"git", "config", "core.hooksPath", ""},
		{"git", "commit", "-q", "--allow-empty", "-m", "seed"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, cerr := cmd.CombinedOutput(); cerr != nil {
			panic(string(out))
		}
	}
}

var _ = Describe("ScaffoldWorktree", func() {
	var repo string

	BeforeEach(func() {
		repo = GinkgoT().TempDir()
		seedGitRepo(repo)
	})

	It("creates the worktree directory with the branch checked out and returns the absolute path", func() {
		res, specErr := swarm.ScaffoldWorktree(repo, "feature/p1", "HEAD")
		if specErr != nil {
			panic(specErr)
		}
		abs, absErr := filepath.Abs(filepath.Join(repo, ".worktrees", "p1"))
		if absErr != nil {
			panic(absErr)
		}
		git := exec.Command("git", "-C", res.Path, "rev-parse", "--abbrev-ref", "HEAD")
		out, gitErr := git.CombinedOutput()
		if gitErr != nil {
			panic(string(out))
		}
		if _, dirErr := os.Stat(res.Path); dirErr != nil {
			panic(dirErr)
		}
		if res.Branch != "feature/p1" || !strings.Contains(string(out), "feature/p1") {
			panic(fmt.Sprintf("unexpected branch state: %s %s", res.Branch, out))
		}
		Expect(res.Path).To(Equal(abs))
	})

	It("errors with a typed error when the base ref is unknown", func() {
		_, err := swarm.ScaffoldWorktree(repo, "feature/p2", "no-such-ref")
		var wtErr *swarm.WorktreeError
		Expect(err).To(BeAssignableToTypeOf(wtErr))
	})

	It("prunes partial state when the base ref is unknown", func() {
		if _, specErr := swarm.ScaffoldWorktree(repo, "feature/p2b", "no-such-ref"); specErr == nil {
			panic("expected the scaffold to fail on an unknown base ref")
		}
		worktrees := filepath.Join(repo, ".worktrees")
		entries, _ := os.ReadDir(worktrees)
		Expect(entries).To(BeEmpty())
	})

	It("errors when the branch already exists", func() {
		_, err := swarm.ScaffoldWorktree(repo, "main", "HEAD")
		Expect(err).To(MatchError(ContainSubstring("already exists")))
	})

	It("rejects an empty branch name", func() {
		_, err := swarm.ScaffoldWorktree(repo, "", "HEAD")
		Expect(err).To(MatchError(ContainSubstring("invalid branch name")))
	})

	It("rejects a branch name containing dotdot", func() {
		_, err := swarm.ScaffoldWorktree(repo, "feature/../evil", "HEAD")
		Expect(err).To(MatchError(ContainSubstring("invalid branch name")))
	})

	It("rejects a branch name containing whitespace", func() {
		_, err := swarm.ScaffoldWorktree(repo, "feature one", "HEAD")
		Expect(err).To(MatchError(ContainSubstring("invalid branch name")))
	})

	It("rejects a branch name with a leading dash", func() {
		_, err := swarm.ScaffoldWorktree(repo, "-feature", "HEAD")
		Expect(err).To(MatchError(ContainSubstring("invalid branch name")))
	})

	It("rejects a branch name containing a path separator", func() {
		_, err := swarm.ScaffoldWorktree(repo, `feature\two`, "HEAD")
		Expect(err).To(MatchError(ContainSubstring("invalid branch name")))
	})

	It("rejects the bare dot branch name", func() {
		_, err := swarm.ScaffoldWorktree(repo, ".", "HEAD")
		Expect(err).To(MatchError(ContainSubstring("invalid branch name")))
	})

	It("rejects the bare dotdot branch name", func() {
		_, err := swarm.ScaffoldWorktree(repo, "..", "HEAD")
		Expect(err).To(MatchError(ContainSubstring("invalid branch name")))
	})
})
