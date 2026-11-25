package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	gtypes "github.com/onsi/gomega/types"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/store"
	"github.com/baphled/flowstate/internal/tool"
)

// toolFixture is a delegate fixture whose parent runs the real loop with the
// delegate tool registered; the parent and its children share one script.
type toolFixture struct {
	*delegateFixture
	tool     *DelegateTool
	allow    []string
	registry *tool.Registry
}

func newToolFixture(turns ...[]provider.StreamEvent) *toolFixture {
	f := &toolFixture{delegateFixture: newDelegateFixture(turns...), allow: []string{"delegate"}}
	writer := agent.Profile{
		Name: "writer", Description: "writes", Tools: []string{"fs.write"}, Model: agent.InheritModel,
		MaxTurns: 2, Path: "/profiles/writer.md", SHA256: "cafe", Body: "You write.",
	}
	f.tool = NewDelegateTool(DelegateToolConfig{
		Store: f.st, Parent: f.parent, Streamer: f.streamer, Pool: f.pool, Workdir: f.workdir,
		Profiles: []agent.Profile{writer, f.profile},
	})
	f.registry = tool.NewRegistry()
	check(f.registry.Register(f.tool))
	check(f.registry.Register(tool.NewEcho()))
	return f
}

// runParent runs the parent loop under ctx with the fixture's allowlist.
func (f *toolFixture) runParent(ctx context.Context) (*ExecLoopResult, error) {
	lease, _ := acquireTestLease(f.st, "exec:"+f.parent.ID, "cli")
	return RunExecLoop(ctx, ExecLoopConfig{
		Store: f.st, Session: f.parent, Streamer: f.streamer, Registry: f.registry, Allowlist: f.allow,
		Pool: f.pool, TurnBudget: 1, Lease: lease, MaxTurns: 4,
	})
}

// invoke calls the tool directly with JSON arguments.
func (f *toolFixture) invoke(args string) (any, error) {
	return f.tool.Invoke(context.Background(), json.RawMessage(args))
}

// parentTypes lists the parent's event types in order.
func (f *toolFixture) parentTypes() []string {
	var out []string
	for _, e := range must(f.st.Replay(context.Background(), f.parent.ID)) {
		out = append(out, e.Type)
	}
	return out
}

// childIDs lists every session id except the parent's.
func (f *toolFixture) childIDs() []string {
	var out []string
	for _, s := range must(f.st.ListSessions(context.Background(), "")) {
		if s.ID != f.parent.ID {
			out = append(out, s.ID)
		}
	}
	return out
}

func delegateCall(args string) provider.ToolCall {
	return provider.ToolCall{ID: "d1", Name: "delegate", Arguments: json.RawMessage(args)}
}

// toolNames lists the tool names of one provider request.
func toolNames(req *provider.ChatRequest) []string {
	names := []string{}
	for _, d := range req.Tools {
		names = append(names, d.Name)
	}
	return names
}

// toolResultContent is the model-visible content of the last tool message sent.
func (f *toolFixture) toolResultContent() string {
	msgs := f.streamer.lastRequest().Messages
	return msgs[len(msgs)-1].Content
}

// blockAfterFirst serves its script for the first request and blocks every later
// one until the context is cancelled.
type blockAfterFirst struct {
	execLoopStreamer
	started chan struct{}
	once    sync.Once
}

func (b *blockAfterFirst) Stream(ctx context.Context, req *provider.ChatRequest) <-chan provider.StreamEvent {
	if len(b.reqs) == 0 {
		return b.execLoopStreamer.Stream(ctx, req)
	}
	b.once.Do(func() { close(b.started) })
	out := make(chan provider.StreamEvent)
	go func() {
		<-ctx.Done()
		close(out)
	}()
	return out
}

// receiptFailSink fails every append.
type receiptFailSink struct{}

func (receiptFailSink) Append(context.Context, string, string, any) (int64, error) {
	return 0, errors.New("disk full")
}

// namedTool is a custom tool that reuses the delegate name.
type namedTool struct{}

