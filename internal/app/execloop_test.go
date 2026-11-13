package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/store"
	"github.com/baphled/flowstate/internal/tool"
)

// execLoopStreamer replays one scripted stream per Stream call.
type execLoopStreamer struct {
	turns [][]provider.StreamEvent
	reqs  []*provider.ChatRequest
}

func (f *execLoopStreamer) Name() string     { return "scripted" }
func (f *execLoopStreamer) Models() []string { return []string{"scripted-model"} }

func (f *execLoopStreamer) lastRequest() *provider.ChatRequest {
	if len(f.reqs) == 0 {
		return nil
	}
	return f.reqs[len(f.reqs)-1]
}
func (f *execLoopStreamer) Chat(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	return &provider.ChatResponse{Content: "ignored"}, nil
}
func (f *execLoopStreamer) Stream(ctx context.Context, req *provider.ChatRequest) <-chan provider.StreamEvent {
	f.reqs = append(f.reqs, req)
	var events []provider.StreamEvent
	if len(f.reqs) <= len(f.turns) {
		events = f.turns[len(f.reqs)-1]
	}
	out := make(chan provider.StreamEvent, len(events)+1)
	for _, e := range events {
		out <- e
	}
	close(out)
	return out
}

// execLoopFixture opens a temp store with one seeded session.
func execLoopFixture(streamer *execLoopStreamer) ExecLoopConfig {
	st, sess := execLoopSeed()
	DeferCleanup(func() { _ = st.Close() })
	registry, err := NewSessionToolRegistry("")
	execMust(err)
	return ExecLoopConfig{Store: st, Session: sess, Streamer: streamer, Registry: registry, Allowlist: []string{"echo"}, LeaseItem: "exec:" + sess.ID, LeaseAgent: "agent-a", MaxTurns: 4}
}
func echoCall(id string) provider.ToolCall { // an allowlisted echo invocation
	return provider.ToolCall{ID: id, Name: "echo", Arguments: json.RawMessage(`{"message":"hi"}`)}
}

// execLoopFeedback bundles a run result with the next provider request.
type execLoopFeedback struct {
	Result     *ExecLoopResult
	NoErr      bool
	HistoryLen int
	Denied     bool
	Shape      requestShape
}

// execMust panics on arrange-only fixture failures.
func execMust(err error) {
	if err != nil {
		panic(err)
	}
}
func execLoopSeed() (*store.Store, *store.Session) {
	st, err := store.OpenMemory()
	execMust(err)
	ctx := context.Background()
	sess, cerr := st.CreateSession(ctx, "exec-loop-1", "scripted", "scripted-model", "harness", "sha")
	execMust(cerr)
	w, werr := st.NewEventWriter(ctx, sess.ID)
	execMust(werr)
	_, aerr := w.Append(ctx, session.ActorUser, session.TypeTurnStart, map[string]any{"task": "do the thing"})
	execMust(aerr)
	execMust(w.Close())
	return st, sess
}

// execLoopOutcome bundles a run's outcome for structural assertion.
type execLoopOutcome struct {
	Result                          *ExecLoopResult
	ErrConfig, ErrBudget, ErrFenced bool
	ErrProvider, NoErr              bool
	EventLog                        string
	LastStatus, SessionStatus       string
}

// runExec collects one run's outcome.
func runExec(ctx context.Context, cfg ExecLoopConfig) execLoopOutcome {
	res, err := RunExecLoop(ctx, cfg)
	out := execLoopOutcome{Result: res, NoErr: err == nil, ErrConfig: errors.Is(err, ErrExecLoopConfig),
		ErrBudget: errors.Is(err, ErrExecLoopBudget), ErrFenced: errors.Is(err, ErrExecLoopFenced), ErrProvider: errors.Is(err, ErrProvider)}
	events, rerr := cfg.Store.Replay(ctx, cfg.Session.ID)
	execMust(rerr)
	types := make([]string, 0, len(events))
	for _, e := range events {
		types = append(types, e.Type)
	}
	out.EventLog = strings.Join(types, ",")
	if len(events) > 0 {
		out.LastStatus = payloadFieldText(events[len(events)-1].PayloadJSON, "status")
	}
	sess, gerr := cfg.Store.GetSession(ctx, cfg.Session.ID)
	execMust(gerr)
	out.SessionStatus = sess.Status
	return out
}

func provErrorLoopFixture() ExecLoopConfig { // first stream terminates in an error
	return execLoopFixture(&execLoopStreamer{turns: [][]provider.StreamEvent{{provError("boom")}}})
}

// replayLoopFixture stops a two-tool-call run after MaxTurns for a later resume.
func replayLoopFixture(ctx context.Context) ExecLoopConfig {
	call := []provider.StreamEvent{provText("need a tool"), provCall(echoCall("call-6")), provFinish()}
	first := &execLoopStreamer{turns: [][]provider.StreamEvent{call, {provText("need another"), provCall(echoCall("call-7")), provFinish()}}}
	cfg := execLoopFixture(first)
	cfg.MaxTurns = 2
	if out := runExec(ctx, cfg); out.Result.Turns != 2 {
		panic("replay fixture: expected the first run to stop after two turns")
	}
	return cfg
}

