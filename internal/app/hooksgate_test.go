//go:build !windows

package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/store"
	"github.com/baphled/flowstate/internal/swarm"
	"github.com/baphled/flowstate/internal/tool"
)

// gateScript writes an executable shell hook into dir and returns its path.
func gateScript(dir, name, body string) string {
	return putHookMode(dir, name, "#!/bin/sh\n"+body+"\n", 0o700)
}

// gateHook is a loaded tool.before command hook running script, failing closed.
func gateHook(id, script string, match ...string) Hook {
	return Hook{ID: id, Point: HookToolBefore, Kind: HookKindCommand, Argv: []string{script}, Executable: script,
		Match: match, TimeoutMS: 5000, OnFailure: HookOnFailureDeny, Origin: "/test/hooks.json", Source: HookSourceUser}
}

// gateSet is the loaded set of hooks, in order.
func gateSet(hooks ...Hook) LoadedHooks { return LoadedHooks{Scope: HookScopeUser, Hooks: hooks} }

// gateEvents is every event of the session's log.
func gateEvents(st *store.Store, id string) []store.RecordedEvent {
	return must(st.Replay(context.Background(), id))
}

// gateOfType is the events of one type.
func gateOfType(events []store.RecordedEvent, types ...string) []store.RecordedEvent {
	var out []store.RecordedEvent
	for _, e := range events {
		for _, t := range types {
			if e.Type == t {
				out = append(out, e)
			}
		}
	}
	return out
}

// gateTypes is the event types, in order.
func gateTypes(events []store.RecordedEvent) []string {
	out := []string{}
	for _, e := range events {
		out = append(out, e.Type)
	}
	return out
}

// gateLines is the lines of a file the hooks appended to; none when the file does not exist.
func gateLines(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return strings.Fields(string(data))
}

// gateDenied reports whether a tool message is the fixed hook denial text.
func gateDenied(content string) bool { return strings.HasPrefix(content, "error: denied by hook") }

// gateDeniedResults is the hook_denied tool.result events of the session.
func gateDeniedResults(st *store.Store, id string) []store.RecordedEvent {
	var out []store.RecordedEvent
	for _, e := range gateOfType(gateEvents(st, id), tool.TypeToolResult) {
		if strings.Contains(e.PayloadJSON, `"hook_denied"`) {
			out = append(out, e)
		}
	}
	return out
}