func (namedTool) Name() string { return "delegate" }
func (namedTool) Invoke(context.Context, json.RawMessage) (any, error) {
	return nil, nil
}

// badArgEntries are argument objects the tool refuses, with the message they get.
func badArgEntries() []TableEntry {
	return []TableEntry{
		Entry("an unknown field", `{"agent":"reviewer","task":"t","extra":1}`, "unknown field"),
		Entry("a missing agent", `{"task":"t"}`, "agent is required"),
		Entry("a missing task", `{"agent":"reviewer"}`, "task is required"),
		Entry("a blank task", `{"agent":"reviewer","task":"  "}`, "task is required"),
		Entry("extra JSON after the object", `{"agent":"reviewer","task":"t"} {}`, "extra data"),
		Entry("arguments that are not an object", `"reviewer"`, "invalid arguments"),
		Entry("empty arguments", ``, "invalid arguments"),
		Entry("empty tasks", `{"tasks":[]}`, "tasks must not be empty"),
		Entry("null tasks", `{"tasks":null}`, "tasks must not be empty"),
		Entry("tasks that are not an array", `{"tasks":"x"}`, "invalid tasks"),
		Entry("nine tasks", tasksJSON(9), "too many tasks: 9, the limit is 8"),
		Entry("tasks with agent and task", `{"agent":"reviewer","task":"t","tasks":[{"agent":"reviewer","task":"t"}]}`,
			"tasks cannot be combined"),
		Entry("tasks with a cwd", `{"cwd":".","tasks":[{"agent":"reviewer","task":"t"}]}`, "tasks cannot be combined"),
		Entry("tasks with a resume", `{"resume":"abc","tasks":[{"agent":"reviewer","task":"t"}]}`, "resume cannot be combined with tasks"),
		Entry("a task entry with an unknown field", `{"tasks":[{"agent":"reviewer","task":"t","x":1}]}`, "unknown field"),
		Entry("a task entry without an agent", `{"tasks":[{"agent":"reviewer","task":"t"},{"task":"t"}]}`, "tasks[1]: agent is required"),
		Entry("a blank task in entry 2", `{"tasks":[{"agent":"reviewer","task":"t"},{"agent":"reviewer","task":"t"},`+
			`{"agent":"reviewer","task":" "}]}`, "tasks[2]: task is required"),
		Entry("a NUL in an entry's task", `{"tasks":[{"agent":"reviewer","task":"a\u0000b"}]}`, "tasks[0]: task must not contain NUL"),
		Entry("an unknown agent in entry 1", `{"tasks":[{"agent":"reviewer","task":"t"},{"agent":"ghost","task":"t"}]}`,
			`tasks[1]: unknown agent "ghost"; available agents: reviewer, writer`),
		Entry("a resume with an agent", `{"resume":"abc","agent":"reviewer"}`, "resume cannot be combined with agent or cwd"),
		Entry("a resume with a cwd", `{"resume":"abc","cwd":"."}`, "resume cannot be combined with agent or cwd"),
		Entry("a resume with a NUL in the follow-up", `{"resume":"abc","task":"a\u0000b"}`, "NUL, ESC or C1"),
		Entry("an unknown agent", `{"agent":"ghost","task":"t"}`, `unknown agent "ghost"; available agents: reviewer, writer`),
		Entry("a cwd outside the parent's", `{"agent":"reviewer","task":"t","cwd":"../"}`, "inside the parent's"),
		Entry("a task with NUL", `{"agent":"reviewer","task":"a\u0000b"}`, "NUL, ESC or C1"),
		Entry("a task with ESC", `{"agent":"reviewer","task":"a\u001b[31mb"}`, "NUL, ESC or C1"),
		Entry("a task with the lowest C1 control", `{"agent":"reviewer","task":"a\u0080b"}`, "NUL, ESC or C1"),
		Entry("a task with the highest C1 control", `{"agent":"reviewer","task":"a\u009fb"}`, "NUL, ESC or C1"),
		Entry("a task with a C1 control", `{"agent":"reviewer","task":"a\u009bb"}`, "NUL, ESC or C1"),
	}
}