func deniedCallLoopFixture() ExecLoopConfig { // non-allowlisted fs.write call + recovery turn
	rogue := provider.ToolCall{ID: "call-5", Name: "fs.write", Arguments: json.RawMessage(`{"path":"x","content":"y"}`)}
	streamer := &execLoopStreamer{turns: [][]provider.StreamEvent{{provCall(rogue), provFinish()}, {provText("recovered from denial")}}}
	cfg := execLoopFixture(streamer)
	cfg.Allowlist = []string{"echo"}
	return cfg
}

func toolTurnLoopFixture() ExecLoopConfig { // allowlisted echo call + finishing turn
	return execLoopFixture(&execLoopStreamer{turns: [][]provider.StreamEvent{{provText("need a tool"), provCall(echoCall("call-1")), provFinish()}, {provText("finished after tool")}}})
}

func annotateLoopFixture() ExecLoopConfig { // bare tool call, persisted ids read back
	return execLoopFixture(&execLoopStreamer{turns: [][]provider.StreamEvent{{provCall(echoCall("call-2")), provFinish()}, {provText("done")}}})
}

func budgetLoopFixture(ctx context.Context) ExecLoopConfig { // two budgeted turns + an unaffordable third
	turn := []provider.StreamEvent{provCall(echoCall("call-3")), provFinish()}
	cfg := execLoopFixture(&execLoopStreamer{turns: [][]provider.StreamEvent{turn, turn, {provText("never")}}})
	cfg.Pool, cfg.TurnBudget = NewBudgetPool(2), 1
	return cfg
}

func fencedLoopFixture(ctx context.Context) ExecLoopConfig { // lease already held by agent-b
	streamer := &execLoopStreamer{turns: [][]provider.StreamEvent{{provText("late")}}}
	cfg := execLoopFixture(streamer)
	_, _ = cfg.Store.AcquireLease(ctx, cfg.LeaseItem, "agent-b")
	return cfg
}

// annotatedCallIDs returns persisted tool event call ids.
func annotatedCallIDs(ctx context.Context, cfg ExecLoopConfig) []string {
	events, rerr := cfg.Store.Replay(ctx, cfg.Session.ID)
	execMust(rerr)
	ids := []string{}
	for _, e := range events {
		if e.Type == tool.TypeToolCall || e.Type == tool.TypeToolResult {
			ids = append(ids, payloadFieldText(e.PayloadJSON, "tool_call_id"))
		}
	}
	return ids
}

// requestShape summarises a provider request's messages.
type requestShape struct{ AssistantCalls, ToolResults []string }

func requestSummary(req *provider.ChatRequest) requestShape {
	shape := requestShape{AssistantCalls: []string{}, ToolResults: []string{}}
	if req == nil {
		return shape
	}
	for _, m := range req.Messages {
		for _, c := range m.ToolCalls {
			shape.AssistantCalls = append(shape.AssistantCalls, c.ID)
		}
		if m.Role == "tool" {
			shape.ToolResults = append(shape.ToolResults, m.ToolCallID)
		}
	}
	return shape
}

