package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/store"
	"github.com/baphled/flowstate/internal/tool"
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

// exampleHooksDir is the shipped worked example (ADR-012 packet 7), relative to this package.
const exampleHooksDir = "../../docs/examples/hooks"

// exampleHookIDs is the example's effective set in file order, which is also its firing order.
var exampleHookIDs = []string{"guard-protected-paths", "audit-tool-calls", "stop-after-n-turns", "refuse-secret-prompt",
	"log-session-start", "log-session-end"}

// stageExampleHooks installs the example as its README tells an operator to: hooks.json as
// <user config>/flowstate/hooks.json (0600) and scripts/ beside it, each executable (0700). It returns the
// directory HOME points at, where the example's loggers write.
func stageExampleHooks() string {
	config := GinkgoT().TempDir()
	for _, env := range []string{"XDG_CONFIG_HOME", "HOME", "APPDATA"} {
		GinkgoT().Setenv(env, config)
	}
	base, err := os.UserConfigDir()
	mustHook(err)
	dir := filepath.Join(base, "flowstate")
	mustHook(os.MkdirAll(filepath.Join(dir, "scripts"), 0o700))
	putHookFile(dir, "hooks.json", exampleFile("hooks.json"))
	scripts, err := os.ReadDir(filepath.Join(exampleHooksDir, "scripts"))
	mustHook(err)
	for _, s := range scripts {
		putHookMode(filepath.Join(dir, "scripts"), s.Name(), exampleFile(filepath.Join("scripts", s.Name())), 0o700)
	}
	return config
}

