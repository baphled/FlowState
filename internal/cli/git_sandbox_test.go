package cli_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The CLI suite shells out to git (autoresearch trial commits, repo
// scaffolding in autoresearch_test.go, promote specs, ...). The
// 2026-08-14 "make check self-poisoning" incident was caused by a
// spec whose git invocation inherited the test process's working
// directory — internal/cli inside the live FlowState worktree — so
// its commits landed on feature/agent-platform's HEAD and its
// index churn produced ~40 phantom deleted files. Three repair
// attempts were destroyed that way.
//
// These specs are the regression guard. They pin the two
// invariants that make the incident structurally impossible:
//
//  1. The test process runs from a working directory that is NOT
//     inside any git repository. A forgotten cmd.Dir / -C then
//     fails git's repository discovery instead of silently
//     resolving to the live repo.
//  2. Any suite-driven git activity leaves the executing
//     worktree's HEAD and reflog untouched.
var _ = Describe("CLI suite git sandbox", func() {
	// gitIn runs git from the process's current working directory
	// with no explicit -C, exactly like the unsandboxed call that
	// caused the incident.
	gitIn := func(args ...string) (string, error) {
		cmd := exec.Command("git", args...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}

	// gitRepo runs git against an explicit repository directory —
	// the sandboxed form every helper in autoresearch_test.go uses.
	gitRepo := func(dir string, args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		out, err := cmd.CombinedOutput()
		ExpectWithOffset(1, err).NotTo(HaveOccurred(), "git %s: %s", strings.Join(args, " "), string(out))
		return strings.TrimSpace(string(out))
	}

	It("executes with its working directory outside any git repository", func() {
		// If the process cwd were inside a repository, repository
		// discovery succeeds and returns the worktree root. That is
		// the precondition the 2026-08-14 poison relied on.
		out, err := gitIn("rev-parse", "--show-toplevel")
		Expect(err).To(HaveOccurred(),
			"suite must not run inside a git repository; discovery resolved to %q", out)
		Expect(out).NotTo(ContainSubstring("FlowState"))
	})

	It("leaves the executing worktree's HEAD untouched when the suite commits to a temp repo", func() {
		// The worktree this suite executes in, discovered the same
		// way git discovers it for an unsandboxed call.
		worktreeTop, topErr := gitIn("rev-parse", "--show-toplevel")
		if topErr != nil {
			Skip("suite cwd is outside any repository — invariance holds trivially")
		}
		headBefore := gitRepo(worktreeTop, "rev-parse", "HEAD")
		reflogBefore := gitRepo(worktreeTop, "reflog", "--format=%H")

		// Reproduce the suite's git activity: scaffold a temp repo
		// and commit to it, the same pattern initRepo and the
		// autoresearch trial loop use.
		tmp := GinkgoT().TempDir()
		gitRepo(tmp, "init", "--initial-branch=main", tmp)
		gitRepo(tmp, "config", "user.email", "test@example.com")
		gitRepo(tmp, "config", "user.name", "test")
		Expect(os.WriteFile(tmp+"/README.md", []byte("scratch\n"), 0o600)).To(Succeed())
		gitRepo(tmp, "add", ".")
		gitRepo(tmp, "commit", "--no-verify", "-m", "initial")

		headAfter := gitRepo(worktreeTop, "rev-parse", "HEAD")
		reflogAfter := gitRepo(worktreeTop, "reflog", "--format=%H")

		Expect(headAfter).To(Equal(headBefore),
			"suite git activity must not move the executing worktree's HEAD")
		Expect(reflogAfter).To(Equal(reflogBefore),
			"suite git activity must not append reflog entries to the executing worktree")
	})

	It("leaves the live repository untouched when scaffolding a repo the way the apply specs do", func() {
		// The apply-Describe initRepo sequence (autoresearch_test.go)
		// must operate strictly inside its temp repo. Pin the live
		// side of the invariant against the repository that contains
		// this suite's source directory — the live FlowState worktree
		// under `make check`. The suite's GIT_CEILING_DIRECTORIES
		// sandbox blocks git's own discovery from the source tree, so
		// resolve the root in Go: walk up to the nearest .git entry.
		_, thisFile, _, ok := runtime.Caller(0)
		if !ok {
			Skip("cannot locate this suite's source file")
		}
		liveRoot := ""
		for dir := filepath.Dir(thisFile); ; {
			if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
				liveRoot = dir
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
		if liveRoot == "" {
			Skip("no live repository above this suite's source — invariance holds trivially")
		}

		// gitLive runs git inside the live root with the suite's
		// ceiling sandbox lifted, so the pinned repo resolves exactly
		// the way an unsandboxed call would.
		gitLive := func(args ...string) string {
			cmd := exec.Command("git", args...)
			cmd.Dir = liveRoot
			env := os.Environ()
			kept := make([]string, 0, len(env))
			for _, kv := range env {
				if strings.HasPrefix(kv, "GIT_CEILING_DIRECTORIES=") {
					continue
				}
				kept = append(kept, kv)
			}
			cmd.Env = kept
			out, err := cmd.CombinedOutput()
			Expect(err).NotTo(HaveOccurred(), "git %s: %s", strings.Join(args, " "), string(out))
			return strings.TrimSpace(string(out))
		}

		headBefore := gitLive("rev-parse", "HEAD")
		reflogBefore := gitLive("reflog", "--format=%H")

		// initRepo, verbatim shape: fresh temp repo (through the
		// mustGitDir sandbox guard), plumbing config, program files
		// under internal/app, then the initial commit.
		repo := mustGitDir(GinkgoT().TempDir())
		gitRepo(repo, "init", "--initial-branch=main", repo)
		gitRepo(repo, "config", "user.email", "test@example.com")
		gitRepo(repo, "config", "user.name", "test")
		Expect(os.MkdirAll(filepath.Join(repo, "internal", "app", "agents"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(repo, "internal", "app", "agents", "planner.md"),
			[]byte("planner body\n"), 0o600)).To(Succeed())
		gitRepo(repo, "add", ".")
		gitRepo(repo, "commit", "--no-verify", "-m", "initial")

		Expect(gitLive("rev-parse", "HEAD")).To(Equal(headBefore),
			"the initRepo sequence must not move the live repository's HEAD")
		Expect(gitLive("reflog", "--format=%H")).To(Equal(reflogBefore),
			"the initRepo sequence must not append reflog entries to the live repository")
	})
})