// tasksJSON is a tasks argument holding n valid entries.
func tasksJSON(n int) string {
	entries := make([]string, n)
	for i := range entries {
		entries[i] = `{"agent":"reviewer","task":"task-` + strconv.Itoa(i) + `"}`
	}
	return `{"tasks":[` + strings.Join(entries, ",") + `]}`
}

func hasTypes(want ...string) gtypes.GomegaMatcher { return Equal(want) }

var _ = Describe("The delegate tool", func() {
	DescribeTable("refuses bad arguments with a clear error",
		func(args, message string) {
			f := newToolFixture()
			_, err := f.invoke(args)
			Expect(err).To(MatchError(ContainSubstring(message)))
		}, badArgEntries())

	DescribeTable("creates no child for a refused call",
		func(args, _ string) {
			f := newToolFixture()
			_, _ = f.invoke(args)
			Expect(f.childIDs()).To(BeEmpty())
		}, badArgEntries())

	Describe("its definition", func() {
		It("lists the loaded profiles in name order", func() {
			Expect(newToolFixture().tool.Definition().Description).To(ContainSubstring(
				"Available agents:\n- reviewer: reviews\n- writer: writes"))
		})
		It("bounds tasks at 8 entries in its schema", func() {
			Expect(string(newToolFixture().tool.Definition().Parameters)).To(ContainSubstring(`"tasks":{"type":"array","minItems":1,"maxItems":8`))
		})
		It("requires agent and task in each tasks entry", func() {
			Expect(string(newToolFixture().tool.Definition().Parameters)).To(ContainSubstring(`"required":["agent","task"],"additionalProperties":false}}}`))
		})
		It("says up to 8 tasks run 4 at a time", func() {
			Expect(newToolFixture().tool.Definition().Description).To(ContainSubstring("up to 8 entries"))
		})
		It("says no agent is available when no profile was loaded", func() {
			def := NewDelegateTool(DelegateToolConfig{}).Definition()
			Expect(def.Description).To(ContainSubstring("No agents are available"))
		})
		It("refuses a call when no profile was loaded", func() {
			_, err := NewDelegateTool(DelegateToolConfig{}).Invoke(context.Background(), json.RawMessage(`{"agent":"a","task":"t"}`))
			Expect(err).To(MatchError(ContainSubstring("no agents are available")))
		})
		It("is produced by SessionToolDefinitions for the allowlist entry", func() {
			f := newToolFixture()
			defs := must(SessionToolDefinitions(f.registry, []string{"delegate"}))
			Expect(defs[0].Name).To(Equal("delegate"))
		})
		It("is not given to a custom tool that reuses the name", func() {
			registry := tool.NewRegistry()
			check(registry.Register(namedTool{}))
			_, err := SessionToolDefinitions(registry, []string{"delegate"})
			Expect(err).To(MatchError(ContainSubstring("no qualified schema")))
		})
	})

	Describe("a parent run whose model delegates", func() {
		var f *toolFixture
		var res *ExecLoopResult
		var err error
		BeforeEach(func() {
			f = newToolFixture(
				callTurn(delegateCall(`{"agent":"reviewer","task":"look"}`)), finishText("child says hi"), finishText("parent done"))
			res, err = f.runParent(context.Background())
		})
		It("completes the parent without an error", func() { Expect(err).To(Succeed()) })
		It("ends with the parent's own final answer", func() { Expect(res.FinalContent).To(Equal("parent done")) })
		It("hands the model the child's id, status, turns and text as JSON", func() {
			Expect(f.toolResultContent()).To(MatchJSON(`{"child":"` + f.childIDs()[0] + `","status":"completed","turns":1,"text":"child says hi"}`))
		})
		It("writes the receipt between the tool.call and the tool.result of that call", func() {
			Expect(f.parentTypes()[2:6]).To(hasTypes("tool.call", TypeDelegationStarted, TypeDelegationCompleted, "tool.result"))
		})
		It("offers the parent the delegate tool", func() { Expect(toolNames(f.streamer.reqs[0])).To(Equal([]string{"delegate"})) })
		It("never offers the child the delegate tool", func() { Expect(toolNames(f.streamer.reqs[1])).To(Equal([]string{"fs.read"})) })
	})

	Describe("a child that fails", func() {
		var f *toolFixture
		var res *ExecLoopResult
		var err error
		BeforeEach(func() {
			f = newToolFixture(
				callTurn(delegateCall(`{"agent":"reviewer","task":"look"}`)), []provider.StreamEvent{provError("boom")}, finishText("parent done"))
			res, err = f.runParent(context.Background())
		})
		It("does not fail the parent", func() { Expect(err).To(Succeed()) })
		It("lets the parent finish with its answer", func() { Expect(res.FinalContent).To(Equal("parent done")) })
		It("hands the model an error tool result", func() { Expect(f.toolResultContent()).To(HavePrefix("error: ")) })
		It("names the child's status and reason", func() {
			Expect(f.toolResultContent()).To(ContainSubstring("child " + f.childIDs()[0] + " failed after 1 turns"))
		})
		It("gives the reason", func() { Expect(f.toolResultContent()).To(ContainSubstring("boom")) })
		It("leaves the parent active", func() { Expect(f.status(f.parent.ID)).To(Equal("active")) })
		It("marks the child failed", func() { Expect(f.status(f.childIDs()[0])).To(Equal("failed")) })
		It("records the failed status in the receipt", func() { Expect(f.receiptPayload()).To(HaveKeyWithValue("status", "failed")) })
	})

	Describe("a read-only reviewer asked to write", func() {
		var f *toolFixture
		BeforeEach(func() {
			f = newToolFixture(
				callTurn(delegateCall(`{"agent":"reviewer","task":"look"}`)), callTurn(writeCall()),
				finishText("could not write"), finishText("parent done"))
			if _, err := f.runParent(context.Background()); err != nil {
				panic(err)
			}
		})
		It("writes no file", func() {
			_, err := os.Stat(filepath.Join(f.workdir, "new.txt"))
			Expect(os.IsNotExist(err)).To(BeTrue())
		})
		It("records the denial in the child's log", func() {
			Expect(f.events(f.childIDs()[0], tool.TypeToolResult)[0].PayloadJSON).To(ContainSubstring("tool_denied"))
		})
	})

	Describe("a call when delegate is not on the allowlist", func() {
		var f *toolFixture
		BeforeEach(func() {
			f = newToolFixture(callTurn(delegateCall(`{"agent":"reviewer","task":"look"}`)), finishText("parent done"))
			f.allow = []string{"echo"}
			if _, err := f.runParent(context.Background()); err != nil {
				panic(err)
			}
		})
		It("is denied in the parent's log", func() {
			Expect(f.events(f.parent.ID, tool.TypeToolResult)[0].PayloadJSON).To(ContainSubstring("tool_denied"))
		})
		It("creates no child", func() { Expect(f.childIDs()).To(BeEmpty()) })
		It("tells the model the call failed", func() { Expect(f.toolResultContent()).To(HavePrefix("error: ")) })
	})

	Describe("a cwd outside the parent's directory", func() {
		var f *toolFixture
		BeforeEach(func() {
			f = newToolFixture(
				callTurn(delegateCall(`{"agent":"reviewer","task":"look","cwd":"../"}`)), finishText("parent done"))
			if _, err := f.runParent(context.Background()); err != nil {
				panic(err)
			}
		})
		It("hands the model an error naming the confinement", func() {
			Expect(f.toolResultContent()).To(ContainSubstring("inside the parent's"))
		})
		It("creates no child", func() { Expect(f.childIDs()).To(BeEmpty()) })
	})

	Describe("a child's long answer", func() {
		longText := func(text string) any {
			f := newToolFixture(finishText(text))
			out, err := f.invoke(`{"agent":"reviewer","task":"look"}`)
			if err != nil {
				panic(err)
			}
			return out.(DelegateToolResult).Text
		}
		It("is cut at 2000 lines with a notice", func() {
			Expect(longText(strings.Repeat("x\n", 2500))).To(Equal(
				strings.Repeat("x\n", 2000) + "\n[truncated: the child's message was cut at 50 KB / 2000 lines]"))
		})
		It("is cut at 50 KB with a notice", func() {
			Expect(longText(strings.Repeat("x", 60*1024))).To(Equal(
				strings.Repeat("x", 50*1024) + "\n[truncated: the child's message was cut at 50 KB / 2000 lines]"))
		})
		It("is cut on a character boundary", func() {
			text := strings.Repeat("x", 50*1024-1) + "é" // the two bytes straddle the limit
			Expect(longText(text)).To(HavePrefix(strings.Repeat("x", 50*1024-1) + "\n[truncated"))
		})
		It("is returned whole under both limits", func() {
			Expect(longText(strings.Repeat("x\n", 1999))).To(Equal(strings.Repeat("x\n", 1999)))
		})
	})

	Describe("an oversized request or failure", func() {
		It("refuses a task over 64 KiB with a clear error", func() {
			_, err := newToolFixture().invoke(`{"agent":"reviewer","task":"` + strings.Repeat("x", 64*1024+1) + `"}`)
			Expect(err).To(MatchError(ContainSubstring("task is too long")))
		})
		It("creates no child for a task over 64 KiB", func() {
			f := newToolFixture()
			_, _ = f.invoke(`{"agent":"reviewer","task":"` + strings.Repeat("x", 64*1024+1) + `"}`)
			Expect(f.childIDs()).To(BeEmpty())
		})
		It("accepts a task of exactly 64 KiB", func() {
			f := newToolFixture(finishText("ok"))
			out, err := f.invoke(`{"agent":"reviewer","task":"` + strings.Repeat("x", 64*1024) + `"}`)
			if err != nil {
				panic(err)
			}
			Expect(out.(DelegateToolResult).Status).To(Equal(DelegateStatusCompleted))
		})
		It("caps the agent name echoed for an unknown agent", func() {
			_, err := newToolFixture().invoke(`{"agent":"` + strings.Repeat("n", 5000) + `","task":"t"}`)
			Expect(len(err.Error())).To(BeNumerically("<", 300))
		})
		It("caps a long failure reason with the truncation notice", func() {
			f := newToolFixture(callTurn(delegateCall(`{"agent":"reviewer","task":"look"}`)),
				[]provider.StreamEvent{provError(strings.Repeat("r", 200*1024))}, finishText("parent done"))
			if _, err := f.runParent(context.Background()); err != nil {
				panic(err)
			}
			Expect(f.toolResultContent()).To(HaveSuffix("cut at 50 KB / 2000 lines]"))
		})
		It("keeps a capped failure under 60 KB", func() {
			f := newToolFixture(callTurn(delegateCall(`{"agent":"reviewer","task":"look"}`)),
				[]provider.StreamEvent{provError(strings.Repeat("r", 200*1024))}, finishText("parent done"))
			if _, err := f.runParent(context.Background()); err != nil {
				panic(err)
			}
			Expect(len(f.toolResultContent())).To(BeNumerically("<", 60*1024))
		})
		It("clips a profile description to 200 characters in the definition", func() {
			p := agent.Profile{Name: "a", Description: strings.Repeat("d", 201)}
			desc := NewDelegateTool(DelegateToolConfig{Profiles: []agent.Profile{p}}).Definition().Description
			Expect(desc).To(HaveSuffix("\n- a: " + strings.Repeat("d", 200) + "..."))
		})
		It("keeps a description of exactly 200 characters whole", func() {
			p := agent.Profile{Name: "a", Description: strings.Repeat("d", 200)}
			desc := NewDelegateTool(DelegateToolConfig{Profiles: []agent.Profile{p}}).Definition().Description
			Expect(desc).To(HaveSuffix("\n- a: " + strings.Repeat("d", 200)))
		})
		It("keeps 200 CJK characters whole", func() {
			s := strings.Repeat("日", 200)
			Expect(clipRunes(s, 200)).To(Equal(s))
		})
		It("cuts 201 CJK characters to 200 runes and an ellipsis", func() {
			Expect(clipRunes(strings.Repeat("日", 201), 200)).To(Equal(strings.Repeat("日", 200) + "..."))
		})
		It("leaves valid UTF-8 when it cuts emoji", func() {
			Expect(utf8.ValidString(clipRunes(strings.Repeat("😀", 201), 200))).To(BeTrue())
		})
		It("accepts a task with newlines and tabs", func() {
			args := must(parseDelegateArgs(json.RawMessage(`{"agent":"reviewer","task":"a\n\tb"}`)))
			Expect(args.Task).To(Equal("a\n\tb"))
		})
		It("lists at most 32 profiles and counts the rest", func() {
			profiles := make([]agent.Profile, 40)
			for i := range profiles {
				profiles[i] = agent.Profile{Name: "a" + string(rune('a'+i/26)) + string(rune('a'+i%26)), Description: "d"}
			}
			desc := NewDelegateTool(DelegateToolConfig{Profiles: profiles}).Definition().Description
			Expect(desc).To(HaveSuffix("\n- abf: d\n(and 8 more)"))
		})
	})

	Describe("a failure to record the receipt in the parent's log", func() {
		It("terminates the run like a tool.call append failure", func() {
			f := newToolFixture(finishText("ok"))
			_, err := f.tool.Invoke(withParentEvents(context.Background(), receiptFailSink{}), json.RawMessage(`{"agent":"reviewer","task":"t"}`))
			Expect(err).To(MatchError(tool.ErrEventAppend))
		})
	})

	Describe("a parent cancelled while its child runs", func() {
		var f *toolFixture
		var err error
		BeforeEach(func() {
			blocker := &blockAfterFirst{started: make(chan struct{})}
			blocker.turns = [][]provider.StreamEvent{callTurn(delegateCall(`{"agent":"reviewer","task":"look"}`))}
			f = newToolFixture()
			f.streamer = &blocker.execLoopStreamer
			f.tool.cfg.Streamer = blocker
			cctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			done := make(chan error, 1)
			lease, _ := acquireTestLease(f.st, "exec:"+f.parent.ID, "cli")
			go func() {
				_, e := RunExecLoop(cctx, ExecLoopConfig{
					Store: f.st, Session: f.parent, Streamer: blocker, Registry: f.registry, Allowlist: f.allow,
					Lease: lease, MaxTurns: 4,
				})
				done <- e
			}()
			select {
			case <-blocker.started:
			case <-time.After(10 * time.Second):
				panic("the child never reached the provider")
			}
			cancel(&CancelCause{Reason: "sigterm"})
			select {
			case err = <-done:
			case <-time.After(10 * time.Second):
				panic("the parent did not return after cancellation")
			}
		})
		It("ends the parent run as cancelled instead of reporting a tool result", func() {
			Expect(err).To(MatchError(context.Canceled))
		})
		It("cancels the child", func() { Expect(f.status(f.childIDs()[0])).To(Equal("cancelled")) })
		It("records the parent's reason in the receipt", func() { Expect(f.receiptPayload()).To(HaveKeyWithValue("reason", "sigterm")) })
	})
})