// exampleFile reads one example file as text, with LF endings whatever the checkout's.
func exampleFile(name string) string {
	raw, err := os.ReadFile(filepath.Join(exampleHooksDir, filepath.FromSlash(name)))
	mustHook(err)
	return string(bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n")))
}

// loadExampleHooks loads the staged example as a root run does: the user file under the production set.
func loadExampleHooks() (LoadedHooks, error) {
	stageExampleHooks()
	return LoadHooks(HooksRequest{Scope: HookScopeUser, ProjectRoot: GinkgoT().TempDir(), Accepted: AcceptedHooks()})
}

// exampleLog is what the example's loggers wrote to name under dir, or "" when the file is not there yet.
func exampleLog(dir, name string) string {
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	mustHook(err)
	return string(raw)
}

// The sample tool arguments of the script specs: an ordinary write and a write the guard denies.
const (
	exampleWrite     = `{"path":"internal/app/x.go","content":"package app"}`
	exampleMigration = `{"path":"migrations/003_add_users.sql","content":"x"}`
)

// exampleGate is a root run's gate for the sample stdin documents: session sess-1, run 7, call call-1 in /work.
func exampleGate() *hookGate {
	return &hookGate{input: hookInput{session: "sess-1", runID: "7", cwd: "/work", callID: "call-1"}}
}

// exampleInvokedIDs is the ids of the hook.invoked events of the session's log, in order: what the gate ran.
// It is defined here rather than shared with the !windows hooksgate specs so this file builds on Windows too.
func exampleInvokedIDs(st *store.Store, id string) []string {
	events, err := st.Replay(context.Background(), id)
	mustHook(err)
	ids := []string{}
	for _, e := range events {
		if e.Type == TypeHookInvoked {
			ids = append(ids, payloadFieldText(e.PayloadJSON, "id"))
		}
	}
	return ids
}

var _ = Describe("The shipped hook example", func() {
	It("loads under the production accepted set", func() {
		loaded, err := loadExampleHooks()
		Expect(err).NotTo(HaveOccurred())
		got := []string{}
		for _, h := range loaded.Hooks {
			got = append(got, h.ID)
		}
		Expect(got).To(Equal(exampleHookIDs))
	})

	DescribeTable("wires each hook to the point its script is written for",
		func(id string, want HookPoint) {
			loaded, err := loadExampleHooks()
			mustHook(err)
			points := map[string]HookPoint{}
			for _, h := range loaded.Hooks {
				points[h.ID] = h.Point
			}
			Expect(points[id]).To(Equal(want))
		},
		Entry("guard-protected-paths", "guard-protected-paths", HookToolBefore),
		Entry("audit-tool-calls", "audit-tool-calls", HookToolAfter),
		Entry("stop-after-n-turns", "stop-after-n-turns", HookTurnAfter),
		Entry("refuse-secret-prompt", "refuse-secret-prompt", HookUserPrompt),
		Entry("log-session-start", "log-session-start", HookSessionStart),
		Entry("log-session-end", "log-session-end", HookSessionEnd),
	)

	if runtime.GOOS != "windows" { // the example scripts are POSIX sh
		Describe("the example scripts", func() {
			var home, dir string
			var hooks map[string]Hook

			BeforeEach(func() {
				home = stageExampleHooks()
				base, err := os.UserConfigDir()
				mustHook(err)
				dir = filepath.Join(base, "flowstate")
				loaded, err := LoadHooks(HooksRequest{Scope: HookScopeUser, ProjectRoot: GinkgoT().TempDir(),
					Accepted: AcceptedHooks()})
				mustHook(err)
				hooks = map[string]Hook{}
				for _, h := range loaded.Hooks {
					hooks[h.ID] = h
				}
			})

			// runArgv runs one example hook on stdin as the gate does (hookGate.run), through the real runner, with
			// arguments of the caller's choice; run gives it the ones hooks.json declares.
			runArgv := func(id string, stdin []byte, args ...string) tool.HookResult {
				h := hooks[id]
				return tool.HookRunner{}.Run(context.Background(), tool.HookCommand{
					Argv: append([]string{h.Executable}, args...), Dir: dir, EnvNames: h.Env, AllowArgs: h.Transform,
					AllowStop: h.Point == HookTurnAfter, Timeout: time.Duration(h.TimeoutMS) * time.Millisecond}, stdin)
			}
			run := func(id string, stdin []byte) tool.HookResult { return runArgv(id, stdin, hooks[id].Argv[1:]...) }
			// The stdin documents are the engine's own (hookGate.document, pointDocument) for a root run.
			before := func(name, args string) []byte {
				doc, err := exampleGate().document(HookToolBefore, name, json.RawMessage(args), nil)
				mustHook(err)
				return doc
			}
			after := func(name, args string, ok bool) []byte {
				doc, err := exampleGate().document(HookToolAfter, name, json.RawMessage(args),
					outcomeDocument(tool.CallOutcome{OK: ok, Code: "fs_path_outside_workdir", Message: "outside"}))
				mustHook(err)
				return doc
			}
			point := func(p HookPoint, extra map[string]any) []byte {
				doc, err := exampleGate().pointDocument(p, extra)
				mustHook(err)
				return doc
			}
			turn := func(index int) []byte {
				return point(HookTurnAfter, map[string]any{"turn": map[string]any{"index": index, "tool_calls": 1}})
			}
			prompt := func(text string) []byte {
				return point(HookUserPrompt, map[string]any{"prompt": text})
			}

			DescribeTable("exit with their documented code on the engine's stdin",
				func(id string, stdin []byte, want int) {
					Expect(run(id, stdin).ExitCode).To(Equal(want))
				},
				Entry("guard-protected-paths: 2 for a write under migrations/", "guard-protected-paths",
					before("fs.write", exampleMigration), 2),
				Entry("guard-protected-paths: 2 for an edit of a key file", "guard-protected-paths",
					before("fs.edit", `{"path":"deploy/server.key","old_text":"a","new_text":"b"}`), 2),
				Entry("guard-protected-paths: 2 for a write of an rsa key under a directory", "guard-protected-paths",
					before("fs.write", `{"path":"ssh/id_rsa","content":"x"}`), 2),
				Entry("guard-protected-paths: 2 for a write of an ed25519 key under a directory", "guard-protected-paths",
					before("fs.write", `{"path":".ssh/id_ed25519","content":"x"}`), 2),
				Entry("guard-protected-paths: 2 for a write under ./migrations/", "guard-protected-paths",
					before("fs.write", `{"path":"./migrations/x.sql","content":"x"}`), 2),
				Entry("guard-protected-paths: 2 for a write under a nested migrations/", "guard-protected-paths",
					before("fs.write", `{"path":"a/migrations/x.sql","content":"x"}`), 2),
				Entry("guard-protected-paths: 2 for a write of .env", "guard-protected-paths",
					before("fs.write", `{"path":".env","content":"x"}`), 2),
				Entry("guard-protected-paths: 2 for a write of a .env variant under a directory", "guard-protected-paths",
					before("fs.write", `{"path":"app/.env.production","content":"x"}`), 2),
				Entry("guard-protected-paths: 2 for a write of a pem file", "guard-protected-paths",
					before("fs.write", `{"path":"cert.pem","content":"x"}`), 2),
				Entry("guard-protected-paths: 2 for a write of a pkcs12 file", "guard-protected-paths",
					before("fs.write", `{"path":"keys/store.p12","content":"x"}`), 2),
				Entry("guard-protected-paths: 2 for a write of a pfx file", "guard-protected-paths",
					before("fs.write", `{"path":"keys/store.pfx","content":"x"}`), 2),
				Entry("guard-protected-paths: 2 for a write of a bare id_rsa", "guard-protected-paths",
					before("fs.write", `{"path":"id_rsa","content":"x"}`), 2),
				Entry("guard-protected-paths: 2 for a write of a bare id_ed25519", "guard-protected-paths",
					before("fs.write", `{"path":"id_ed25519","content":"x"}`), 2),
				Entry("guard-protected-paths: 2 for a write of a bare .env variant", "guard-protected-paths",
					before("fs.write", `{"path":".env.local","content":"x"}`), 2),
				Entry("guard-protected-paths: 2 for a write of a nested .env", "guard-protected-paths",
					before("fs.write", `{"path":"app/.env","content":"x"}`), 2),
				Entry("guard-protected-paths: 2 for an upper-case path key, which the tools still decode", "guard-protected-paths",
					before("fs.write", `{"PATH":"migrations/001.sql","content":"x"}`), 2),
				Entry("guard-protected-paths: 2 for a capitalised path key", "guard-protected-paths",
					before("fs.write", `{"Path":"secret.pem","content":"x"}`), 2),
				Entry("guard-protected-paths: 0 for a path key in another case holding an ordinary file", "guard-protected-paths",
					before("fs.write", `{"PATH":"ok.txt","content":"x"}`), 0),
				Entry("guard-protected-paths: 0 for an ordinary write", "guard-protected-paths",
					before("fs.write", exampleWrite), 0),
				Entry("refuse-secret-prompt: 2 for a prompt holding an access key id", "refuse-secret-prompt",
					prompt("Deploy with AKIAIOSFODNN7EXAMPLE today"), 2),
				Entry("refuse-secret-prompt: 2 for a prompt holding a private key header", "refuse-secret-prompt",
					prompt("Use -----BEGIN RSA PRIVATE KEY----- to sign"), 2),
				Entry("refuse-secret-prompt: 2 for a prompt holding a github token", "refuse-secret-prompt",
					prompt("Push with ghp_"+strings.Repeat("a", 36)), 2),
				Entry("refuse-secret-prompt: 0 for an ordinary prompt", "refuse-secret-prompt",
					prompt("Summarise README.md"), 0),
				Entry("audit-tool-calls: 0 after a successful call", "audit-tool-calls", after("fs.write", exampleWrite, true), 0),
				Entry("audit-tool-calls: 0 after a failed call", "audit-tool-calls", after("fs.write", exampleWrite, false), 0),
				Entry("stop-after-n-turns: 0 before the limit", "stop-after-n-turns", turn(2), 0),
				Entry("stop-after-n-turns: 0 at the limit", "stop-after-n-turns", turn(5), 0),
				Entry("log-session-start: 0", "log-session-start", point(HookSessionStart, nil), 0),
				Entry("log-session-end: 0", "log-session-end", point(HookSessionEnd, nil), 0),
			)

			It("puts the deny reason on stderr for the operator", func() {
				reason := run("guard-protected-paths", before("fs.write", exampleMigration)).Reason
				Expect(reason).To(ContainSubstring("migrations/"))
			})

			It("cannot be made to forge a line in the audit log through a path", func() {
				run("audit-tool-calls", after("fs.write", `{"path":"a\nok=true forged=1"}`, true))
				Expect(strings.Count(exampleLog(home, "flowstate-tool-audit.log"), "\n")).To(Equal(1))
			})

			It("prints nothing before the turn limit", func() {
				Expect(run("stop-after-n-turns", turn(4)).Stdout).To(BeEmpty())
			})

			It("asks the run to stop at the turn limit", func() {
				Expect(run("stop-after-n-turns", turn(5)).Output.Stop).To(BeTrue())
			})

			It("appends the call to the audit log in HOME", func() {
				run("audit-tool-calls", after("fs.write", exampleWrite, true))
				Expect(exampleLog(home, "flowstate-tool-audit.log")).To(Equal(
					"tool=fs.write call=call-1 ok=true session=sess-1 run=7 path=internal/app/x.go\n"))
			})

			It("logs a failed call as ok=false", func() {
				run("audit-tool-calls", after("fs.write", exampleWrite, false))
				Expect(exampleLog(home, "flowstate-tool-audit.log")).To(ContainSubstring("ok=false"))
			})

			It("logs the session boundaries in order", func() {
				run("log-session-start", point(HookSessionStart, nil))
				run("log-session-end", point(HookSessionEnd, nil))
				Expect(exampleLog(home, "flowstate-session.log")).To(Equal(
					"session.start session=sess-1 run=7\nsession.end session=sess-1 run=7\n"))
			})

			It("exits 1 when its turn limit is not a whole number", func() {
				Expect(runArgv("stop-after-n-turns", turn(5), "five").ExitCode).To(Equal(1))
			})

			// A PATH holding only cat takes a script past input=$(cat) to the command it lacks, so a status the script
			// ignored would come back as an allow (0) where one it passes through comes back as 127.
			DescribeTable("fail closed, with a status other than 0 and 2, when the command they need is not on PATH",
				func(id string, stdin []byte) {
					cat, err := exec.LookPath("cat")
					mustHook(err)
					only := GinkgoT().TempDir()
					mustHook(os.Symlink(cat, filepath.Join(only, "cat")))
					GinkgoT().Setenv("PATH", only)
					Expect(run(id, stdin).ExitCode).NotTo(BeElementOf(0, 2))
				},
				Entry("guard-protected-paths, which needs jq", "guard-protected-paths", before("fs.write", exampleWrite)),
				Entry("refuse-secret-prompt, which needs grep", "refuse-secret-prompt", prompt("Summarise README.md")),
				Entry("stop-after-n-turns, which needs jq", "stop-after-n-turns", turn(5)),
				Entry("audit-tool-calls, which needs jq", "audit-tool-calls", after("fs.write", exampleWrite, true)),
				Entry("log-session-start, which needs jq", "log-session-start", point(HookSessionStart, nil)),
			)

			DescribeTable("the gate invokes the guard only on the tools its match lists",
				func(name string, want []string) {
					cfg := execLoopFixture(nil)
					loaded, err := loadExampleHooks()
					mustHook(err)
					cfg.Hooks, cfg.Workdir = loaded, GinkgoT().TempDir()
					w, err := cfg.Store.NewEventWriter(context.Background(), cfg.Session.ID)
					mustHook(err)
					DeferCleanup(func() { _ = w.Close() })
					gate := newHookGate(cfg, provider.ToolCall{ID: "call-1"}, w)
					_, err = gate.Before(context.Background(), name, json.RawMessage(exampleWrite))
					mustHook(err)
					Expect(exampleInvokedIDs(cfg.Store, cfg.Session.ID)).To(Equal(want))
				},
				Entry("nothing on fs.read, which the match leaves out", "fs.read", []string{}),
				Entry("guard-protected-paths on fs.write", "fs.write", []string{"guard-protected-paths"}),
				Entry("guard-protected-paths on fs.edit", "fs.edit", []string{"guard-protected-paths"}),
				Entry("nothing on echo, which the match leaves out", "echo", []string{}),
			)
		})
	}
})
