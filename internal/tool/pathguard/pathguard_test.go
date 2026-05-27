package pathguard_test

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/permissionmode"
	"github.com/baphled/flowstate/internal/session"
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
			Expect(g.CheckForTool(context.Background(), "read", filepath.Join(denied, "ok.md"))).NotTo(HaveOccurred())
		})

		It("returns an error when the matcher explicitly denies the path, even outside the denied roots", func() {
			matcher := stubMatcher{decision: "deny", matched: true}
			g := pathguard.NewWithPermissions(nil, matcher)
			Expect(g.CheckForTool(context.Background(), "read", "/tmp/elsewhere/file.md")).To(HaveOccurred())
		})

		It("falls through to the legacy Check when the matcher has no opinion", func() {
			matcher := stubMatcher{matched: false}
			g := pathguard.NewWithPermissions([]string{denied}, matcher)
			err := g.CheckForTool(context.Background(), "read", filepath.Join(denied, "still-blocked.md"))
			Expect(err).To(HaveOccurred())
		})

		It("collapses to legacy Check when tool is empty", func() {
			matcher := stubMatcher{decision: "allow", matched: true}
			g := pathguard.NewWithPermissions([]string{denied}, matcher)
			// empty tool name → matcher MUST be skipped → legacy denies
			err := g.CheckForTool(context.Background(), "", filepath.Join(denied, "foo.md"))
			Expect(err).To(HaveOccurred())
		})

		It("collapses to legacy Check when no matcher is wired", func() {
			g := pathguard.New([]string{denied})
			err := g.CheckForTool(context.Background(), "read", filepath.Join(denied, "foo.md"))
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("CheckCommandForTool — permissions matcher integration", func() {
		It("denies a tokenised path when the matcher denies it", func() {
			matcher := stubMatcher{decision: "deny", matched: true}
			g := pathguard.NewWithPermissions(nil, matcher)
			cmd := "cat /tmp/anywhere/secret.txt"
			Expect(g.CheckCommandForTool(context.Background(), "bash", cmd)).To(HaveOccurred())
		})

		It("allows a tokenised path under a legacy-denied root when the matcher explicitly allows it", func() {
			matcher := stubMatcher{decision: "allow", matched: true}
			g := pathguard.NewWithPermissions([]string{denied}, matcher)
			cmd := "cat " + filepath.Join(denied, "notes.md")
			Expect(g.CheckCommandForTool(context.Background(), "bash", cmd)).NotTo(HaveOccurred())
		})

		It("falls through to legacy denied-roots when the matcher has no opinion", func() {
			matcher := stubMatcher{matched: false}
			g := pathguard.NewWithPermissions([]string{denied}, matcher)
			cmd := "cat " + filepath.Join(denied, "notes.md")
			Expect(g.CheckCommandForTool(context.Background(), "bash", cmd)).To(HaveOccurred())
		})

		It("preserves the quote-aware tokeniser — quoted mentions of the denied path do not trip the matcher", func() {
			// stub counts how many tokens the matcher saw — quoted text
			// is discarded by tokenize so the matcher must see zero
			// path-shaped tokens here.
			counter := &countingMatcher{}
			g := pathguard.NewWithPermissions(nil, counter)
			cmd := `echo "TODO: ` + denied + `/notes.md is internal"`
			Expect(g.CheckCommandForTool(context.Background(), "bash", cmd)).NotTo(HaveOccurred())
			Expect(counter.calls).To(Equal(0))
		})

		It("collapses to legacy CheckCommand when tool is empty", func() {
			matcher := stubMatcher{decision: "deny", matched: true}
			g := pathguard.NewWithPermissions(nil, matcher)
			// empty tool name → matcher skipped → legacy CheckCommand
			// has no denied roots configured so should succeed.
			Expect(g.CheckCommandForTool(context.Background(), "", "cat /tmp/foo.md")).NotTo(HaveOccurred())
		})
	})

	// Permission Modes plan §4 Slice 1 (May 2026). The new Cases 13–17
	// pin the ctx-aware behaviour the *ForTool methods now expose:
	//
	//   - YOLO short-circuits to PASS at the top of both methods,
	//     ahead of any matcher consultation or denied-roots check.
	//   - Plan and Default leave the legacy decision flow alone
	//     (Plan-mode enforcement is engine-side, not pathguard).
	//   - A missing mode binding (or nil ctx) canonicalises to
	//     Default via permissionmode.FromContext.
	Describe("CheckForTool — permission-mode ctx integration (Slice 1)", func() {
		// Case 13 — YOLO short-circuits even with a permissions
		// matcher actively denying the path.
		It("Case 13: returns nil under mode=yolo even when matcher would deny", func() {
			matcher := stubMatcher{decision: "deny", matched: true}
			g := pathguard.NewWithPermissions([]string{denied}, matcher)
			ctx := permissionmode.WithMode(context.Background(), permissionmode.ModeYolo)
			err := g.CheckForTool(ctx, "write", filepath.Join(denied, "foo.md"))
			Expect(err).NotTo(HaveOccurred(), "YOLO MUST bypass the permissions matcher and denied roots")
		})

		// Case 14 — Default mode preserves the pre-Slice-1 behaviour:
		// the matcher deny still fires.
		It("Case 14: denies under mode=default when matcher denies (current behaviour preserved)", func() {
			matcher := stubMatcher{decision: "deny", matched: true}
			g := pathguard.NewWithPermissions([]string{denied}, matcher)
			ctx := permissionmode.WithMode(context.Background(), permissionmode.ModeDefault)
			err := g.CheckForTool(ctx, "write", filepath.Join(denied, "foo.md"))
			Expect(err).To(HaveOccurred(), "Default MUST defer to the matcher; YOLO is the only bypass")
		})

		// Case 16 — Plan mode does NOT short-circuit pathguard. The
		// engine-side schema filter is what enforces Plan; pathguard
		// MUST treat Plan identically to Default so a future bug that
		// confuses the two cannot silently leak write access.
		It("Case 16: denies under mode=plan when matcher denies (Plan != YOLO)", func() {
			matcher := stubMatcher{decision: "deny", matched: true}
			g := pathguard.NewWithPermissions([]string{denied}, matcher)
			ctx := permissionmode.WithMode(context.Background(), permissionmode.ModePlan)
			err := g.CheckForTool(ctx, "write", filepath.Join(denied, "foo.md"))
			Expect(err).To(HaveOccurred(), "Plan MUST behave like Default in pathguard — Plan enforcement is engine-side")
		})

		// Case 17 — missing mode binding canonicalises to Default.
		// Uses a bare context.Background() with no WithMode stamp.
		It("Case 17: denies when ctx carries no mode binding (defaults to Default)", func() {
			matcher := stubMatcher{decision: "deny", matched: true}
			g := pathguard.NewWithPermissions([]string{denied}, matcher)
			err := g.CheckForTool(context.Background(), "write", filepath.Join(denied, "foo.md"))
			Expect(err).To(HaveOccurred(), "missing mode binding MUST canonicalise to Default, not YOLO")
		})

		// Belt-and-braces: an explicit empty mode also defaults safely.
		It("denies when ctx carries empty-string mode (canonicalised to Default)", func() {
			matcher := stubMatcher{decision: "deny", matched: true}
			g := pathguard.NewWithPermissions([]string{denied}, matcher)
			// WithMode("") short-circuits to the same ctx, so this
			// exercises the FromContext "" → default branch directly.
			ctx := permissionmode.WithMode(context.Background(), "")
			err := g.CheckForTool(ctx, "write", filepath.Join(denied, "foo.md"))
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("CheckCommandForTool — permission-mode ctx integration (Slice 1)", func() {
		// Case 15 — YOLO short-circuits the command-token scan too.
		It("Case 15: returns nil under mode=yolo for a denied bash command", func() {
			g := pathguard.New([]string{denied})
			ctx := permissionmode.WithMode(context.Background(), permissionmode.ModeYolo)
			cmd := "cat " + filepath.Join(denied, "secrets.md")
			err := g.CheckCommandForTool(ctx, "bash", cmd)
			Expect(err).NotTo(HaveOccurred(), "YOLO MUST bypass the per-token denied-roots scan")
		})

		// Pairing pin: Default mode still denies the same command.
		It("denies the same bash command under mode=default", func() {
			g := pathguard.New([]string{denied})
			ctx := permissionmode.WithMode(context.Background(), permissionmode.ModeDefault)
			cmd := "cat " + filepath.Join(denied, "secrets.md")
			err := g.CheckCommandForTool(ctx, "bash", cmd)
			Expect(err).To(HaveOccurred())
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
			Expect(g.CheckForTool(context.Background(), "write", "/vault/file.md")).NotTo(HaveOccurred())
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
			err := g.CheckForTool(context.Background(), "write", "/vault/foo.md")
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
			err := g.CheckForTool(context.Background(), "grep", "/vault/file.md")
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
			err := g.CheckForTool(context.Background(), "write", "/vault/file.md")
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
			Expect(g.CheckForTool(context.Background(), "write", "/vault/subdir/deep/file.md")).NotTo(HaveOccurred())
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
			Expect(g.CheckCommandForTool(context.Background(), "bash", "cat /vault/file.md")).NotTo(HaveOccurred())
		})
	})
})

// Plan-Mode Output Directory plan (May 2026) §3 Slice 1 wires a
// plan_output_dir overlay into pathguard. Under ModePlan, file-mutation
// tools (write/edit/multiedit/apply_patch) are constrained to paths
// under planOutputDir regardless of the matcher's allow rules; the
// matcher's deny rules still apply so deny-wins-over-allow precedence
// holds (config.Permissions.Match permissions.go:127-129).
var _ = Describe("Pathguard — Plan-Mode Output Directory overlay (Slice 1)", func() {
	var (
		planOutputDir string
		denied        string
	)

	BeforeEach(func() {
		planOutputDir = "/tmp/pathguard-plan-out"
		denied = "/tmp/pathguard-plan-vault"
	})

	planCtx := func() context.Context {
		return permissionmode.WithMode(context.Background(), permissionmode.ModePlan)
	}

	Describe("CheckForTool under Plan mode", func() {
		It("ALLOWS write under plan_output_dir", func() {
			perms := &config.Permissions{
				Version: 1,
				Tools: map[string]config.ToolRules{
					// Operator-set rule for the vault; the overlay
					// MUST replace this under Plan mode.
					"write": {Allow: []string{denied + "/**"}},
				},
			}
			g := pathguard.NewWithPermissionsAndPlanOutputDir([]string{denied}, perms, planOutputDir)
			err := g.CheckForTool(planCtx(), "write", filepath.Join(planOutputDir, "turn.md"))
			Expect(err).NotTo(HaveOccurred(),
				"Plan mode MUST allow write under plan_output_dir — the overlay's positive case")
		})

		It("DENIES write at a path outside plan_output_dir but inside the vault (overlay overrides operator allow)", func() {
			perms := &config.Permissions{
				Version: 1,
				Tools: map[string]config.ToolRules{
					"write": {Allow: []string{denied + "/**"}},
				},
			}
			g := pathguard.NewWithPermissionsAndPlanOutputDir([]string{denied}, perms, planOutputDir)
			err := g.CheckForTool(planCtx(), "write", filepath.Join(denied, "doc.md"))
			Expect(err).To(HaveOccurred(),
				"Plan mode MUST replace the operator allow with 'must be under plan_output_dir' — vault paths outside plan_output_dir are denied")
		})

		It("DENIES write at a path outside the vault and outside plan_output_dir", func() {
			perms := &config.Permissions{
				Version: 1,
				Tools: map[string]config.ToolRules{
					"write": {Allow: []string{denied + "/**"}},
				},
			}
			g := pathguard.NewWithPermissionsAndPlanOutputDir([]string{denied}, perms, planOutputDir)
			err := g.CheckForTool(planCtx(), "write", "/tmp/escape.md")
			Expect(err).To(HaveOccurred(),
				"Plan mode MUST deny writes outside plan_output_dir even when no legacy denied root applies")
		})

		It("DENIES write at a path under plan_output_dir that the matcher ALSO denies (deny wins over plan_output_dir allow)", func() {
			// Plan-Mode Output Directory plan §3 Slice 1 nit 4: the
			// matcher's DENY rules MUST still apply under the overlay.
			// config.Permissions.Match returns "deny" first when both
			// match (permissions.go:127-129), so a deny rule pointing
			// at .obsidian inside plan_output_dir keeps Plan-mode
			// writes from clobbering the operator's metadata directory.
			obsidian := filepath.Join(planOutputDir, ".obsidian", "workspace.json")
			perms := &config.Permissions{
				Version: 1,
				Tools: map[string]config.ToolRules{
					"write": {Deny: []string{planOutputDir + "/.obsidian/**"}},
				},
			}
			g := pathguard.NewWithPermissionsAndPlanOutputDir([]string{denied}, perms, planOutputDir)
			err := g.CheckForTool(planCtx(), "write", obsidian)
			Expect(err).To(HaveOccurred(),
				"matcher deny MUST still fire under the overlay — deny-wins-over-allow precedence holds (permissions.go:127-129)")
		})

		It("DENIES write when plan_output_dir is empty (fails closed)", func() {
			// Bootstrap mkdir failure → empty plan_output_dir → Plan-
			// mode write fails closed with an explicit denial rather
			// than silently succeeding.
			g := pathguard.NewWithPermissionsAndPlanOutputDir([]string{denied}, nil, "")
			err := g.CheckForTool(planCtx(), "write", filepath.Join(planOutputDir, "turn.md"))
			Expect(err).To(HaveOccurred(),
				"empty plan_output_dir MUST fail closed under Plan mode — silent allow would defeat the overlay")
		})

		It("applies the overlay to all four planScopedTools", func() {
			perms := &config.Permissions{Version: 1}
			g := pathguard.NewWithPermissionsAndPlanOutputDir([]string{denied}, perms, planOutputDir)
			ctx := planCtx()
			for _, tool := range []string{"write", "edit", "multiedit", "apply_patch"} {
				// Inside plan_output_dir → allow.
				inErr := g.CheckForTool(ctx, tool, filepath.Join(planOutputDir, "x.md"))
				Expect(inErr).NotTo(HaveOccurred(), "%q MUST be permitted under plan_output_dir", tool)
				// Outside plan_output_dir → deny.
				outErr := g.CheckForTool(ctx, tool, "/tmp/escape.md")
				Expect(outErr).To(HaveOccurred(), "%q MUST be denied outside plan_output_dir", tool)
			}
		})

		It("does NOT apply the overlay to read (read is not in planScopedTools)", func() {
			perms := &config.Permissions{
				Version: 1,
				Tools: map[string]config.ToolRules{
					"read": {Allow: []string{denied + "/**"}},
				},
			}
			g := pathguard.NewWithPermissionsAndPlanOutputDir([]string{denied}, perms, planOutputDir)
			// read under the operator's vault — matcher allow takes
			// effect normally, the Plan-mode overlay never fires.
			err := g.CheckForTool(planCtx(), "read", filepath.Join(denied, "doc.md"))
			Expect(err).NotTo(HaveOccurred(),
				"read MUST follow the standard matcher path under Plan mode — the overlay is scoped to file-mutation tools")
		})
	})

	Describe("CheckForTool under Default mode", func() {
		It("does NOT apply the overlay — Default mode follows the standard matcher path", func() {
			// Pin the negative side: outside Plan mode, the same path
			// that Plan would deny is permitted by the operator's
			// allow rule (write.allow = vault/**).
			perms := &config.Permissions{
				Version: 1,
				Tools: map[string]config.ToolRules{
					"write": {Allow: []string{denied + "/**"}},
				},
			}
			g := pathguard.NewWithPermissionsAndPlanOutputDir([]string{denied}, perms, planOutputDir)
			ctx := permissionmode.WithMode(context.Background(), permissionmode.ModeDefault)
			err := g.CheckForTool(ctx, "write", filepath.Join(denied, "doc.md"))
			Expect(err).NotTo(HaveOccurred(),
				"Default mode MUST defer to the operator's allow — the overlay fires ONLY under Plan")
		})
	})
})

// Permission Mode ModeAskUser Extension plan (May 2026) Slice 2.
//
// These specs pin the PermissionPrompter integration on Guard.
// CheckForTool and CheckCommandForTool under ModeAskUser escalate a
// denial to the prompter and apply the returned grant:
//
//   - GrantOnce → return nil, no persistence
//   - GrantSession → return nil + remember (tool, resource) for the
//     session so the second call skips the prompter
//   - GrantForever → return nil + remember (Slice 4 will wire the
//     permissions.yaml writer; Slice 2 treats Forever as Session)
//   - GrantDeny → return the original access-denied error
//
// Floor preserved: prompter == nil OR mode != ModeAskUser falls back
// to today's binary deny semantics (memory:
// project_flowstate_agent_tools_fail_closed).
var _ = Describe("Pathguard — ModeAskUser PermissionPrompter (Slice 2)", func() {
	var (
		denied   string
		askUser  = func() context.Context {
			ctx := context.Background()
			ctx = permissionmode.WithMode(ctx, permissionmode.ModeAskUser)
			return ctx
		}
	)

	BeforeEach(func() {
		denied = "/tmp/pathguard-askuser-vault"
	})

	Describe("CheckForTool under ModeAskUser", func() {
		It("returns nil when the prompter grants Once (the call proceeds)", func() {
			matcher := stubMatcher{decision: "deny", matched: true}
			g := pathguard.NewWithPermissions([]string{denied}, matcher)
			prompter := &spyPrompter{grant: pathguard.PermissionGrant{Scope: pathguard.GrantOnce}}
			g.SetPermissionPrompter(prompter)

			err := g.CheckForTool(askUser(), "write", filepath.Join(denied, "foo.md"))

			Expect(err).NotTo(HaveOccurred(),
				"GrantOnce MUST resume the suspended call — the operator clicked Allow Once on the inline prompt")
			Expect(prompter.calls).To(Equal(1),
				"the prompter MUST be consulted exactly once on the denial path under ModeAskUser")
			Expect(prompter.lastReq.ToolName).To(Equal("write"))
			Expect(prompter.lastReq.Resource).To(ContainSubstring("foo.md"))
			Expect(prompter.lastReq.Mode).To(Equal(permissionmode.ModeAskUser),
				"the PermissionRequest payload MUST stamp the active mode so the prompter (app.go) can include it in the bus event")
			Expect(prompter.lastReq.DenialReason).NotTo(BeEmpty(),
				"the prompter receives the original denial reason so the UI can render the why")
		})

		It("returns the access-denied error when the prompter denies", func() {
			matcher := stubMatcher{decision: "deny", matched: true}
			g := pathguard.NewWithPermissions([]string{denied}, matcher)
			prompter := &spyPrompter{grant: pathguard.PermissionGrant{Scope: pathguard.GrantDeny}}
			g.SetPermissionPrompter(prompter)

			err := g.CheckForTool(askUser(), "write", filepath.Join(denied, "foo.md"))

			Expect(err).To(HaveOccurred(),
				"GrantDeny MUST surface the original access-denied error — the model sees the IsError tool_result")
			Expect(err.Error()).To(ContainSubstring("access denied"))
		})

		It("returns nil AND remembers the resource on GrantSession — second call skips the prompter", func() {
			matcher := stubMatcher{decision: "deny", matched: true}
			g := pathguard.NewWithPermissions([]string{denied}, matcher)
			prompter := &spyPrompter{grant: pathguard.PermissionGrant{Scope: pathguard.GrantSession}}
			g.SetPermissionPrompter(prompter)

			ctx := context.WithValue(askUser(), session.IDKey{}, "sess-allow-once")
			path := filepath.Join(denied, "foo.md")

			err1 := g.CheckForTool(ctx, "write", path)
			err2 := g.CheckForTool(ctx, "write", path)

			Expect(err1).NotTo(HaveOccurred(),
				"GrantSession MUST permit the first call")
			Expect(err2).NotTo(HaveOccurred(),
				"GrantSession MUST permit the second call to the same (tool, resource) pair under the same session")
			Expect(prompter.calls).To(Equal(1),
				"the prompter MUST be consulted ONCE — the second call hits the per-session in-memory allow set, not the prompter; this is the load-bearing 'session grant persists for the session lifetime' contract")
		})

		It("falls back to binary deny when prompter is nil under ModeAskUser (regression guard for app.go wiring omission)", func() {
			// Plan §4 Slice 2 risk note: 'The PermissionPrompter field on
			// Guard is nilable; existing constructors that don't inject
			// one behave identically to today.' This spec pins that
			// invariant — a half-wired ModeAskUser (mode dial flipped
			// but prompter not injected) MUST NOT silently grant.
			matcher := stubMatcher{decision: "deny", matched: true}
			g := pathguard.NewWithPermissions([]string{denied}, matcher)
			// SetPermissionPrompter intentionally NOT called.

			err := g.CheckForTool(askUser(), "write", filepath.Join(denied, "foo.md"))

			Expect(err).To(HaveOccurred(),
				"a Guard without a prompter MUST preserve the pre-Slice-2 binary deny — fail closed, never fall through to permit")
		})

		It("does NOT consult the prompter under ModeDefault (regression guard — Default unchanged)", func() {
			matcher := stubMatcher{decision: "deny", matched: true}
			g := pathguard.NewWithPermissions([]string{denied}, matcher)
			prompter := &spyPrompter{grant: pathguard.PermissionGrant{Scope: pathguard.GrantOnce}}
			g.SetPermissionPrompter(prompter)

			ctx := permissionmode.WithMode(context.Background(), permissionmode.ModeDefault)
			err := g.CheckForTool(ctx, "write", filepath.Join(denied, "foo.md"))

			Expect(err).To(HaveOccurred(),
				"Default mode MUST preserve the binary deny — the prompter is opt-in via ModeAskUser, plan §2 acceptance bullet 2")
			Expect(prompter.calls).To(Equal(0),
				"Default mode MUST NOT consult the prompter — silent fall-through to GrantOnce would defeat the closed mode vocabulary")
		})

		It("does NOT escalate when there is no denial under ModeAskUser (allow paths bypass the prompter)", func() {
			matcher := stubMatcher{decision: "allow", matched: true}
			g := pathguard.NewWithPermissions([]string{denied}, matcher)
			prompter := &spyPrompter{grant: pathguard.PermissionGrant{Scope: pathguard.GrantDeny}}
			g.SetPermissionPrompter(prompter)

			err := g.CheckForTool(askUser(), "write", filepath.Join(denied, "foo.md"))

			Expect(err).NotTo(HaveOccurred(),
				"matcher allow MUST short-circuit BEFORE the prompter — the call is permitted by the operator's existing config, no prompt needed")
			Expect(prompter.calls).To(Equal(0))
		})
	})

	Describe("CheckCommandForTool under ModeAskUser", func() {
		It("escalates a denied bash command and applies GrantOnce", func() {
			g := pathguard.New([]string{denied})
			prompter := &spyPrompter{grant: pathguard.PermissionGrant{Scope: pathguard.GrantOnce}}
			g.SetPermissionPrompter(prompter)

			cmd := "cat " + filepath.Join(denied, "secrets.md")
			err := g.CheckCommandForTool(askUser(), "bash", cmd)

			Expect(err).NotTo(HaveOccurred(),
				"GrantOnce MUST resume the bash dispatch — the operator approved the specific resource")
			Expect(prompter.calls).To(Equal(1))
			Expect(prompter.lastReq.ToolName).To(Equal("bash"))
		})

		It("returns the original error when the prompter denies a bash command", func() {
			g := pathguard.New([]string{denied})
			prompter := &spyPrompter{grant: pathguard.PermissionGrant{Scope: pathguard.GrantDeny}}
			g.SetPermissionPrompter(prompter)

			cmd := "cat " + filepath.Join(denied, "secrets.md")
			err := g.CheckCommandForTool(askUser(), "bash", cmd)

			Expect(err).To(HaveOccurred(),
				"GrantDeny on a bash command MUST surface the existing 'command references protected path' error")
		})

		It("falls back to binary deny when prompter is nil under ModeAskUser (regression)", func() {
			g := pathguard.New([]string{denied})
			// No prompter wired.
			cmd := "cat " + filepath.Join(denied, "secrets.md")
			err := g.CheckCommandForTool(askUser(), "bash", cmd)

			Expect(err).To(HaveOccurred(),
				"missing prompter under ModeAskUser MUST preserve the pre-Slice-2 binary deny on bash command escalation")
		})
	})

	Describe("ClearSessionAllow", func() {
		It("drops the per-session allow set so a subsequent call re-prompts", func() {
			matcher := stubMatcher{decision: "deny", matched: true}
			g := pathguard.NewWithPermissions([]string{denied}, matcher)
			prompter := &spyPrompter{grant: pathguard.PermissionGrant{Scope: pathguard.GrantSession}}
			g.SetPermissionPrompter(prompter)

			ctx := context.WithValue(askUser(), session.IDKey{}, "sess-clear-test")
			path := filepath.Join(denied, "foo.md")
			Expect(g.CheckForTool(ctx, "write", path)).NotTo(HaveOccurred())
			g.ClearSessionAllow("sess-clear-test")

			// Second call after clear MUST consult the prompter again —
			// the session lifecycle ended (e.g. session.ended fired) so
			// the previous grant is no longer valid.
			err := g.CheckForTool(ctx, "write", path)
			Expect(err).NotTo(HaveOccurred(), "prompter still grants — but the cleared-allow path forces a fresh consultation")
			Expect(prompter.calls).To(Equal(2),
				"ClearSessionAllow MUST drop the in-memory allow set so a fresh call re-prompts; without this, session lifecycle leaks become silent")
		})
	})
})

// spyPrompter is the test seam for the PermissionPrompter contract.
// Returns a fixed grant on every RequestPermission call and records
// the call count + last seen request for assertions.
type spyPrompter struct {
	grant   pathguard.PermissionGrant
	calls   int
	lastReq pathguard.PermissionRequest
}

func (s *spyPrompter) RequestPermission(_ context.Context, req pathguard.PermissionRequest) pathguard.PermissionGrant {
	s.calls++
	s.lastReq = req
	return s.grant
}

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
