package app

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
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
	events := []provider.StreamEvent{}
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
func echoCall(id string) provider.ToolCall { // allowlisted echo invocation
	return provider.ToolCall{ID: id, Name: "echo", Arguments: json.RawMessage(`{"message":"hi"}`)}
}

// streamerStub cancels its context as the wrapped stream closes (provider cut mid-turn).
type streamerStub struct {
	inner  func(context.Context, *provider.ChatRequest) <-chan provider.StreamEvent
	cancel context.CancelFunc
}

func (s streamerStub) Name() string     { return "stub" }
func (s streamerStub) Models() []string { return []string{"stub-model"} }
func (s streamerStub) Chat(context.Context, *provider.ChatRequest) (*provider.ChatResponse, error) {
	return &provider.ChatResponse{}, nil
}
func (s streamerStub) Stream(ctx context.Context, req *provider.ChatRequest) <-chan provider.StreamEvent {
	ch := make(chan provider.StreamEvent)
	go func() {
		defer func() { s.cancel(); close(ch) }()
		for ev := range s.inner(ctx, req) {
			ch <- ev
		}
	}()
	return ch
}

type execLoopFeedback struct { // run result plus next-request shape
	Result     *ExecLoopResult
	NoErr      bool
	HistoryLen int
	Denied     bool
	Shape      requestShape
}

func execMust(err error) {
	if err != nil {
		panic(err)
	}
} // panics on arrange failures
func execLoopSeed() (*store.Store, *store.Session) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(GinkgoT().TempDir(), "ws.db"))
	execMust(err)
	sess, cerr := st.CreateSession(ctx, "exec-loop-1", "scripted", "scripted-model", "harness", "sha")
	execMust(cerr)
	w, werr := st.NewEventWriter(ctx, sess.ID)
	execMust(werr)
	_, aerr := w.Append(ctx, session.ActorUser, session.TypeTurnStart, map[string]any{"task": "do the thing"})
	execMust(aerr)
	execMust(w.Close())
	return st, sess
}

