package app

import (
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func mustHook(err error) {
	if err != nil {
		panic(err)
	}
}

func putHookMode(dir, name, content string, mode os.FileMode) string {
	mustHook(os.MkdirAll(dir, 0o700))
	path := filepath.Join(dir, name)
	mustHook(os.WriteFile(path, []byte(content), mode))
	mustHook(os.Chmod(path, mode))
	return path
}

func putHookFile(dir, name, content string) string { return putHookMode(dir, name, content, 0o600) }

func hookFileWith(hooks ...string) string {
	return `{"version":1,"hooks":[` + strings.Join(hooks, ",") + `]}`
}

var _ = Describe("Hook loading and scope", func() {
	var userDir, projectRoot string

	BeforeEach(func() {
		config := GinkgoT().TempDir()
		for _, env := range []string{"XDG_CONFIG_HOME", "HOME", "APPDATA"} {
			GinkgoT().Setenv(env, config)
		}
		base, err := os.UserConfigDir()
		mustHook(err)
		userDir = filepath.Join(base, "flowstate")
		projectRoot = GinkgoT().TempDir()
	})

	load := func(scope HookScope) (LoadedHooks, error) {
		return LoadHooks(HooksRequest{Scope: scope, ProjectRoot: projectRoot, Accepted: allHookPoints()})
	}
	ids := func(l LoadedHooks) []string {
		out := []string{}
		for _, h := range l.Hooks {
			out = append(out, h.ID)
		}
		return out
	}
	loadIDs := func(scope HookScope) []string {
		got, err := load(scope)
		mustHook(err)
		return ids(got)
	}
	putUser := func(hooks ...string) {
		putHookScript(userDir, "x.sh")
		putHookFile(userDir, "hooks.json", hookFileWith(hooks...))
	}
	putProject := func(hooks ...string) {
		dir := filepath.Join(projectRoot, ".flowstate")
		putHookScript(dir, "x.sh")
		putHookFile(dir, "hooks.json", hookFileWith(hooks...))
	}

	Describe("ParseHookScope", func() {
		DescribeTable("accepts",
			func(in string, want HookScope) {
				got, err := ParseHookScope(in)
				mustHook(err)
				Expect(got).To(Equal(want))
			},
			Entry("the empty string as user", "", HookScopeUser),
			Entry("user", "user", HookScopeUser),
			Entry("project", "project", HookScopeProject),
			Entry("both", "both", HookScopeBoth),
			Entry("none", "none", HookScopeNone),
		)

		It("refuses another value as a usage error", func() {
			_, err := ParseHookScope("all")
			Expect(err).To(MatchError(ErrUsage))
		})
	})

	Describe("the scope matrix for files", func() {
		BeforeEach(func() {
			putUser(cmdHook("from-user", "tool.before", ""))
			putProject(cmdHook("from-project", "tool.before", ""))
		})

		DescribeTable("loads",
			func(scope HookScope, want []string) { Expect(loadIDs(scope)).To(Equal(want)) },
			Entry("only the user file under user", HookScopeUser, []string{"from-user"}),
			Entry("only the project file under project", HookScopeProject, []string{"from-project"}),
			Entry("user then project under both", HookScopeBoth, []string{"from-user", "from-project"}),
			Entry("nothing under none", HookScopeNone, []string{}),
		)

		It("does not read a project file the scope does not select", func() {
			putHookFile(filepath.Join(projectRoot, ".flowstate"), "hooks.json", "not json")
			Expect(loadIDs(HookScopeUser)).To(Equal([]string{"from-user"}))
		})
	})

	It("loads no hooks when neither file exists", func() {
		Expect(loadIDs(HookScopeBoth)).To(BeEmpty())
	})

	It("finds the project file under the project root, not another directory", func() {
		putHookFile(filepath.Join(GinkgoT().TempDir(), ".flowstate"), "hooks.json", `{"version":1,"hooks":[]}`)
		putProject(cmdHook("rooted", "tool.before", ""))
		Expect(loadIDs(HookScopeProject)).To(Equal([]string{"rooted"}))
	})

	It("loads the empty-root project file from the process working directory", func() {
		putProject(cmdHook("cwd-rooted", "tool.before", ""))
		cwd, err := os.Getwd()
		mustHook(err)
		mustHook(os.Chdir(projectRoot))
		DeferCleanup(func() { mustHook(os.Chdir(cwd)) })
		got, err := LoadHooks(HooksRequest{Scope: HookScopeProject, Accepted: allHookPoints()})
		mustHook(err)
		Expect(ids(got)).To(Equal([]string{"cwd-rooted"}))
	})

	DescribeTable("rejects a scope that is not one of the four",
		func(scope HookScope, want error) {
			putUser(cmdHook("a", "tool.before", ""))
			_, err := load(scope)
			Expect(err).To(MatchError(want))
		},
		Entry("the empty scope (not parsed)", HookScope(""), ErrHookScopeInvalid),
		Entry("an unknown scope", HookScope("bogus"), ErrHookScopeInvalid),
		Entry("a differently cased scope", HookScope("User"), ErrHookScopeInvalid),
		Entry("and reports it as config_invalid", HookScope(""), ErrConfigInvalid),
	)

	It("refuses an id declared in both files under both, naming both files", func() {
		putUser(cmdHook("same", "tool.before", ""))
		putProject(cmdHook("same", "tool.before", ""))
		_, err := load(HookScopeBoth)
		Expect(err).To(MatchError(And(ContainSubstring(userDir), ContainSubstring(projectRoot))))
	})

	It("loads a duplicate id when the scope selects only one file", func() {
		putUser(cmdHook("same", "tool.before", ""))
		putProject(cmdHook("same", "tool.before", ""))
		Expect(loadIDs(HookScopeUser)).To(Equal([]string{"same"}))
	})

	It("reports a file failure as config_invalid", func() {
		putHookFile(userDir, "hooks.json", `{"version":1,"hooks":[],"extra":1}`)
		_, err := load(HookScopeUser)
		Expect(err).To(MatchError(ErrConfigInvalid))
	})

	It("loads tool.before under the production accepted set", func() {
		putUser(cmdHook("a", "tool.before", ""))
		got, err := LoadHooks(HooksRequest{Scope: HookScopeUser, Accepted: AcceptedHooks()})
		mustHook(err)
		Expect(ids(got)).To(Equal([]string{"a"}))
	})

	It("loads transform: true under the production accepted set", func() {
		putUser(cmdHook("a", "tool.before", `"transform":true,`))
		got, err := LoadHooks(HooksRequest{Scope: HookScopeUser, Accepted: AcceptedHooks()})
		mustHook(err)
		Expect(got.Hooks[0].Transform).To(BeTrue())
	})

	It("loads tool.after under the production accepted set", func() {
		putUser(cmdHook("a", "tool.after", ""))
		got, err := LoadHooks(HooksRequest{Scope: HookScopeUser, Accepted: AcceptedHooks()})
		mustHook(err)
		Expect(ids(got)).To(Equal([]string{"a"}))
	})

	It("refuses a point outside the production accepted set", func() {
		putUser(cmdHook("a", "compaction.before", ""))
		_, err := LoadHooks(HooksRequest{Scope: HookScopeUser, Accepted: AcceptedHooks()})
		Expect(err).To(MatchError(ErrHookPointUnsupported))
	})

	It("refuses on_failure deny on tool.after under the production accepted set", func() {
		putUser(cmdHook("a", "tool.after", `"on_failure":"deny",`))
		_, err := LoadHooks(HooksRequest{Scope: HookScopeUser, Accepted: AcceptedHooks()})
		Expect(err).To(MatchError(ContainSubstring("nothing to deny")))
	})
})

var _ = Describe("LoadedHooks.Digest (ADR-012, composition pin)", func() {
	base := func() LoadedHooks {
		return LoadedHooks{Scope: HookScopeUser, Hooks: []Hook{
			{ID: "a", Point: HookToolBefore, Kind: HookKindCommand, Argv: []string{"./a.sh", "x"}, Executable: "/h/a.sh",
				TimeoutMS: 1000, OnFailure: HookOnFailureDeny},
			{ID: "b", Point: HookToolBefore, Kind: HookKindCommand, Argv: []string{"./b.sh"}, Executable: "/h/b.sh",
				TimeoutMS: 1000, OnFailure: HookOnFailureDeny},
		}}
	}

	It("is a hex sha256", func() { Expect(base().Digest()).To(MatchRegexp(`^[0-9a-f]{64}$`)) })

	It("is the same for the same set", func() { Expect(base().Digest()).To(Equal(base().Digest())) })

	It("is defined for the empty set, nil or not", func() {
		Expect(LoadedHooks{}.Digest()).To(Equal(LoadedHooks{Hooks: []Hook{}}.Digest()))
	})

	It("does not depend on where the hook was declared", func() {
		moved := base()
		moved.Hooks[0].Origin = "/elsewhere/hooks.json"
		Expect(moved.Digest()).To(Equal(base().Digest()))
	})

	DescribeTable("changes with what decides what runs",
		func(mutate func(*LoadedHooks)) {
			changed := base()
			mutate(&changed)
			Expect(changed.Digest()).NotTo(Equal(base().Digest()))
		},
		Entry("an id", func(l *LoadedHooks) { l.Hooks[0].ID = "z" }),
		Entry("a point", func(l *LoadedHooks) { l.Hooks[0].Point = HookToolAfter }),
		Entry("an argument", func(l *LoadedHooks) { l.Hooks[0].Argv[1] = "y" }),
		Entry("the resolved executable", func(l *LoadedHooks) { l.Hooks[0].Executable = "/other/a.sh" }),
		Entry("a matcher", func(l *LoadedHooks) { l.Hooks[0].Match = []string{"fs.write"} }),
		Entry("the failure policy", func(l *LoadedHooks) { l.Hooks[0].OnFailure = HookOnFailureAllow }),
		Entry("the order", func(l *LoadedHooks) { l.Hooks[0], l.Hooks[1] = l.Hooks[1], l.Hooks[0] }),
		Entry("a removed hook", func(l *LoadedHooks) { l.Hooks = l.Hooks[:1] }),
	)
})