var _ = Describe("The tool.before gate in the root loop (ADR-012 packet 3a)", func() {
	var (
		dir      string
		cfg      ExecLoopConfig
		streamer *execLoopStreamer
		ctx      context.Context
	)

	run := func(set LoadedHooks, call provider.ToolCall) {
		streamer = &execLoopStreamer{turns: [][]provider.StreamEvent{{provCall(call), provFinish()}, {provText("done")}}}
		cfg = execLoopFixture(streamer)
		cfg.Hooks, cfg.Workdir = set, dir
		if out := runExec(ctx, cfg); !out.NoErr {
			panic("the gated run failed")
		}
	}
	toolMessage := func() string { return streamer.lastRequest().Messages[2].Content }

	BeforeEach(func() {
		ctx = context.Background()
		dir = GinkgoT().TempDir()
	})

	Describe("a denied call", func() {
		var counter string
		BeforeEach(func() {
			counter = filepath.Join(dir, "count")
			guard := gateScript(dir, "guard", `echo ran >> `+counter+"\necho 'secret reason' >&2\nexit 2")
			run(gateSet(gateHook("guard", guard)), echoCall("call-1"))
		})
		It("is recorded as hook.invoked, hook.result, tool.call and tool.result in that order", func() {
			recorded := gateOfType(gateEvents(cfg.Store, cfg.Session.ID), TypeHookInvoked, TypeHookResult, tool.TypeToolCall, tool.TypeToolResult)
			Expect(gateTypes(recorded)).To(Equal([]string{TypeHookInvoked, TypeHookResult, tool.TypeToolCall, tool.TypeToolResult}))
		})
		It("answers the model's call id with a hook_denied error result", func() {
			result := gateDeniedResults(cfg.Store, cfg.Session.ID)[0]
			Expect(payloadFieldText(result.PayloadJSON, "tool_call_id")).To(Equal("call-1"))
		})
		It("records the code hook_denied and the fixed message", func() {
			result := gateDeniedResults(cfg.Store, cfg.Session.ID)[0]
			Expect(payloadFieldRaw(result.PayloadJSON, "error")).To(MatchJSON(`{"code":"hook_denied","message":"denied by hook guard"}`))
		})
		It("sends the model one fixed text naming the hook", func() {
			Expect(toolMessage()).To(Equal("error: denied by hook guard"))
		})
		It("keeps the hook's reason out of everything the model is sent", func() {
			sent, err := json.Marshal(streamer.lastRequest().Messages)
			if err != nil {
				panic(err)
			}
			Expect(string(sent)).NotTo(ContainSubstring("secret reason"))
		})
		It("keeps the hook's reason in hook.result for the operator", func() {
			result := gateOfType(gateEvents(cfg.Store, cfg.Session.ID), TypeHookResult)[0]
			Expect(payloadFieldText(result.PayloadJSON, "reason")).To(Equal("secret reason"))
		})
		It("is rebuilt from the log as a call with an answer, not dropped", func() {
			var rebuilt []provider.Message
			if err := replayInto(ctx, cfg.Store, cfg.Session.ID, &rebuilt); err != nil {
				panic(err)
			}
			Expect(rebuilt[2].Role).To(Equal("tool"))
		})
		It("is rebuilt to the same tool message the live loop sent", func() {
			var rebuilt []provider.Message
			if err := replayInto(ctx, cfg.Store, cfg.Session.ID, &rebuilt); err != nil {
				panic(err)
			}
			Expect(rebuilt[2].Content).To(Equal(toolMessage()))
		})
		It("runs the hook once for the call", func() {
			Expect(gateLines(counter)).To(HaveLen(1))
		})
		It("does not run the hook again when the log is replayed", func() {
			var rebuilt []provider.Message
			if err := replayInto(ctx, cfg.Store, cfg.Session.ID, &rebuilt); err != nil {
				panic(err)
			}
			Expect(gateLines(counter)).To(HaveLen(1))
		})
	})

	Describe("an allowed call", func() {
		BeforeEach(func() {
			allow := gateScript(dir, "allow", "exit 0")
			run(gateSet(gateHook("allow", allow)), echoCall("call-1"))
		})
		It("runs the tool", func() {
			Expect(gateDenied(toolMessage())).To(BeFalse())
		})
		It("records hook.result with the decision allow", func() {
			result := gateOfType(gateEvents(cfg.Store, cfg.Session.ID), TypeHookResult)[0]
			Expect(payloadFieldText(result.PayloadJSON, "decision")).To(Equal("allow"))
		})
		It("records hook.invoked with the hook's id and the call id", func() {
			invoked := gateOfType(gateEvents(cfg.Store, cfg.Session.ID), TypeHookInvoked)[0]
			Expect([]string{payloadFieldText(invoked.PayloadJSON, "id"), payloadFieldText(invoked.PayloadJSON, "tool_call_id")}).
				To(Equal([]string{"allow", "call-1"}))
		})
	})

	DescribeTable("a hook that does not run to a valid verdict follows on_failure",
		func(body, onFailure string, denied bool) {
			script := gateScript(dir, "h", body)
			hook := gateHook("h", script)
			hook.OnFailure, hook.TimeoutMS = onFailure, 300
			run(gateSet(hook), echoCall("call-1"))
			Expect(gateDenied(toolMessage())).To(Equal(denied))
		},
		Entry("a crash under deny", "exit 1", HookOnFailureDeny, true),
		Entry("a crash under allow", "exit 1", HookOnFailureAllow, false),
		Entry("output that is not JSON under deny", "echo nope", HookOnFailureDeny, true),
		Entry("a timeout under deny", "sleep 30", HookOnFailureDeny, true),
		Entry("a timeout under allow", "sleep 30", HookOnFailureAllow, false),
		Entry("context under deny (context_unsupported)", `echo '{"context":"more"}'`, HookOnFailureDeny, true),
		Entry("context under allow", `echo '{"context":"more"}'`, HookOnFailureAllow, false),
	)

	It("records a returned context as the failure context_unsupported", func() {
		run(gateSet(gateHook("h", gateScript(dir, "h", `echo '{"context":"more"}'`))), echoCall("call-1"))
		result := gateOfType(gateEvents(cfg.Store, cfg.Session.ID), TypeHookResult)[0]
		Expect(payloadFieldText(result.PayloadJSON, "failure")).To(Equal("context_unsupported"))
	})
	It("records a failure with the decision failed", func() {
		run(gateSet(gateHook("h", gateScript(dir, "h", "exit 1"))), echoCall("call-1"))
		result := gateOfType(gateEvents(cfg.Store, cfg.Session.ID), TypeHookResult)[0]
		Expect(payloadFieldText(result.PayloadJSON, "decision")).To(Equal("failed"))
	})

	Describe("a call whose stdin document is over the 256 KiB cap", func() {
		var counter string
		BeforeEach(func() {
			counter = filepath.Join(dir, "count")
			run(gateSet(gateHook("h", gateScript(dir, "h", "echo ran >> "+counter))), provider.ToolCall{ID: "call-1", Name: "echo",
				Arguments: json.RawMessage(`{"message":"` + strings.Repeat("a", 300<<10) + `"}`)})
		})
		It("is denied", func() { Expect(gateDenied(toolMessage())).To(BeTrue()) })
		It("never starts the hook", func() { Expect(gateLines(counter)).To(BeEmpty()) })
		It("records input_too_large in hook.result", func() {
			result := gateOfType(gateEvents(cfg.Store, cfg.Session.ID), TypeHookResult)[0]
			Expect(payloadFieldText(result.PayloadJSON, "failure")).To(Equal("input_too_large"))
		})
	})

	Describe("hooks of one point", func() {
		var order string
		BeforeEach(func() {
			order = filepath.Join(dir, "order")
			hooks := []Hook{}
			for _, h := range []struct{ id, exit string }{{"first", "0"}, {"second", "2"}, {"third", "0"}} {
				script := gateScript(dir, h.id, "echo "+h.id+" >> "+order+"\necho no >&2\nexit "+h.exit)
				hooks = append(hooks, gateHook(h.id, script))
			}
			run(gateSet(hooks...), echoCall("call-1"))
		})
		It("run sequentially in file order and stop at the first deny", func() {
			Expect(gateLines(order)).To(Equal([]string{"first", "second"}))
		})
		It("name the denying hook to the model", func() {
			Expect(toolMessage()).To(Equal("error: denied by hook second"))
		})
	})

	Describe("a hook that matches another tool", func() {
		var counter string
		BeforeEach(func() {
			counter = filepath.Join(dir, "count")
			run(gateSet(gateHook("h", gateScript(dir, "h", "echo ran >> "+counter+"\nexit 2\n"), "fs.*")), echoCall("call-1"))
		})
		It("does not run", func() { Expect(gateLines(counter)).To(BeEmpty()) })
		It("leaves the call alone", func() { Expect(gateDenied(toolMessage())).To(BeFalse()) })
	})

	Describe("the stdin document", func() {
		var doc map[string]any
		BeforeEach(func() {
			stdin := filepath.Join(dir, "stdin.json")
			run(gateSet(gateHook("h", gateScript(dir, "h", "cat > "+stdin))), echoCall("call-1"))
			data, err := os.ReadFile(stdin)
			if err != nil {
				panic(err)
			}
			if err := json.Unmarshal(data, &doc); err != nil {
				panic(err)
			}
		})
		It("names the point", func() { Expect(doc["point"]).To(Equal("tool.before")) })
		It("names the version", func() { Expect(doc["version"]).To(Equal(float64(1))) })
		It("names the session", func() { Expect(doc["session_id"]).To(Equal(cfg.Session.ID)) })
		It("names the working directory", func() { Expect(doc["cwd"]).To(Equal(dir)) })
		It("gives the root run depth 0", func() { Expect(doc["depth"]).To(Equal(float64(0))) })
		It("carries the tool name, the call id and the arguments", func() {
			Expect(doc["tool"]).To(Equal(map[string]any{"name": "echo", "call_id": "call-1",
				"args": map[string]any{"message": "hi"}}))
		})
	})

	Describe("a tool the allowlist refuses", func() {
		var counter string
		BeforeEach(func() {
			counter = filepath.Join(dir, "count")
			run(gateSet(gateHook("h", gateScript(dir, "h", "echo ran >> "+counter+"\nexit 2\n"))), writeCall())
		})
		It("never reaches a hook", func() { Expect(gateLines(counter)).To(BeEmpty()) })
		It("is refused by the allowlist, not by a hook", func() {
			Expect(toolMessage()).To(ContainSubstring("allowlist"))
		})
	})

	Describe("an allowed name no tool is registered for", func() {
		var counter string
		BeforeEach(func() {
			counter = filepath.Join(dir, "count")
			streamer = &execLoopStreamer{}
			cfg = execLoopFixture(streamer)
			cfg.Allowlist = []string{"echo", "ghost"}
			cfg.Hooks = gateSet(gateHook("h", gateScript(dir, "h", "echo ran >> "+counter+"\nexit 2\n")))
			cfg.Workdir = dir
			_, err := executeCalls(ctx, cfg, []provider.ToolCall{{ID: "g1", Name: "ghost", Arguments: json.RawMessage(`{}`)}})
			if err != nil {
				panic(err)
			}
		})
		It("never reaches a hook", func() { Expect(gateLines(counter)).To(BeEmpty()) })
		It("records no hook event", func() {
			Expect(gateOfType(gateEvents(cfg.Store, cfg.Session.ID), TypeHookInvoked, TypeHookResult)).To(BeEmpty())
		})
	})
	It("fires nothing for a set that is not a loaded hook set", func() {
		streamer = &execLoopStreamer{turns: [][]provider.StreamEvent{{provCall(echoCall("call-1")), provFinish()}, {provText("done")}}}
		cfg = execLoopFixture(streamer)
		cfg.Hooks = stubHookSet("opaque")
		runExec(ctx, cfg)
		Expect(gateOfType(gateEvents(cfg.Store, cfg.Session.ID), TypeHookInvoked)).To(BeEmpty())
	})
})