var _ = Describe("The delegate tool's resume", func() {
	It("is denied when delegate is not on the allowlist", func() {
		f := newToolFixture(callTurn(delegateCall(`{"resume":"abc"}`)), finishText("parent done"))
		f.allow = []string{"echo"}
		must(f.runParent(context.Background()))
		Expect(f.events(f.parent.ID, tool.TypeToolResult)[0].PayloadJSON).To(ContainSubstring("tool_denied"))
	})

	It("refuses an unknown child as a tool error", func() {
		f := newToolFixture()
		_, err := f.invoke(`{"resume":"ghost"}`)
		Expect(err).To(MatchError(ContainSubstring("is not a child of this session")))
	})

	Describe("after the parent crashed mid-delegate", func() {
		var f *toolFixture
		var child *store.Session
		var res *ExecLoopResult
		BeforeEach(func() {
			ctx := context.Background()
			f = newToolFixture()
			child = f.seedChild(f.parent, 2)
			// The crash: the call and delegation.started are in the log, no result, no receipt, and the
			// killed runner's lease is still on the child.
			mustAppendEvent(ctx, f.st, f.parent.ID, "tool:delegate", tool.TypeToolCall, map[string]any{
				"tool": "delegate", "tool_call_id": "d0", "args": map[string]any{"agent": "reviewer", "task": "look"},
			})
			mustAppendEvent(ctx, f.st, f.parent.ID, "engine", TypeDelegationStarted, map[string]any{"child_id": child.ID})
			must(f.st.AcquireLease(ctx, "exec:"+child.ID, delegateLeasePrefix+"dead-run"))
			check(RecoverCrashedSession(ctx, f.st, f.parent.ID))
			f.streamer.turns = [][]provider.StreamEvent{
				callTurn(delegateCall(`{"resume":"` + child.ID + `"}`)), finishText("child done"), finishText("parent done"),
			}
			res = must(f.runParent(ctx))
		})
		It("lets the parent finish", func() { Expect(res.FinalContent).To(Equal("parent done")) })
		It("tells the model about the interruption in its first request", func() {
			msgs := f.streamer.reqs[0].Messages
			Expect(msgs[len(msgs)-1].Content).To(ContainSubstring("interrupted by a crash"))
		})
		It("names the child and how to resume it in that request", func() {
			msgs := f.streamer.reqs[0].Messages
			Expect(msgs[len(msgs)-1].Content).To(ContainSubstring(child.ID + " (active): resume it with delegate"))
		})
		It("completes the resumed child", func() { Expect(f.status(child.ID)).To(Equal("completed")) })
		It("gives the resumed child its earlier turns", func() {
			Expect(roles(f.streamer.reqs[1])).To(Equal([]string{"system", "user", "assistant", "tool", "assistant", "tool"}))
		})
		It("hands the parent the child's final text", func() {
			Expect(f.toolResultContent()).To(ContainSubstring(`"text":"child done"`))
		})
		It("records the receipt as resumed", func() { Expect(f.receiptPayload()).To(HaveKeyWithValue("resumed", true)) })
	})
})