var _ = Describe("Provider-driven execution loop", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})
	It("runs a plain streamed turn to completion without tool calls", func() {
		streamer := &execLoopStreamer{turns: [][]provider.StreamEvent{{provText("all done")}}}
		cfg := execLoopFixture(streamer)
		out := runExec(ctx, cfg)
		Expect(out).To(Equal(execLoopOutcome{Result: &ExecLoopResult{Turns: 1, FinalContent: "all done"}, NoErr: true, EventLog: "turn.start,assistant.delta,assistant.message", SessionStatus: "active"}))
	})
	It("executes a streamed tool call through the real loop and feeds the correlated result to the next turn", func() {
		cfg := toolTurnLoopFixture()
		out := runExec(ctx, cfg)
		second := cfg.Streamer.(*execLoopStreamer).lastRequest()
		Expect(execLoopFeedback{Result: out.Result, NoErr: out.NoErr, HistoryLen: len(second.Messages), Shape: requestSummary(second)}).To(Equal(execLoopFeedback{Result: &ExecLoopResult{Turns: 2, FinalContent: "finished after tool", ToolCalls: 1}, NoErr: true, HistoryLen: 3, Shape: requestShape{AssistantCalls: []string{"call-1"}, ToolResults: []string{"call-1"}}}))
	})
	It("annotates tool events with the provider call id in the persisted log", func() {
		cfg := annotateLoopFixture()
		Expect(execLoopFeedback{NoErr: runExec(ctx, cfg).NoErr, Shape: requestShape{ToolResults: annotatedCallIDs(ctx, cfg)}}).To(Equal(execLoopFeedback{NoErr: true, Shape: requestShape{ToolResults: []string{"call-2", "call-2"}}}))
	})
	DescribeTable("stops a spent budget pool safely", func(extract func(execLoopOutcome) any, want any) {
		out := runExec(ctx, budgetLoopFixture(ctx))
		Expect(extract(out)).To(Equal(want))
	},
		Entry("with typed ErrExecLoopBudget", func(o execLoopOutcome) any { return o.ErrBudget }, true),
		Entry("with a persisted budget_exhausted turn.end status", func(o execLoopOutcome) any { return o.LastStatus }, "budget_exhausted"))
	It("marks the session failed on a terminal stream error", func() {
		Expect(runExec(ctx, provErrorLoopFixture()).SessionStatus).To(Equal("failed"))
	})
	It("denies a non-allowlisted tool call and feeds explicit error feedback to the provider", func() {
		cfg := deniedCallLoopFixture()
		out := runExec(ctx, cfg)
		second := cfg.Streamer.(*execLoopStreamer).lastRequest()
		Expect(execLoopFeedback{Result: out.Result, NoErr: out.NoErr, HistoryLen: len(second.Messages), Denied: strings.Contains(second.Messages[2].Content, "not on session allowlist"), Shape: requestSummary(second)}).To(Equal(execLoopFeedback{Result: &ExecLoopResult{Turns: 2, FinalContent: "recovered from denial", ToolCalls: 1}, NoErr: true, HistoryLen: 3, Denied: true, Shape: requestShape{AssistantCalls: []string{"call-5"}, ToolResults: []string{"call-5"}}}))
	})
	It("resumes after a denial by replaying the correlated denial feedback into the next provider request", func() {
		cfg := deniedCallLoopFixture()
		Expect(runExec(ctx, cfg).NoErr).To(BeTrue())
		resume := &execLoopStreamer{turns: [][]provider.StreamEvent{{provText("resumed after denial")}}}
		cfg.Streamer, cfg.MaxTurns = resume, 4
		resumed := runExec(ctx, cfg)
		Expect(execLoopFeedback{Result: resumed.Result, NoErr: resumed.NoErr, HistoryLen: len(resume.lastRequest().Messages), Denied: strings.Contains(resume.lastRequest().Messages[2].Content, "not on the session allowlist"), Shape: requestSummary(resume.lastRequest())}).To(Equal(execLoopFeedback{Result: &ExecLoopResult{Turns: 1, FinalContent: "resumed after denial"}, NoErr: true, HistoryLen: 3, Denied: true, Shape: requestShape{AssistantCalls: []string{"call-5"}, ToolResults: []string{"call-5"}}}))
	})
	It("fails closed with ErrExecLoopConfig on a misconfigured loop", func() {
		streamer := &execLoopStreamer{}
		cfg := execLoopFixture(streamer)
		cfg.MaxTurns = 0
		Expect(runExec(ctx, cfg).ErrConfig).To(BeTrue())
	})
	DescribeTable("stops under a foreign lease", func(step func(cfg ExecLoopConfig) any, want any) {
		cfg := fencedLoopFixture(ctx)
		runExec(ctx, cfg)
		Expect(step(cfg)).To(Equal(want))
	},
		Entry("making no provider call", func(cfg ExecLoopConfig) any {
			return len(cfg.Streamer.(*execLoopStreamer).reqs)
		}, 0),
		Entry("fencing a retry with ErrExecLoopFenced", func(cfg ExecLoopConfig) any {
			cfg.Streamer = &execLoopStreamer{turns: [][]provider.StreamEvent{{provText("still fenced")}}}
			return runExec(context.Background(), cfg).ErrFenced
		}, true))
	It("resumes a stopped session by replaying correlated history into the next provider request", func() {
		cfg := replayLoopFixture(ctx)
		resume := &execLoopStreamer{turns: [][]provider.StreamEvent{{provText("resumed")}}}
		cfg.Streamer, cfg.MaxTurns = resume, 4
		resumed := runExec(ctx, cfg)
		Expect(execLoopFeedback{Result: resumed.Result, NoErr: resumed.NoErr, Shape: requestSummary(resume.lastRequest())}).To(Equal(execLoopFeedback{Result: &ExecLoopResult{Turns: 1, FinalContent: "resumed"}, NoErr: true, Shape: requestShape{AssistantCalls: []string{"call-6", "call-7"}, ToolResults: []string{"call-6", "call-7"}}}))
	})
})

// provText, provCall, provFinish and provError yield scripted stream events.
func provText(s string) provider.StreamEvent {
	return provider.StreamEvent{Kind: provider.StreamEventText, Content: s}
}
func provCall(c provider.ToolCall) provider.StreamEvent {
	return provider.StreamEvent{Kind: provider.StreamEventToolCall, ToolCall: &c}
}
func provFinish() provider.StreamEvent {
	return provider.StreamEvent{Kind: provider.StreamEventFinish, FinishReason: "stop"}
}
func provError(msg string) provider.StreamEvent {
	return provider.StreamEvent{Kind: provider.StreamEventError, Err: errors.New(msg)}
}