var _ = Describe("The tool.before gate below the root (ADR-012 D22)", func() {
	var (
		dir     string
		hooks   LoadedHooks
		counter string
	)
	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		counter = filepath.Join(dir, "count")
		guard := gateScript(dir, "guard", "cat > "+filepath.Join(dir, "stdin.json")+"\necho ran >> "+counter+"\necho no >&2\nexit 2")
		hooks = gateSet(gateHook("guard", guard, "fs.write"))
	})

	Describe("a delegated child", func() {
		var f *delegateFixture
		var child string
		BeforeEach(func() {
			f = newDelegateFixture(callTurn(writeCall()), finishText("child done"))
			f.profile.Tools = []string{"fs.write"}
			req := f.request()
			req.Hooks = hooks
			res, err := RunDelegatedChild(context.Background(), req)
			check(err)
			child = res.ChildID
		})
		It("is denied the write", func() { Expect(gateDeniedResults(f.st, child)).To(HaveLen(1)) })
		It("writes no file", func() { Expect(filepath.Join(f.workdir, "new.txt")).NotTo(BeAnExistingFile()) })
		It("records hook.invoked and hook.result in its own log", func() {
			Expect(gateTypes(gateOfType(gateEvents(f.st, child), TypeHookInvoked, TypeHookResult))).
				To(Equal([]string{TypeHookInvoked, TypeHookResult}))
		})
		It("writes no hooks.loaded", func() {
			Expect(gateOfType(gateEvents(f.st, child), TypeHooksLoaded)).To(BeEmpty())
		})

		Describe("its hook's stdin document", func() {
			var doc map[string]any
			BeforeEach(func() {
				data, err := os.ReadFile(filepath.Join(dir, "stdin.json"))
				check(err)
				check(json.Unmarshal(data, &doc))
			})
			It("names the child's agent", func() { Expect(doc["agent"]).To(Equal("reviewer")) })
			It("gives the child depth 1", func() { Expect(doc["depth"]).To(Equal(float64(1))) })
		})
		It("leaves the parent's log without hook events", func() {
			Expect(gateOfType(gateEvents(f.st, f.parent.ID), TypeHookInvoked, TypeHookResult)).To(BeEmpty())
		})
	})

	Describe("a nested child", func() {
		var f *nestFixture
		var grandchild string
		BeforeEach(func() {
			f = newNestFixture(1)
			f.addProfile(nestProfile("writer", 2, "fs.write"))
			f.tool.cfg.Profiles[0].Delegates = []string{"writer"}
			f.tool.cfg.Hooks = hooks
			f.say("root", delegates(`{"agent":"lead","task":"plan it"}`), finishText("root done"))
			f.say("lead", delegates(`{"agent":"writer","task":"write it"}`), finishText("lead done"))
			f.say("writer", callTurn(writeCall()), finishText("writer done"))
			_, err := f.runRoot(context.Background())
			check(err)
			grandchild = f.childOf(f.childOf(f.parent.ID))
		})
		It("is denied the write", func() { Expect(gateDeniedResults(f.st, grandchild)).To(HaveLen(1)) })
		It("writes no file", func() { Expect(filepath.Join(f.workdir, "new.txt")).NotTo(BeAnExistingFile()) })
	})

	Describe("a swarm run", func() {
		var f *swarmExecFixture
		var res ExecuteSwarmResult
		BeforeEach(func() {
			f = newSwarmExecFixture()
			f.cfg.Hooks = hooks
			f.cfg.Profiles[0].Tools = []string{"delegate", "fs.write"}
			f.cfg.Profiles[1].Tools = []string{"fs.write"}
			f.both("swarm done")
			f.scripts.byRole["coord-body"] = append([][]provider.StreamEvent{callTurn(writeCallID("w0"))}, f.scripts.byRole["coord-body"]...)
			f.scripts.byRole["writer-body"] = [][]provider.StreamEvent{callTurn(writeCall()), finishText("writer done")}
			res = f.execute(context.Background())
		})
		It("denies the coordinator's write", func() {
			Expect(gateDeniedResults(f.st, res.CoordinatorID)).To(HaveLen(1))
		})
		It("denies a packet child's write", func() {
			var writer string
			for _, c := range res.Children {
				if c.Profile == "writer" {
					writer = c.ID
				}
			}
			Expect(gateDeniedResults(f.st, writer)).To(HaveLen(1))
		})
		It("writes no file", func() { Expect(filepath.Join(f.cfg.Workdir, "new.txt")).NotTo(BeAnExistingFile()) })
		It("writes no hooks.loaded for the run", func() {
			Expect(gateOfType(gateEvents(f.st, res.CoordinatorID), TypeHooksLoaded)).To(BeEmpty())
		})
		It("ran the hook for the coordinator and the packet child", func() {
			Expect(gateLines(counter)).To(HaveLen(2))
		})
	})
})