var _ = Describe("Compaction in a child resumed through the delegate tool", func() {
	var (
		f   *toolFixture
		seq *seqStreamer
		id  string
	)
	// resume seeds a crashed child of two turns whose profile holds the window, scripts its first resumed
	// turn to be over its default line (see bigReadTurn), and resumes it by the tool.
	resume := func(settings CompactionConfig, window int) {
		f = newToolFixture()
		for i := range f.tool.cfg.Profiles {
			if f.tool.cfg.Profiles[i].Name == f.profile.Name {
				f.tool.cfg.Profiles[i].MaxTurns, f.tool.cfg.Profiles[i].ContextWindow = 6, window
			}
		}
		f.profile.MaxTurns = 6
		f.tool.cfg.Compaction = settings
		id = f.seedChild(f.parent, 2).ID
		seq = &seqStreamer{calls: map[string]int{}, seqs: map[string][][]provider.StreamEvent{
			delegateBodyPrompt:  {bigReadTurn("r1"), finishText("resumed")},
			compactSystemPrompt: {summaryTurn("s")},
		}}
		f.tool.cfg.Streamer = seq
		must(f.invoke(`{"resume":"` + id + `"}`))
	}

	Context("with compaction.enabled false", func() {
		BeforeEach(func() { off := false; resume(CompactionConfig{Enabled: &off}, 100000) })
		It("never calls the summariser for the resumed child", func() { Expect(seq.summaries()).To(BeZero()) })
	})

	Context("with the default settings and a profile window", func() {
		BeforeEach(func() { resume(CompactionConfig{}, 100000) })
		It("compacts the resumed child by its profile's window", func() {
			Expect(f.events(id, TypeContextCompacted)).To(HaveLen(1))
		})
	})
})
