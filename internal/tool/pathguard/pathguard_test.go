package pathguard_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/tool/pathguard"
)

// Pathguard tests pin token-precise matching. Pre-fix, CheckCommand ran
// strings.Contains over the whole expanded command, so any incidental
// mention of the vault path tripped the deny — comments, echo arguments,
// quoted error messages, even path fragments inside heredocs. The fixed
// guard tokenises the command and only treats tokens that LOOK like
// filesystem paths AND resolve under a denied root as denials. Tokens
// fully enclosed in quotes are exempt because they are not file-access
// arguments to the shell.
var _ = Describe("Pathguard", func() {
	var (
		denied string
		guard  *pathguard.Guard
	)

	BeforeEach(func() {
		// Use a sentinel directory that is NOT this process's cwd so the
		// cwd carve-out does not short-circuit denial checks.
		denied = "/tmp/pathguard-vault-fixture"
		guard = pathguard.New([]string{denied})
	})

	Describe("Check (filesystem-tool path argument)", func() {
		It("denies an absolute path under the denied root", func() {
			err := guard.Check(filepath.Join(denied, "foo.md"))
			Expect(err).To(HaveOccurred())
		})

		It("allows paths outside the denied root", func() {
			err := guard.Check("/tmp/anywhere-else/foo.md")
			Expect(err).NotTo(HaveOccurred())
		})

		It("is a no-op when no denied roots are configured", func() {
			open := pathguard.New(nil)
			Expect(open.Check(filepath.Join(denied, "foo.md"))).NotTo(HaveOccurred())
		})
	})

	Describe("CheckCommand — false positives that the substring scan tripped", func() {
		It("allows a comment that mentions the denied path", func() {
			cmd := `echo "TODO: review ` + denied + `/notes.md later"`
			Expect(guard.CheckCommand(cmd)).NotTo(HaveOccurred())
		})

		It("allows a heredoc whose body mentions the denied path", func() {
			cmd := "cat <<EOF\nerror: " + denied + " is blocked\nEOF"
			Expect(guard.CheckCommand(cmd)).NotTo(HaveOccurred())
		})

		It("allows a command whose only mention of the denied path is in a trailing comment", func() {
			cmd := `find /tmp -name foo 2>/dev/null # was: find ` + denied
			Expect(guard.CheckCommand(cmd)).NotTo(HaveOccurred())
		})

		It("allows a bare word that contains the denied basename but is not a path", func() {
			cmd := "echo vault baphled note"
			Expect(guard.CheckCommand(cmd)).NotTo(HaveOccurred())
		})

		It("allows a single-quoted argument that contains the denied path", func() {
			cmd := `printf '%s\n' 'context: ` + denied + ` is internal'`
			Expect(guard.CheckCommand(cmd)).NotTo(HaveOccurred())
		})
	})

	Describe("CheckCommand — true positives that real path access still trips", func() {
		It("denies an unquoted path argument under the denied root", func() {
			cmd := "cp /tmp/foo " + filepath.Join(denied, "dest.md")
			Expect(guard.CheckCommand(cmd)).To(HaveOccurred())
		})

		It("denies a path passed to cat", func() {
			cmd := "cat " + filepath.Join(denied, "notes.md")
			Expect(guard.CheckCommand(cmd)).To(HaveOccurred())
		})

		It("denies a path passed in via redirection target", func() {
			cmd := "echo hi > " + filepath.Join(denied, "out.txt")
			Expect(guard.CheckCommand(cmd)).To(HaveOccurred())
		})

		It("denies a tilde-expanded path that resolves under the denied root", func() {
			home, _ := os.UserHomeDir()
			if home == "" {
				Skip("no HOME set")
			}
			tildeDenied := "/tmp/pathguard-home-fixture"
			tildeGuard := pathguard.New([]string{tildeDenied})
			os.Setenv("HOME", "/tmp")
			DeferCleanup(func() { os.Setenv("HOME", home) })

			cmd := "cat ~/pathguard-home-fixture/notes.md"
			Expect(tildeGuard.CheckCommand(cmd)).To(HaveOccurred())
		})
	})

	Describe("CheckCommand — cwd carve-out", func() {
		It("allows commands when cwd is inside the denied root", func() {
			cwdDenied, err := os.MkdirTemp("", "pathguard-cwd-*")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { os.RemoveAll(cwdDenied) })

			// The cwd carve-out matches when cwd is a strict subdirectory
			// of the denied root (HasPrefix(cwd, denied+sep)), so put a
			// nested dir under the denied root and chdir into it.
			nested := filepath.Join(cwdDenied, "subdir")
			Expect(os.MkdirAll(nested, 0o755)).To(Succeed())

			original, _ := os.Getwd()
			Expect(os.Chdir(nested)).To(Succeed())
			DeferCleanup(func() { _ = os.Chdir(original) })

			// macOS prefixes /tmp with /private; resolve to match how the
			// guard sees cwd. Use the resolved absolute path as the denied
			// root so the cwd carve-out matches.
			resolved, _ := filepath.EvalSymlinks(cwdDenied)
			if resolved == "" {
				resolved = cwdDenied
			}
			cwdGuard := pathguard.New([]string{resolved})

			cmd := "cat ./notes.md"
			Expect(cwdGuard.CheckCommand(cmd)).NotTo(HaveOccurred())
		})
	})

	Describe("CheckCommand — no denied roots", func() {
		It("is a no-op", func() {
			open := pathguard.New(nil)
			Expect(open.CheckCommand("cat " + filepath.Join(denied, "x.md"))).NotTo(HaveOccurred())
		})
	})

	// Slice A of the Tool-Scoped Permissions plan adds the *ForTool
	// variants that consult a PermissionsMatcher before falling
	// through to the legacy denied-roots semantics. Existing Check /
	// CheckCommand callers are unaffected — the legacy behaviour is
	// pinned by the cases above.
	Describe("CheckForTool — permissions matcher integration", func() {
		It("returns nil when the matcher explicitly allows the path, even under a denied root", func() {
			matcher := stubMatcher{decision: "allow", matched: true}
			g := pathguard.NewWithPermissions([]string{denied}, matcher)
			Expect(g.CheckForTool("read", filepath.Join(denied, "ok.md"))).NotTo(HaveOccurred())
		})

		It("returns an error when the matcher explicitly denies the path, even outside the denied roots", func() {
			matcher := stubMatcher{decision: "deny", matched: true}
			g := pathguard.NewWithPermissions(nil, matcher)
			Expect(g.CheckForTool("read", "/tmp/elsewhere/file.md")).To(HaveOccurred())
		})

		It("falls through to the legacy Check when the matcher has no opinion", func() {
			matcher := stubMatcher{matched: false}
			g := pathguard.NewWithPermissions([]string{denied}, matcher)
			err := g.CheckForTool("read", filepath.Join(denied, "still-blocked.md"))
			Expect(err).To(HaveOccurred())
		})

		It("collapses to legacy Check when tool is empty", func() {
			matcher := stubMatcher{decision: "allow", matched: true}
			g := pathguard.NewWithPermissions([]string{denied}, matcher)
			// empty tool name → matcher MUST be skipped → legacy denies
			err := g.CheckForTool("", filepath.Join(denied, "foo.md"))
			Expect(err).To(HaveOccurred())
		})

		It("collapses to legacy Check when no matcher is wired", func() {
			g := pathguard.New([]string{denied})
			err := g.CheckForTool("read", filepath.Join(denied, "foo.md"))
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("CheckCommandForTool — permissions matcher integration", func() {
		It("denies a tokenised path when the matcher denies it", func() {
			matcher := stubMatcher{decision: "deny", matched: true}
			g := pathguard.NewWithPermissions(nil, matcher)
			cmd := "cat /tmp/anywhere/secret.txt"
			Expect(g.CheckCommandForTool("bash", cmd)).To(HaveOccurred())
		})

		It("allows a tokenised path under a legacy-denied root when the matcher explicitly allows it", func() {
			matcher := stubMatcher{decision: "allow", matched: true}
			g := pathguard.NewWithPermissions([]string{denied}, matcher)
			cmd := "cat " + filepath.Join(denied, "notes.md")
			Expect(g.CheckCommandForTool("bash", cmd)).NotTo(HaveOccurred())
		})

		It("falls through to legacy denied-roots when the matcher has no opinion", func() {
			matcher := stubMatcher{matched: false}
			g := pathguard.NewWithPermissions([]string{denied}, matcher)
			cmd := "cat " + filepath.Join(denied, "notes.md")
			Expect(g.CheckCommandForTool("bash", cmd)).To(HaveOccurred())
		})

		It("preserves the quote-aware tokeniser — quoted mentions of the denied path do not trip the matcher", func() {
			// stub counts how many tokens the matcher saw — quoted text
			// is discarded by tokenize so the matcher must see zero
			// path-shaped tokens here.
			counter := &countingMatcher{}
			g := pathguard.NewWithPermissions(nil, counter)
			cmd := `echo "TODO: ` + denied + `/notes.md is internal"`
			Expect(g.CheckCommandForTool("bash", cmd)).NotTo(HaveOccurred())
			Expect(counter.calls).To(Equal(0))
		})

		It("collapses to legacy CheckCommand when tool is empty", func() {
			matcher := stubMatcher{decision: "deny", matched: true}
			g := pathguard.NewWithPermissions(nil, matcher)
			// empty tool name → matcher skipped → legacy CheckCommand
			// has no denied roots configured so should succeed.
			Expect(g.CheckCommandForTool("", "cat /tmp/foo.md")).NotTo(HaveOccurred())
		})
	})
})

// Slice B of the Tool-Scoped Permissions plan wires permissions.yaml
// through to per-tool Check calls in production (app.buildPathGuard
// constructs the Guard via NewWithPermissions, and the seven file /
// command callers route through *ForTool). The Slice A specs above
// cover the sibling methods via stubMatcher in isolation; the cases
// below pin the integration of the REAL *config.Permissions matcher
// with the Guard's *ForTool decision flow, plus the backwards-compat
// fall-through that legacy callers depend on.
//
// Numbering picks up from Slice A's existing 24 specs above.
var _ = Describe("Pathguard — Slice B integration with *config.Permissions", func() {
	const vaultRoot = "/vault"

	Describe("CheckForTool with a real *config.Permissions matcher", func() {
		// Case 7
		It("allows a write under /vault/** when permissions.yaml grants write.allow=[/vault/**]", func() {
			perms := &config.Permissions{
				Version: 1,
				Tools: map[string]config.ToolRules{
					"write": {Allow: []string{"/vault/**"}},
				},
			}
			g := pathguard.NewWithPermissions([]string{vaultRoot}, perms)
			Expect(g.CheckForTool("write", "/vault/file.md")).NotTo(HaveOccurred())
		})

		// Case 8 — deny-wins-over-allow at the real matcher layer
		It("denies a specific file even when write.allow=[/vault/**] grants the parent, because write.deny wins", func() {
			perms := &config.Permissions{
				Version: 1,
				Tools: map[string]config.ToolRules{
					"write": {
						Allow: []string{"/vault/**"},
						Deny:  []string{"/vault/foo.md"},
					},
				},
			}
			g := pathguard.NewWithPermissions([]string{vaultRoot}, perms)
			err := g.CheckForTool("write", "/vault/foo.md")
			Expect(err).To(HaveOccurred())
		})

		// Case 9 — tool not in permissions.yaml falls through to legacy DENY
		It("falls through to the legacy denied-roots check for a tool with no permissions.yaml entry", func() {
			perms := &config.Permissions{
				Version: 1,
				Tools: map[string]config.ToolRules{
					// only "write" is configured; "grep" has no entry
					"write": {Allow: []string{"/vault/**"}},
				},
			}
			g := pathguard.NewWithPermissions([]string{vaultRoot}, perms)
			err := g.CheckForTool("grep", "/vault/file.md")
			Expect(err).To(HaveOccurred())
		})

		// Case 10 — nil permissions (file absent) collapses to legacy DENY,
		// proving the backwards-compat code path is selected when
		// LoadPermissions returns (nil, nil).
		It("collapses to the legacy DENY when LoadPermissions returned nil (no permissions.yaml on disk)", func() {
			// Pass an explicit nil *config.Permissions to mirror the
			// app.buildPathGuard branch where LoadPermissions returns
			// (nil, nil) for a missing file.
			var perms *config.Permissions
			g := pathguard.NewWithPermissions([]string{vaultRoot}, perms)
			err := g.CheckForTool("write", "/vault/file.md")
			Expect(err).To(HaveOccurred())
		})

		// Case 11 — proves the `**` doublestar wiring all the way through
		// pathguard → config.Permissions → doublestar.PathMatch.
		It("matches recursive ** depth through the real config matcher (deep subdir under /vault/**)", func() {
			perms := &config.Permissions{
				Version: 1,
				Tools: map[string]config.ToolRules{
					"write": {Allow: []string{"/vault/**"}},
				},
			}
			g := pathguard.NewWithPermissions([]string{vaultRoot}, perms)
			Expect(g.CheckForTool("write", "/vault/subdir/deep/file.md")).NotTo(HaveOccurred())
		})
	})

	Describe("CheckCommandForTool with a real *config.Permissions matcher", func() {
		// Case 12 — proves the command tokeniser hands each path-shaped
		// token to the real matcher and that an allow short-circuits the
		// legacy denied-roots check.
		It("allows `cat /vault/file.md` when bash.allow=[/vault/**] grants the path, despite the legacy denied root", func() {
			perms := &config.Permissions{
				Version: 1,
				Tools: map[string]config.ToolRules{
					"bash": {Allow: []string{"/vault/**"}},
				},
			}
			g := pathguard.NewWithPermissions([]string{vaultRoot}, perms)
			Expect(g.CheckCommandForTool("bash", "cat /vault/file.md")).NotTo(HaveOccurred())
		})
	})
})

// stubMatcher returns a fixed verdict for every (tool, path) tuple.
type stubMatcher struct {
	decision string
	matched  bool
}

func (s stubMatcher) Match(string, string) (string, bool) {
	return s.decision, s.matched
}

// countingMatcher records the number of Match calls so tests can
// assert that quoted tokens never reach the matcher.
type countingMatcher struct {
	calls int
}

func (c *countingMatcher) Match(string, string) (string, bool) {
	c.calls++
	return "", false
}