var _ = Describe("Hook loading in RunSessionExecLoop and ExecuteSwarmFile (ADR-012 packet 3a)", func() {
	var (
		root, other string
		st          *store.Store
		sess        *store.Session
		streamer    *execLoopStreamer
		script      string
	)

	// projectFile writes .flowstate/hooks.json and its script under base.
	projectFile := func(base string) {
		hookDir := filepath.Join(base, ".flowstate")
		script = gateScript(hookDir, "guard.sh", "echo no >&2\nexit 2")
		putHookFile(hookDir, "hooks.json", hookFileWith(
			`{"id":"guard","point":"tool.before","kind":"command","argv":["./guard.sh"],"match":["echo"]}`))
	}
	runSession := func(scope HookScope) error {
		_, err := RunSessionExecLoop(context.Background(), st, filepath.Join(other, "s.db"), sess, streamer, SessionExecLoopRequest{
			MaxTurns: 4, Lease: heldSessionLease(st, sess), Allow: []string{"echo"}, Workdir: other, ProjectRoot: root,
			HooksScope: scope,
		})
		return err
	}

	BeforeEach(func() {
		root, other = GinkgoT().TempDir(), GinkgoT().TempDir()
		st, sess = execLoopSeed()
		DeferCleanup(func() { _ = st.Close() })
		streamer = &execLoopStreamer{turns: [][]provider.StreamEvent{{provCall(echoCall("call-1")), provFinish()}, {provText("done")}}}
	})

	Describe("a project hook file under the project root", func() {
		var loaded []store.RecordedEvent
		BeforeEach(func() {
			projectFile(root)
			check(runSession(HookScopeProject))
			loaded = gateOfType(gateEvents(st, sess.ID), TypeHooksLoaded)
		})
		It("denies the call the hook matches", func() {
			Expect(gateDeniedResults(st, sess.ID)).To(HaveLen(1))
		})
		It("is recorded once in hooks.loaded", func() { Expect(loaded).To(HaveLen(1)) })
		It("is recorded before any hook runs", func() {
			Expect(gateTypes(gateOfType(gateEvents(st, sess.ID), TypeHooksLoaded, TypeHookInvoked))).
				To(Equal([]string{TypeHooksLoaded, TypeHookInvoked}))
		})
		It("records the scope", func() { Expect(payloadFieldText(loaded[0].PayloadJSON, "scope")).To(Equal("project")) })
		It("records the digest of the loaded set", func() {
			set, err := LoadHooks(HooksRequest{Scope: HookScopeProject, ProjectRoot: root, Accepted: AcceptedHooks()})
			check(err)
			Expect(payloadFieldText(loaded[0].PayloadJSON, "set_sha256")).To(Equal(set.Digest()))
		})
		It("records each hook's id, source, file, resolved path and sha256", func() {
			resolved := must(filepath.EvalSymlinks(script))
			content, err := os.ReadFile(script)
			check(err)
			sum := sha256.Sum256(content)
			Expect(payloadFieldRaw(loaded[0].PayloadJSON, "hooks")).To(MatchJSON(fmt.Sprintf(
				`[{"id":"guard","source":"project","file":%q,"path":%q,"sha256":%q}]`,
				filepath.Join(root, ".flowstate", "hooks.json"), resolved, hex.EncodeToString(sum[:]))))
		})
		It("records empty skipped and inert lists", func() {
			Expect(payloadFieldRaw(loaded[0].PayloadJSON, "skipped") + payloadFieldRaw(loaded[0].PayloadJSON, "inert")).
				To(Equal("[][]"))
		})
	})
	It("finds the project file under the project root, never under --workdir", func() {
		projectFile(other)
		check(runSession(HookScopeProject))
		Expect(gateDeniedResults(st, sess.ID)).To(BeEmpty())
	})
	It("records an empty set for a scope that finds no file", func() {
		check(runSession(HookScopeProject))
		loaded := gateOfType(gateEvents(st, sess.ID), TypeHooksLoaded)
		Expect(payloadFieldRaw(loaded[0].PayloadJSON, "ids")).To(Equal("[]"))
	})
	It("loads and records nothing for an unset scope", func() {
		projectFile(root)
		check(runSession(""))
		Expect(gateOfType(gateEvents(st, sess.ID), TypeHooksLoaded, TypeHookInvoked)).To(BeEmpty())
	})
	It("loads no project file under the user scope", func() {
		projectFile(root)
		GinkgoT().Setenv("XDG_CONFIG_HOME", GinkgoT().TempDir())
		check(runSession(HookScopeUser))
		Expect(gateDeniedResults(st, sess.ID)).To(BeEmpty())
	})

	Describe("a hook file naming a point this build cannot fire", func() {
		var err error
		BeforeEach(func() {
			putHookFile(filepath.Join(root, ".flowstate"), "hooks.json", hookFileWith(cmdHook("late", "compaction.before", "")))
			gateScript(filepath.Join(root, ".flowstate"), "x.sh", "exit 0")
			err = runSession(HookScopeProject)
		})
		It("fails the run as a config error", func() { Expect(err).To(MatchError(ErrConfigInvalid)) })
		It("names hook_point_unsupported", func() { Expect(err).To(MatchError(ErrHookPointUnsupported)) })
		It("sends no provider request", func() { Expect(streamer.reqs).To(BeEmpty()) })
	})

	Describe("ExecuteSwarmFile", func() {
		var f *swarmExecFixture
		var manifest string
		BeforeEach(func() {
			f = newSwarmExecFixture()
			f.cfg.Profiles[1].Tools = []string{"fs.write"}
			f.both("swarm done")
			f.scripts.byRole["writer-body"] = [][]provider.StreamEvent{callTurn(writeCall()), finishText("writer done")}
			manifest = filepath.Join(GinkgoT().TempDir(), "swarm.yml")
			text := fmt.Sprintf("name: demo\nscope: demo\ncoordinator: coord\npackets:\n"+
				"  - {id: p1, scope: demo.a, task: write it, role: writer, source_sha256: %s}\n"+
				"  - {id: p2, scope: demo.b, task: review it, role: reviewer, prerequisites: [p1], source_sha256: %s}\n"+
				"gates:\n  - {name: go, description: builds, command: \"true\"}\n",
				swarm.SHA256HexForTest([]byte("write it")), swarm.SHA256HexForTest([]byte("review it")))
			check(os.WriteFile(manifest, []byte(text), 0o600))
			f.cfg.HooksScope, f.cfg.ProjectRoot = HookScopeProject, root
		})
		It("loads the scope's hooks and applies them to a packet child", func() {
			hookDir := filepath.Join(root, ".flowstate")
			gateScript(hookDir, "guard.sh", "echo no >&2\nexit 2")
			putHookFile(hookDir, "hooks.json", hookFileWith(
				`{"id":"guard","point":"tool.before","kind":"command","argv":["./guard.sh"],"match":["fs.write"]}`))
			res, err := ExecuteSwarmFile(context.Background(), f.st, manifest, f.cfg)
			check(err)
			Expect(gateDeniedResults(f.st, res.Children[0].ID)).To(HaveLen(1))
		})
		It("refuses a hook file that does not load before it creates a session", func() {
			putHookFile(filepath.Join(root, ".flowstate"), "hooks.json", `{"version":1,"hooks":[],"extra":1}`)
			_, err := ExecuteSwarmFile(context.Background(), f.st, manifest, f.cfg)
			Expect(err).To(MatchError(ErrConfigInvalid))
		})
		It("creates no session when the hook file does not load", func() {
			putHookFile(filepath.Join(root, ".flowstate"), "hooks.json", `{"version":1,"hooks":[],"extra":1}`)
			_, _ = ExecuteSwarmFile(context.Background(), f.st, manifest, f.cfg)
			Expect(f.sessionCount()).To(BeZero())
		})
	})
})