type execLoopOutcome struct { // one run's outcome
	Result                              *ExecLoopResult
	ErrConfig, ErrBudget, ErrFenced     bool
	ErrProvider, NoErr                  bool
	EventLog, LastStatus, SessionStatus string
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
func replayLoopFixture(ctx context.Context) ExecLoopConfig { // stops after MaxTurns for a later resume
	call := []provider.StreamEvent{provText("need a tool"), provCall(echoCall("call-6")), provFinish()}
	first := &execLoopStreamer{turns: [][]provider.StreamEvent{call, {provText("need another"), provCall(echoCall("call-7")), provFinish()}}}
	cfg := execLoopFixture(first)
	cfg.MaxTurns = 2
	if out := runExec(ctx, cfg); out.Result.Turns != 2 {
		panic("replay fixture: expected the first run to stop after two turns")
	}
	return cfg
}

const denialMark = "not on the session allowlist"

func deniedResumeLoopFixture(ctx context.Context) ExecLoopConfig { // denial run then a resume streamer
	cfg := deniedCallLoopFixture()
	if out := runExec(ctx, cfg); !out.NoErr {
		panic("denial run must finish cleanly")
	}
	cfg.Streamer = &execLoopStreamer{turns: [][]provider.StreamEvent{{provText("resumed after denial")}}}
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

func annotatedCallIDs(ctx context.Context, cfg ExecLoopConfig) []string { // persisted tool-call ids
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

type requestShape struct{ AssistantCalls, ToolResults []string } // provider-request summary

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
		cfg := deniedResumeLoopFixture(ctx)
		resumed := runExec(ctx, cfg)
		last := cfg.Streamer.(*execLoopStreamer).lastRequest()
		Expect(execLoopFeedback{Result: resumed.Result, NoErr: resumed.NoErr, HistoryLen: len(last.Messages), Denied: strings.Contains(last.Messages[2].Content, denialMark), Shape: requestSummary(last)}).To(Equal(execLoopFeedback{Result: &ExecLoopResult{Turns: 1, FinalContent: "resumed after denial"}, NoErr: true, HistoryLen: 4, Denied: true, Shape: requestShape{AssistantCalls: []string{"call-5"}, ToolResults: []string{"call-5"}}}))
	})
	It("fails closed with ErrExecLoopConfig on a misconfigured loop", func() {
		streamer := &execLoopStreamer{}
		cfg := execLoopFixture(streamer)
		cfg.MaxTurns = 0
		Expect(runExec(ctx, cfg).ErrConfig).To(BeTrue())
	})
	It("fencing rejects a stale writer's appends after the lease is taken over", func() {
		cfg := execLoopFixture(&execLoopStreamer{turns: [][]provider.StreamEvent{{provText("turn one")}}})
		mustFinish(ctx, cfg)
		w, werr := cfg.Store.NewEventWriter(ctx, cfg.Session.ID)
		execMust(werr)
		tok, terr := cfg.Store.AcquireLease(ctx, cfg.LeaseItem, cfg.LeaseAgent)
		execMust(terr)
		execMust(cfg.Store.ReleaseLease(ctx, cfg.LeaseItem, cfg.LeaseAgent, tok))
		_, terr = cfg.Store.AcquireLease(ctx, cfg.LeaseItem, "agent-b") // fences tok
		execMust(terr)
		aerr := cfg.Store.FenceLease(ctx, cfg.LeaseItem, cfg.LeaseAgent, tok)
		Expect(aerr).To(MatchError(store.ErrLeaseFenced))
		execMust(w.Close())
	})
	It("replay preserves persisted assistant message content on resume", func() {
		cfg := execLoopFixture(&execLoopStreamer{turns: [][]provider.StreamEvent{{provText("remember this line")}}})
		mustFinish(ctx, cfg)
		resume := &execLoopStreamer{turns: [][]provider.StreamEvent{{provText("resumed")}}}
		cfg.Streamer = resume
		content := replayResumeContent(ctx, cfg, resume)
		Expect(content).To(Equal("remember this line"))
	})
	It("a cancelled stream returns ctx.Err()", func() {
		cctx, cfg, cancel := cancelledLoopFixture(ctx)
		_, rerr := RunExecLoop(cctx, cfg)
		cancel()
		Expect(errors.Is(rerr, context.Canceled)).To(BeTrue())
	})
	It("a cancelled stream persists no partial assistant message", func() {
		cctx, cfg, cancel := cancelledLoopFixture(ctx)
		RunExecLoop(cctx, cfg)
		cancel()
		Expect(replayHasAssistantMessage(cfg)).To(BeFalse())
	})
	It("a fatal unknown-tool invocation terminates the turn instead of becoming tool feedback", func() {
		ghost := provider.ToolCall{ID: "call-8", Name: "no.such.tool", Arguments: json.RawMessage(`{}`)}
		cfg := execLoopFixture(&execLoopStreamer{turns: [][]provider.StreamEvent{{provCall(ghost), provFinish()}, {provText("never")}}})
		out := runExec(ctx, cfg)
		Expect(out.ErrConfig && out.Result.Turns == 1).To(BeTrue())
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

func mustFinish(ctx context.Context, cfg ExecLoopConfig) { // panics unless the fixture run completes
	if out := runExec(ctx, cfg); !out.NoErr {
		panic("fixture first run must finish cleanly")
	}
}

// cancelledLoopFixture wires a stream cut mid-turn via streamerStub.
func cancelledLoopFixture(ctx context.Context) (context.Context, ExecLoopConfig, context.CancelFunc) {
	streamer := &execLoopStreamer{turns: [][]provider.StreamEvent{{provText("half a thought")}}}
	cfg := execLoopFixture(streamer)
	cctx, cancel := context.WithCancel(ctx)
	cfg.Streamer = streamerStub{inner: streamer.Stream, cancel: cancel}
	return cctx, cfg, cancel
}

// replayResumeContent runs the resume turn and returns the replayed content.
func replayResumeContent(ctx context.Context, cfg ExecLoopConfig, resume *execLoopStreamer) string {
	if out := runExec(ctx, cfg); !out.NoErr {
		panic("resume run must finish cleanly")
	}
	return resume.lastRequest().Messages[1].Content
}

// replayHasAssistantMessage reports whether an assistant.message survived the run.
func replayHasAssistantMessage(cfg ExecLoopConfig) bool {
	events, rerr := cfg.Store.Replay(context.Background(), cfg.Session.ID)
	execMust(rerr)
	for _, e := range events {
		if e.Type == "assistant.message" {
			return true
		}
	}
	return false
}

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
