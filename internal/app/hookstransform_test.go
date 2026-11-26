//go:build !windows

package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tool"
)

// transformHook is a loaded tool.before transform hook running script, failing closed.
func transformHook(id, script string, match ...string) Hook {
	h := gateHook(id, script, match...)
	h.Transform = true
	return h
}

// afterHook is a loaded tool.after command hook running script; tool.after fails open.
func afterHook(id, script string, match ...string) Hook {
	h := gateHook(id, script, match...)
	h.Point, h.OnFailure = HookToolAfter, HookOnFailureAllow
	return h
}

// rewriteTo is the stdout of a transform hook that rewrites the echo message.
func rewriteTo(message string) string {
	return "echo '{\"args\":{\"message\":\"" + message + "\"}}'"
}

// gateHash is the hex sha256 of text.
func gateHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// gateReadDoc is the stdin document a hook saved into path.
func gateReadDoc(path string) map[string]any {
	data, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		panic(err)
	}
	return doc
}

// gateToolDoc is the "tool" object of a saved stdin document.
func gateToolDoc(path string) map[string]any {
	doc, _ := gateReadDoc(path)["tool"].(map[string]any)
	return doc
}

var _ = Describe("The tool.before transform phase and tool.after (ADR-012 packet 3b)", func() {
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
			panic("the hooked run failed")
		}
	}
	toolMessage := func() string { return streamer.lastRequest().Messages[2].Content }
	results := func() []string {
		var out []string
		for _, e := range gateOfType(gateEvents(cfg.Store, cfg.Session.ID), TypeHookResult) {
			out = append(out, e.PayloadJSON)
		}
		return out
	}

	BeforeEach(func() {
		ctx = context.Background()
		dir = GinkgoT().TempDir()
	})

	Describe("a transform hook that rewrites the arguments", func() {
		BeforeEach(func() {
			run(gateSet(transformHook("fix", gateScript(dir, "fix", rewriteTo("rewritten")))), echoCall("call-1"))
		})
		It("makes the model see the result of the rewritten call", func() {
			Expect(toolMessage()).To(ContainSubstring("rewritten"))
		})
		It("records the model's own arguments in tool.call", func() {
			call := gateOfType(gateEvents(cfg.Store, cfg.Session.ID), tool.TypeToolCall)[0]
			Expect(call.PayloadJSON).To(ContainSubstring(`"message":"hi"`))
		})
		It("records the rewritten call's result in tool.result", func() {
			result := gateOfType(gateEvents(cfg.Store, cfg.Session.ID), tool.TypeToolResult)[0]
			Expect(result.PayloadJSON).To(ContainSubstring(`"message":"rewritten"`))
		})
		It("records args_rewritten in hook.result", func() {
			Expect(payloadFieldRaw(results()[0], "args_rewritten")).To(Equal("true"))
		})
		It("records the hash of the arguments the hook was shown", func() {
			Expect(payloadFieldText(results()[0], "args_sha256")).To(Equal(gateHash(`{"message":"hi"}`)))
		})
		It("records the hash of the rewritten arguments", func() {
			Expect(payloadFieldText(results()[0], "rewritten_args_sha256")).To(Equal(gateHash(`{"message":"rewritten"}`)))
		})
		It("records the rewritten arguments", func() {
			Expect(payloadFieldRaw(results()[0], "rewritten_args")).To(MatchJSON(`{"message":"rewritten"}`))
		})
		It("replays the model's call with its own arguments, as the live history held them", func() {
			var rebuilt []provider.Message
			if err := replayInto(ctx, cfg.Store, cfg.Session.ID, &rebuilt); err != nil {
				panic(err)
			}
			Expect(string(rebuilt[1].ToolCalls[0].Arguments)).To(MatchJSON(`{"message":"hi"}`))
		})
		It("replays the tool message the live loop sent", func() {
			var rebuilt []provider.Message
			if err := replayInto(ctx, cfg.Store, cfg.Session.ID, &rebuilt); err != nil {
				panic(err)
			}
			Expect(rebuilt[2].Content).To(Equal(toolMessage()))
		})
		It("keeps the model's own call in the live history", func() {
			Expect(string(streamer.lastRequest().Messages[1].ToolCalls[0].Arguments)).To(MatchJSON(`{"message":"hi"}`))
		})
	})

	Describe("a gate listed before a transform hook", func() {
		var order, seen string
		BeforeEach(func() {
			order, seen = filepath.Join(dir, "order"), filepath.Join(dir, "seen.json")
			gate := gateScript(dir, "gate", "cat > "+seen+"\necho gate >> "+order)
			fix := gateScript(dir, "fix", "echo transform >> "+order+"\n"+rewriteTo("rewritten"))
			run(gateSet(gateHook("gate", gate), transformHook("fix", fix)), echoCall("call-1"))
		})
		It("sees the final arguments", func() {
			Expect(gateToolDoc(seen)["args"]).To(Equal(map[string]any{"message": "rewritten"}))
		})
		It("runs after the transform hook", func() {
			Expect(gateLines(order)).To(Equal([]string{"transform", "gate"}))
		})
	})

	Describe("two transform hooks", func() {
		var seen string
		BeforeEach(func() {
			seen = filepath.Join(dir, "seen.json")
			one := gateScript(dir, "one", rewriteTo("one"))
			two := gateScript(dir, "two", "cat > "+seen+"\n"+rewriteTo("two"))
			run(gateSet(transformHook("one", one), transformHook("two", two)), echoCall("call-1"))
		})
		It("show the second the arguments the first left", func() {
			Expect(gateToolDoc(seen)["args"]).To(Equal(map[string]any{"message": "one"}))
		})
		It("run the tool with the last rewrite", func() {
			Expect(toolMessage()).To(ContainSubstring("two"))
		})
	})

	Describe("a hook that is not a transform hook", func() {
		BeforeEach(func() {
			run(gateSet(gateHook("sly", gateScript(dir, "sly", rewriteTo("rewritten")))), echoCall("call-1"))
		})
		It("cannot rewrite: its args are a failure", func() {
			Expect(payloadFieldText(results()[0], "failure")).To(Equal(tool.HookFailureFieldNotPermitted))
		})
		It("denies the call under a fail-closed policy", func() { Expect(gateDenied(toolMessage())).To(BeTrue()) })
	})

	Describe("a transform hook that names a tool", func() {
		BeforeEach(func() {
			run(gateSet(transformHook("rename", gateScript(dir, "rename",
				`echo '{"name":"fs.write","args":{"message":"x"}}'`))), echoCall("call-1"))
		})
		It("fails the hook: a rewrite cannot rename the tool", func() {
			Expect(payloadFieldText(results()[0], "failure")).To(Equal(tool.HookFailureInvalidOutput))
		})
		It("does not run the call", func() { Expect(gateDenied(toolMessage())).To(BeTrue()) })
	})

	Describe("a transform hook that returns args that are not an object", func() {
		BeforeEach(func() {
			run(gateSet(transformHook("bad", gateScript(dir, "bad", `echo '{"args":[1]}'`))), echoCall("call-1"))
		})
		It("fails the hook", func() {
			Expect(payloadFieldText(results()[0], "failure")).To(Equal(tool.HookFailureInvalidOutput))
		})
		It("does not run the call under a fail-closed policy", func() { Expect(gateDenied(toolMessage())).To(BeTrue()) })
	})

	Describe("a transform hook that fails under on_failure allow", func() {
		BeforeEach(func() {
			h := transformHook("flaky", gateScript(dir, "flaky", "exit 1"))
			h.OnFailure = HookOnFailureAllow
			run(gateSet(h), echoCall("call-1"))
		})
		It("lets the call run with the model's own arguments", func() {
			Expect(toolMessage()).To(ContainSubstring(`"message":"hi"`))
		})
	})

	Describe("a transform hook that denies", func() {
		var counter string
		BeforeEach(func() {
			counter = filepath.Join(dir, "count")
			deny := gateScript(dir, "deny", "echo no >&2\nexit 2")
			later := gateScript(dir, "later", "echo ran >> "+counter)
			run(gateSet(gateHook("later", later), transformHook("deny", deny)), echoCall("call-1"))
		})
		It("ends the chain as a deny naming the hook", func() {
			Expect(toolMessage()).To(Equal("error: denied by hook deny"))
		})
		It("never runs the gate", func() { Expect(gateLines(counter)).To(BeEmpty()) })
	})

	Describe("a rewrite of a file tool call", func() {
		var work string
		BeforeEach(func() {
			work = filepath.Join(dir, "work")
			if err := os.Mkdir(work, 0o700); err != nil {
				panic(err)
			}
		})
		runWrite := func(path string) {
			streamer = &execLoopStreamer{turns: [][]provider.StreamEvent{{provCall(writeCall()), provFinish()}, {provText("done")}}}
			cfg = execLoopFixture(streamer)
			registry, err := NewSessionToolRegistry(work)
			if err != nil {
				panic(err)
			}
			rewrite := gateScript(dir, "rewrite", `echo '{"args":{"path":"`+path+`","content":"x"}}'`)
			cfg.Registry, cfg.Allowlist, cfg.Workdir = registry, []string{"fs.write"}, work
			cfg.Hooks = gateSet(transformHook("rewrite", rewrite))
			if out := runExec(ctx, cfg); !out.NoErr {
				panic("the run failed")
			}
		}
		It("writes the rewritten path", func() {
			runWrite("moved.txt")
			Expect(filepath.Join(work, "moved.txt")).To(BeAnExistingFile())
		})
		It("does not write the model's own path", func() {
			runWrite("moved.txt")
			Expect(filepath.Join(work, "new.txt")).NotTo(BeAnExistingFile())
		})
		It("is refused by the tool's confinement when it leaves the workdir", func() {
			runWrite("../escaped.txt")
			Expect(filepath.Join(dir, "escaped.txt")).NotTo(BeAnExistingFile())
		})
		It("tells the model of the confinement refusal, not of a hook denial", func() {
			runWrite("../escaped.txt")
			Expect(toolMessage()).To(ContainSubstring("error:"))
		})
	})

	Describe("a tool.after hook", func() {
		var seen string
		BeforeEach(func() {
			seen = filepath.Join(dir, "seen.json")
			run(gateSet(afterHook("watch", gateScript(dir, "watch", "cat > "+seen))), echoCall("call-1"))
		})
		It("sees the point", func() { Expect(gateReadDoc(seen)["point"]).To(Equal("tool.after")) })
		It("sees the result of the call", func() {
			Expect(gateToolDoc(seen)["result"]).To(Equal(map[string]any{"ok": true,
				"result": map[string]any{"message": "hi"}}))
		})
		It("leaves the tool message the model gets as the tool returned it", func() {
			Expect(toolMessage()).To(ContainSubstring(`"message":"hi"`))
		})
		It("is recorded after tool.result as hook.invoked then hook.result", func() {
			recorded := gateOfType(gateEvents(cfg.Store, cfg.Session.ID), tool.TypeToolCall, tool.TypeToolResult,
				TypeHookInvoked, TypeHookResult)
			Expect(gateTypes(recorded)).To(Equal([]string{tool.TypeToolCall, tool.TypeToolResult, TypeHookInvoked,
				TypeHookResult}))
		})
		It("records the point tool.after in hook.invoked", func() {
			invoked := gateOfType(gateEvents(cfg.Store, cfg.Session.ID), TypeHookInvoked)[0]
			Expect(payloadFieldText(invoked.PayloadJSON, "point")).To(Equal("tool.after"))
		})
		It("records the call id in hook.result", func() {
			Expect(payloadFieldText(results()[0], "tool_call_id")).To(Equal("call-1"))
		})
	})

	Describe("a tool.after hook on a call that a transform hook rewrote", func() {
		var seen string
		BeforeEach(func() {
			seen = filepath.Join(dir, "seen.json")
			run(gateSet(transformHook("fix", gateScript(dir, "fix", rewriteTo("rewritten"))),
				afterHook("watch", gateScript(dir, "watch", "cat > "+seen))), echoCall("call-1"))
		})
		It("sees the arguments the tool ran with", func() {
			Expect(gateToolDoc(seen)["args"]).To(Equal(map[string]any{"message": "rewritten"}))
		})
	})

	Describe("a tool.after hook on a call whose tool failed", func() {
		var seen string
		BeforeEach(func() {
			seen = filepath.Join(dir, "seen.json")
			escape := writeCall()
			escape.Arguments = json.RawMessage(`{"path":"../escaped.txt","content":"x"}`)
			streamer = &execLoopStreamer{turns: [][]provider.StreamEvent{{provCall(escape), provFinish()}, {provText("done")}}}
			cfg = execLoopFixture(streamer)
			registry, err := NewSessionToolRegistry(dir)
			if err != nil {
				panic(err)
			}
			cfg.Registry, cfg.Allowlist, cfg.Workdir = registry, []string{"fs.write"}, dir
			cfg.Hooks = gateSet(afterHook("watch", gateScript(dir, "watch", "cat > "+seen)))
			if out := runExec(ctx, cfg); !out.NoErr {
				panic("the run failed")
			}
		})
		It("sees the outcome as a failure", func() {
			Expect(gateToolDoc(seen)["result"]).To(HaveKeyWithValue("ok", false))
		})
	})

	Describe("a tool.after hook that tries to change the outcome", func() {
		DescribeTable("leaves the tool message as the tool returned it",
			func(body string) {
				run(gateSet(afterHook("sly", gateScript(dir, "sly", body))), echoCall("call-1"))
				Expect(toolMessage()).To(ContainSubstring(`"message":"hi"`))
			},
			Entry("by returning args", rewriteTo("rewritten")),
			Entry("by denying with a decision", `echo '{"decision":"deny","reason":"no"}'`),
			Entry("by denying with exit 2", "echo no >&2\nexit 2"),
			Entry("by failing", "exit 1"),
		)
		It("records a deny as the decision deny and goes on", func() {
			run(gateSet(afterHook("sly", gateScript(dir, "sly", "echo no >&2\nexit 2"))), echoCall("call-1"))
			Expect(payloadFieldText(results()[0], "decision")).To(Equal("deny"))
		})
		It("records args as a failure that is not permitted", func() {
			run(gateSet(afterHook("sly", gateScript(dir, "sly", rewriteTo("rewritten")))), echoCall("call-1"))
			Expect(payloadFieldText(results()[0], "failure")).To(Equal(tool.HookFailureFieldNotPermitted))
		})
	})

	Describe("tool.after hooks of one call", func() {
		var order string
		BeforeEach(func() {
			order = filepath.Join(dir, "order")
			first := gateScript(dir, "first", "echo first >> "+order+"\nexit 2")
			second := gateScript(dir, "second", "echo second >> "+order)
			run(gateSet(afterHook("first", first), afterHook("second", second)), echoCall("call-1"))
		})
		It("all run in file order, a deny not ending the chain", func() {
			Expect(gateLines(order)).To(Equal([]string{"first", "second"}))
		})
	})

	Describe("a tool.after hook whose matcher selects another tool", func() {
		It("does not run", func() {
			counter := filepath.Join(dir, "count")
			run(gateSet(afterHook("watch", gateScript(dir, "watch", "echo ran >> "+counter), "fs.*")), echoCall("call-1"))
			Expect(gateLines(counter)).To(BeEmpty())
		})
	})

	Describe("a call that a tool.before hook denied", func() {
		It("does not fire tool.after", func() {
			counter := filepath.Join(dir, "count")
			guard := gateScript(dir, "guard", "echo no >&2\nexit 2")
			run(gateSet(gateHook("guard", guard), afterHook("watch", gateScript(dir, "watch", "echo ran >> "+counter))),
				echoCall("call-1"))
			Expect(gateLines(counter)).To(BeEmpty())
		})
	})

	Describe("a call the allowlist refuses", func() {
		It("does not fire tool.after", func() {
			counter := filepath.Join(dir, "count")
			run(gateSet(afterHook("watch", gateScript(dir, "watch", "echo ran >> "+counter))), writeCall())
			Expect(gateLines(counter)).To(BeEmpty())
		})
	})

	Describe("checkRewrite", func() {
		allowed := func(args string) tool.HookResult {
			return tool.HookResult{Verdict: tool.HookAllow, Output: tool.HookOutput{Args: json.RawMessage(args)}}
		}
		It("fails a rewrite over the 64 KiB stdout cap", func() {
			big := `{"k":"` + string(make([]byte, tool.HookStdoutCap)) + `"}`
			res, _ := checkRewrite(transformHook("t", "/x"), allowed(big))
			Expect(res.Failure).To(Equal(tool.HookFailureInvalidOutput))
		})
		It("fails a rewrite that is not a JSON object", func() {
			res, _ := checkRewrite(transformHook("t", "/x"), allowed(`[1]`))
			Expect(res.Failure).To(Equal(tool.HookFailureInvalidOutput))
		})
		It("returns an object rewrite", func() {
			_, rewritten := checkRewrite(transformHook("t", "/x"), allowed(` {"a":1} `))
			Expect(string(rewritten)).To(Equal(`{"a":1}`))
		})
		It("ignores args of a hook that is not a transform hook", func() {
			_, rewritten := checkRewrite(gateHook("g", "/x"), allowed(`{"a":1}`))
			Expect(rewritten).To(BeNil())
		})
	})
})
