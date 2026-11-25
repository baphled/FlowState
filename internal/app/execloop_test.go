package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/anthropic"
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
func execLoopFixture(streamer *execLoopStreamer) ExecLoopConfig {
	st, sess := execLoopSeed()
	DeferCleanup(func() { _ = st.Close() })
	registry, err := NewSessionToolRegistry("")
	execMust(err)
	lease, _ := acquireTestLease(st, "exec:"+sess.ID, "agent-a")
	return ExecLoopConfig{Store: st, Session: sess, Streamer: streamer, Registry: registry, Allowlist: []string{"echo"}, Lease: lease, MaxTurns: 4}
}

// acquireTestLease takes the judged exec lease for item under owner on st, as a caller of the loop does,
// and returns the handle with its release. The release is also registered with DeferCleanup, so the
// handle is held for the whole spec; the release is idempotent, so a spec may call it earlier.
func acquireTestLease(st *store.Store, item, owner string) (*store.RunLease, func()) {
	lease, err := st.AcquireRunLease(context.Background(), item, owner)
	if err != nil {
		panic(err)
	}
	release := func() { _ = lease.Release(context.Background()) }
	DeferCleanup(release)
	return lease, release
}

// heldSessionLease is the judged exec lease of sess under the owner "cli", held until the spec ends.
func heldSessionLease(st *store.Store, sess *store.Session) ExecLease {
	lease, _ := acquireTestLease(st, "exec:"+sess.ID, "cli")
	return lease
}

// stealTestLease replaces the lease held by handle with one of thief, as a second process taking over a
// dead row would: the handle fails its fence from then on.
func stealTestLease(cfg ExecLoopConfig, thief string) {
	if err := cfg.Lease.(*store.RunLease).Release(context.Background()); err != nil {
		panic(err)
	}
	acquireTestLease(cfg.Store, cfg.Lease.Item(), thief)
}
func echoCall(id string) provider.ToolCall { // an allowlisted echo invocation
	return provider.ToolCall{ID: id, Name: "echo", Arguments: json.RawMessage(`{"message":"hi"}`)}
}

type execLoopFeedback struct { // run result + next provider request
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

type execLoopOutcome struct { // one run's outcome for structural assertion
	Result                          *ExecLoopResult
	ErrConfig, ErrBudget, ErrFenced bool
	ErrProvider, NoErr              bool
	EventLog                        string
	LastStatus, SessionStatus       string
}

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

const denialMark = "not on session allowlist"

func deniedCallLoopFixture() ExecLoopConfig { // non-allowlisted fs.write call, then recovery
	rogue := provider.ToolCall{ID: "call-5", Name: "fs.write", Arguments: json.RawMessage(`{"path":"x","content":"y"}`)}
	streamer := &execLoopStreamer{turns: [][]provider.StreamEvent{{provCall(rogue), provFinish()}, {provText("recovered from denial")}}}
	cfg := execLoopFixture(streamer)
	cfg.Allowlist = []string{"echo"}
	return cfg
}

func deniedResumeLoopFixture(ctx context.Context) ExecLoopConfig { // denial run, then a resume streamer
	cfg := deniedCallLoopFixture()
	if out := runExec(ctx, cfg); !out.NoErr { // explicit setup check
		panic("denial run must finish cleanly")
	}
	cfg.Streamer = &execLoopStreamer{turns: [][]provider.StreamEvent{{provText("resumed")}}}
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
	stealTestLease(cfg, "agent-b")
	return cfg
}
func annotatedCallIDs(ctx context.Context, cfg ExecLoopConfig) []string {
	events, rerr := cfg.Store.Replay(ctx, cfg.Session.ID) // persisted tool events
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
	for _, m := range req.Messages { // req is never nil in these fixtures
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
		Expect(extract(runExec(ctx, budgetLoopFixture(ctx)))).To(Equal(want))
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
		Expect(execLoopFeedback{Result: out.Result, NoErr: out.NoErr, HistoryLen: len(second.Messages), Denied: strings.Contains(second.Messages[2].Content, denialMark), Shape: requestSummary(second)}).To(Equal(execLoopFeedback{Result: &ExecLoopResult{Turns: 2, FinalContent: "recovered from denial", ToolCalls: 1}, NoErr: true, HistoryLen: 3, Denied: true, Shape: requestShape{AssistantCalls: []string{"call-5"}, ToolResults: []string{"call-5"}}}))
	})
	It("resumes after a denial with no orphan tool-role message in the replayed history", func() {
		cfg := deniedResumeLoopFixture(ctx)
		resumed := runExec(ctx, cfg)
		last := cfg.Streamer.(*execLoopStreamer).lastRequest()
		Expect(execLoopFeedback{NoErr: resumed.NoErr, HistoryLen: len(last.Messages), Shape: requestSummary(last)}).To(Equal(execLoopFeedback{NoErr: true, HistoryLen: 1, Shape: requestShape{AssistantCalls: []string{}, ToolResults: []string{}}}))
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

var _ = Describe("failExecTurn", func() {
	var (
		st    *store.Store
		sess  *store.Session
		w     *store.EventWriter
		cause error
	)

	BeforeEach(func() {
		st, sess = execLoopSeed()
		DeferCleanup(func() { _ = st.Close() })
		var werr error
		w, werr = st.NewEventWriter(context.Background(), sess.ID)
		if werr != nil {
			panic(werr)
		}
		DeferCleanup(func() { _ = w.Close() })
		cause = errors.New("boom")
	})

	Context("when both writes succeed", func() {
		var err error

		BeforeEach(func() {
			err = failExecTurn(context.Background(), w, st, sess.ID, cause)
		})

		It("returns the provider error unchanged", func() {
			Expect(err).To(MatchError(ErrProvider.Error() + ": boom"))
		})

		It("keeps the provider's cause matchable", func() {
			Expect(err).To(MatchError(cause))
		})

		It("marks the session failed", func() {
			Expect(statusOf(st, sess.ID)).To(Equal("failed"))
		})

		It("ends the log with the provider error event", func() {
			Expect(lastType(st, sess.ID)).To(Equal("provider.error"))
		})
	})

	// The event commits before the status: were the order reversed, a
	// failed append would leave a terminal status with no final event.
	Context("when the event cannot be recorded", func() {
		var err error

		BeforeEach(func() {
			if cerr := w.Close(); cerr != nil {
				panic(cerr)
			}
			err = failExecTurn(context.Background(), w, st, sess.ID, cause)
		})

		It("still reports the provider failure", func() {
			Expect(err).To(MatchError(ErrProvider))
		})

		It("reports the append failure", func() {
			Expect(err).To(MatchError(store.ErrClosed))
		})

		It("keeps the provider's cause matchable", func() {
			Expect(err).To(MatchError(cause))
		})

		It("leaves the session non-terminal", func() {
			Expect(statusOf(st, sess.ID)).To(Equal("active"))
		})
	})

	Context("when the status cannot be updated", func() {
		var err error

		BeforeEach(func() {
			err = failExecTurn(context.Background(), w, st, "missing-session", cause)
		})

		It("still reports the provider failure", func() {
			Expect(err).To(MatchError(ErrProvider))
		})

		It("reports the status failure", func() {
			Expect(err).To(MatchError(store.ErrUnknownSession))
		})

		It("keeps the provider's cause matchable", func() {
			Expect(err).To(MatchError(cause))
		})
	})
})

var _ = Describe("Execution loop provider failure", func() {
	var (
		cause error
		err   error
	)

	BeforeEach(func() {
		cause = errors.New("upstream refused")
		failure := provider.StreamEvent{Kind: provider.StreamEventError, Err: cause}
		_, err = RunExecLoop(context.Background(), execLoopFixture(&execLoopStreamer{turns: [][]provider.StreamEvent{{failure}}}))
	})

	It("reports the provider error class", func() {
		Expect(err).To(MatchError(ErrProvider))
	})

	It("keeps the provider's own cause matchable through the loop", func() {
		Expect(err).To(MatchError(cause))
	})
})

var _ = Describe("Execution loop finish reason", func() {
	var out execLoopOutcome

	Context("when the final turn ends on a length finish", func() {
		BeforeEach(func() {
			cut := provider.StreamEvent{Kind: provider.StreamEventFinish, FinishReason: provider.FinishLength}
			out = runExec(context.Background(), execLoopFixture(&execLoopStreamer{turns: [][]provider.StreamEvent{{provText("cut"), cut}}}))
		})

		It("reports the reason on the loop result", func() {
			Expect(out.Result.FinishReason).To(Equal(provider.FinishLength))
		})

		It("records no delta for the finish event", func() {
			Expect(out.EventLog).To(Equal("turn.start,assistant.delta,assistant.message"))
		})
	})

	Context("when a tool turn precedes a stop finish", func() {
		BeforeEach(func() {
			tools := provider.StreamEvent{Kind: provider.StreamEventFinish, FinishReason: provider.FinishToolCalls}
			turns := [][]provider.StreamEvent{{provCall(echoCall("c1")), tools}, {provText("done"), provFinish()}}
			out = runExec(context.Background(), execLoopFixture(&execLoopStreamer{turns: turns}))
		})

		It("reports the reason of the final turn only", func() {
			Expect(out.Result.FinishReason).To(Equal(provider.FinishStop))
		})
	})

	// A run cut off at MaxTurns still reports the last turn's reason, which is
	// tool_calls; the run itself ends failed with ErrMaxTurns.
	Context("when the run ends at MaxTurns still asking for tools", func() {
		BeforeEach(func() {
			tools := provider.StreamEvent{Kind: provider.StreamEventFinish, FinishReason: provider.FinishToolCalls}
			turns := [][]provider.StreamEvent{{provCall(echoCall("c1")), tools}, {provCall(echoCall("c2")), tools}}
			cfg := execLoopFixture(&execLoopStreamer{turns: turns})
			cfg.MaxTurns = 2
			out = runExec(context.Background(), cfg)
		})

		It("reports tool_calls as the reason", func() {
			Expect(out.Result.FinishReason).To(Equal(provider.FinishToolCalls))
		})
	})

	Context("when the provider sends no finish event", func() {
		BeforeEach(func() {
			out = runExec(context.Background(), execLoopFixture(&execLoopStreamer{turns: [][]provider.StreamEvent{{provText("plain")}}}))
		})

		It("leaves the reason empty", func() {
			Expect(out.Result.FinishReason).To(BeEmpty())
		})
	})
})

// systemRoles lists the role of the first message of each captured request.
func systemRoles(reqs []*provider.ChatRequest) []string {
	roles := make([]string, 0, len(reqs))
	for _, r := range reqs {
		roles = append(roles, r.Messages[0].Role)
	}
	return roles
}

// replayedHistory runs cfg to completion and returns the history replay builds
// from its transcript.
func replayedHistory(ctx context.Context, cfg ExecLoopConfig) []provider.Message {
	runExec(ctx, cfg)
	history := []provider.Message{}
	execMust(replayInto(ctx, cfg.Store, cfg.Session.ID, &history))
	return history
}

var _ = Describe("Execution loop system prompt", func() {
	var ctx context.Context

	BeforeEach(func() { ctx = context.Background() })

	It("sends the prompt first as a system message", func() {
		cfg := toolTurnLoopFixture()
		cfg.SystemPrompt = "You review."
		runExec(ctx, cfg)
		Expect(cfg.Streamer.(*execLoopStreamer).reqs[0].Messages[0]).To(Equal(provider.Message{Role: "system", Content: "You review."}))
	})
	It("keeps the task right after the prompt", func() {
		cfg := toolTurnLoopFixture()
		cfg.SystemPrompt = "You review."
		runExec(ctx, cfg)
		Expect(cfg.Streamer.(*execLoopStreamer).reqs[0].Messages[1]).To(Equal(provider.Message{Role: "user", Content: "do the thing"}))
	})
	It("sends no system message when the prompt is empty", func() {
		cfg := toolTurnLoopFixture()
		runExec(ctx, cfg)
		Expect(systemRoles(cfg.Streamer.(*execLoopStreamer).reqs)).To(Equal([]string{"user", "user"}))
	})
	It("sends the prompt on every turn of a two-turn loop", func() {
		cfg := toolTurnLoopFixture()
		cfg.SystemPrompt = "You review."
		runExec(ctx, cfg)
		Expect(systemRoles(cfg.Streamer.(*execLoopStreamer).reqs)).To(Equal([]string{"system", "system"}))
	})
	It("sends the prompt once per request, not accumulated", func() {
		cfg := toolTurnLoopFixture()
		cfg.SystemPrompt = "You review."
		runExec(ctx, cfg)
		Expect(cfg.Streamer.(*execLoopStreamer).reqs[1].Messages).To(HaveLen(4))
	})
	It("does not persist the prompt as an event", func() {
		plain, prompted := toolTurnLoopFixture(), toolTurnLoopFixture()
		prompted.SystemPrompt = "You review."
		Expect(runExec(ctx, prompted).EventLog).To(Equal(runExec(ctx, plain).EventLog))
	})
	It("leaves the replayed history the same whatever the prompt", func() {
		plain, prompted := toolTurnLoopFixture(), toolTurnLoopFixture()
		prompted.SystemPrompt = "You review."
		Expect(replayedHistory(ctx, prompted)).To(Equal(replayedHistory(ctx, plain)))
	})
	It("sends the prompt on the live turn that follows a replay", func() {
		cfg := replayLoopFixture(ctx)
		resume := &execLoopStreamer{turns: [][]provider.StreamEvent{{provText("resumed")}}}
		cfg.Streamer, cfg.MaxTurns, cfg.SystemPrompt = resume, 4, "You review."
		runExec(ctx, cfg)
		Expect(resume.lastRequest().Messages[0]).To(Equal(provider.Message{Role: "system", Content: "You review."}))
	})
})

// anthropicLoopServer is a loopback fake Messages API: it answers the first
// request with a streamed tool_use and every later one with a streamed answer,
// and records each request body.
type anthropicLoopServer struct {
	mu     sync.Mutex
	bodies []string
	tool   string // the wire name of the tool the first reply calls
	input  string // the JSON string literal holding the tool input
}

func (f *anthropicLoopServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.bodies = append(f.bodies, string(b))
	n := len(f.bodies)
	f.mu.Unlock()
	if n == 1 {
		_, _ = io.WriteString(w, `data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use",`+
			`"id":"toolu_01","name":"`+f.tool+`","input":{}}}`+"\n\n"+
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":`+f.input+`}}`+"\n\n"+
			`data: {"type":"content_block_stop","index":0}`+"\n\n"+
			`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"}}`+"\n\n"+
			`data: {"type":"message_stop"}`+"\n\n")
		return
	}
	_, _ = io.WriteString(w, `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"all done"}}`+"\n\n"+
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}`+"\n\n"+
		`data: {"type":"message_stop"}`+"\n\n")
}

func (f *anthropicLoopServer) body(i int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i >= len(f.bodies) {
		return ""
	}
	return f.bodies[i]
}

var _ = Describe("Execution loop on the Anthropic API-key route", func() {
	var (
		srv *anthropicLoopServer
		out execLoopOutcome
	)

	BeforeEach(func() {
		srv = &anthropicLoopServer{tool: "echo", input: `"{\"message\":\"hi\"}"`}
		ts := httptest.NewServer(srv)
		DeferCleanup(ts.Close)
		cfg := execLoopFixture(&execLoopStreamer{})
		cfg.Streamer = anthropic.NewAPIKey("sk-ant-synthetic-test-key", ts.URL, nil)
		out = runExec(context.Background(), cfg)
	})

	It("records the turn, the tool call, the tool result and the final answer in order", func() {
		Expect(out.EventLog).To(Equal("turn.start,assistant.message,tool.call,tool.result,assistant.delta,assistant.message"))
	})

	It("counts two turns and one tool call with the final answer", func() {
		Expect(out.Result).To(Equal(&ExecLoopResult{Turns: 2, ToolCalls: 1, FinalContent: "all done", FinishReason: "stop"}))
	})

	It("sends the tool result back to the model as a tool_result block", func() {
		Expect(srv.body(1)).To(ContainSubstring(`"type":"tool_result","tool_use_id":"toolu_01"`))
	})

	It("advertises the allowlisted tool with an input_schema on the first request", func() {
		Expect(srv.body(0)).To(ContainSubstring(`"name":"echo"`))
	})
})

var _ = Describe("Execution loop with a dotted tool on the Anthropic API-key route", func() {
	var (
		srv *anthropicLoopServer
		out execLoopOutcome
	)

	BeforeEach(func() {
		dir := GinkgoT().TempDir()
		if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("dotted-secret"), 0o600); err != nil {
			panic(err)
		}
		srv = &anthropicLoopServer{tool: "fs_read", input: `"{\"path\":\"note.txt\"}"`}
		ts := httptest.NewServer(srv)
		DeferCleanup(ts.Close)
		cfg := execLoopFixture(&execLoopStreamer{})
		registry, err := NewSessionToolRegistry(dir)
		execMust(err)
		cfg.Registry, cfg.Allowlist = registry, []string{"fs.read"}
		cfg.Streamer = anthropic.NewAPIKey("sk-ant-synthetic-test-key", ts.URL, nil)
		out = runExec(context.Background(), cfg)
	})

	It("advertises the allowlisted tool as fs_read", func() {
		Expect(srv.body(0)).To(ContainSubstring(`"name":"fs_read"`))
	})

	It("runs the tool the reply named by its wire name", func() {
		Expect(srv.body(1)).To(ContainSubstring("dotted-secret"))
	})

	It("sends the call in the history as fs_read", func() {
		Expect(srv.body(1)).To(ContainSubstring(`"name":"fs_read"`))
	})

	It("finishes the turn with the final answer", func() {
		Expect(out.Result).To(Equal(&ExecLoopResult{Turns: 2, ToolCalls: 1, FinalContent: "all done", FinishReason: "stop"}))
	})
})

// toolsEveryTurn scripts n turns that each say "turn i" and ask for an echo.
func toolsEveryTurn(n int) [][]provider.StreamEvent {
	turns := make([][]provider.StreamEvent, 0, n)
	for i := 1; i <= n; i++ {
		id := fmt.Sprintf("c%d", i)
		turns = append(turns, []provider.StreamEvent{provText(fmt.Sprintf("turn %d", i)), provCall(echoCall(id)), provFinish()})
	}
	return turns
}

// eventsOf replays the seed session's events.
func eventsOf(cfg ExecLoopConfig) []store.RecordedEvent {
	events, err := cfg.Store.Replay(context.Background(), cfg.Session.ID)
	execMust(err)
	return events
}

// finalPayloadField reads one field of the last recorded event's payload.
func finalPayloadField(cfg ExecLoopConfig, field string) any {
	events := eventsOf(cfg)
	return payloadMap(events[len(events)-1].PayloadJSON)[field]
}

// cancelOnStream cancels its context when the n-th request arrives.
type cancelOnStream struct {
	execLoopStreamer
	n      int
	cancel context.CancelFunc
}

func (c *cancelOnStream) Stream(ctx context.Context, req *provider.ChatRequest) <-chan provider.StreamEvent {
	if len(c.reqs)+1 == c.n {
		c.cancel()
	}
	return c.execLoopStreamer.Stream(ctx, req)
}

var _ = Describe("The turn limit of the execution loop", func() {
	var (
		ctx context.Context
		cfg ExecLoopConfig
		res *ExecLoopResult
		err error
	)

	BeforeEach(func() { ctx = context.Background() })

	Context("when the model asks for tools on every turn and MaxTurns is 3", func() {
		BeforeEach(func() {
			cfg = execLoopFixture(&execLoopStreamer{turns: toolsEveryTurn(4)})
			cfg.MaxTurns = 3
			res, err = RunExecLoop(ctx, cfg)
		})

		It("fails with ErrMaxTurns", func() { Expect(err).To(MatchError(ErrMaxTurns)) })
		It("classifies the error as max_turns", func() { Expect(ErrorCode(err)).To(Equal("max_turns")) })
		It("marks the session failed", func() { Expect(statusOf(cfg.Store, cfg.Session.ID)).To(Equal("failed")) })
		It("ends the log with the provider.error event, before the status", func() {
			Expect(lastType(cfg.Store, cfg.Session.ID)).To(Equal(TypeProviderError))
		})
		It("records reason max_turns on the final event", func() {
			Expect(finalPayloadField(cfg, "reason")).To(Equal("max_turns"))
		})
		It("marks the final event terminal", func() { Expect(finalPayloadField(cfg, "terminal")).To(BeTrue()) })
		It("runs exactly 3 turns", func() { Expect(res.Turns).To(Equal(int64(3))) })
		It("keeps the last assistant text as the partial result", func() { Expect(res.FinalContent).To(Equal("turn 3")) })
		It("records the last turn's tool result", func() {
			Expect(annotatedCallIDs(ctx, cfg)).To(ContainElement("c3"))
		})
		It("leaves a history that replays with every call answered", func() {
			history := []provider.Message{}
			execMust(replayInto(ctx, cfg.Store, cfg.Session.ID, &history))
			Expect(history).To(HaveLen(7))
		})
		It("is terminal, so the session cannot be run or closed again", func() {
			_, lerr := session.Close(ctx, cfg.Store, cfg.Session.ID)
			Expect(lerr).To(MatchError(session.ErrTerminal))
		})
	})

	It("fails MaxTurns 1 with a tool call", func() {
		cfg = execLoopFixture(&execLoopStreamer{turns: toolsEveryTurn(1)})
		cfg.MaxTurns = 1
		_, err = RunExecLoop(ctx, cfg)
		Expect(err).To(MatchError(ErrMaxTurns))
	})

	It("completes a model that answers on its last allowed turn", func() {
		turns := append(toolsEveryTurn(2), []provider.StreamEvent{provText("answer"), provFinish()})
		cfg = execLoopFixture(&execLoopStreamer{turns: turns})
		cfg.MaxTurns = 3
		_, err = RunExecLoop(ctx, cfg)
		Expect(err).To(Succeed())
	})

	It("leaves the session unfailed for a model that answers on its last allowed turn", func() {
		turns := append(toolsEveryTurn(2), []provider.StreamEvent{provText("answer"), provFinish()})
		cfg = execLoopFixture(&execLoopStreamer{turns: turns})
		cfg.MaxTurns = 3
		_, _ = RunExecLoop(ctx, cfg)
		Expect(statusOf(cfg.Store, cfg.Session.ID)).To(Equal("active"))
	})

	It("completes a model that answers earlier", func() {
		cfg = execLoopFixture(&execLoopStreamer{turns: [][]provider.StreamEvent{{provText("quick"), provFinish()}}})
		cfg.MaxTurns = 3
		_, err = RunExecLoop(ctx, cfg)
		Expect(err).To(Succeed())
	})

	Context("when the budget pool runs out before MaxTurns", func() {
		BeforeEach(func() {
			cfg = execLoopFixture(&execLoopStreamer{turns: toolsEveryTurn(4)})
			cfg.MaxTurns, cfg.Pool, cfg.TurnBudget = 5, NewBudgetPool(2), 1
			_, err = RunExecLoop(ctx, cfg)
		})

		It("stops with the budget error, not ErrMaxTurns", func() { Expect(err).To(MatchError(ErrExecLoopBudget)) })
		It("pauses the session instead of failing it", func() {
			_ = finishExecLoopState(ctx, cfg.Store, cfg.Session, err)
			Expect(statusOf(cfg.Store, cfg.Session.ID)).To(Equal("paused"))
		})
	})

	Context("when the run is cancelled during the last turn", func() {
		BeforeEach(func() {
			cctx, cancel := context.WithCancel(ctx)
			DeferCleanup(cancel)
			cfg = execLoopFixture(&execLoopStreamer{})
			cfg.Streamer = &cancelOnStream{execLoopStreamer: execLoopStreamer{turns: toolsEveryTurn(3)}, n: 3, cancel: cancel}
			cfg.MaxTurns = 3
			_, err = RunExecLoop(cctx, cfg)
			err = finishExecLoopState(cctx, cfg.Store, cfg.Session, err)
		})

		It("reports the cancellation, not max_turns", func() { Expect(err).To(MatchError(ErrSessionCancelled)) })
		It("marks the session cancelled", func() { Expect(statusOf(cfg.Store, cfg.Session.ID)).To(Equal("cancelled")) })
	})
})

var _ = Describe("failExecMaxTurns", func() {
	It("leaves the status untouched when the final event cannot be recorded", func() {
		st, sess := execLoopSeed()
		DeferCleanup(func() { _ = st.Close() })
		w, werr := st.NewEventWriter(context.Background(), sess.ID)
		execMust(werr)
		execMust(w.Close())
		_ = failExecMaxTurns(context.Background(), w, st, sess.ID, 3)
		Expect(statusOf(st, sess.ID)).To(Equal("active"))
	})
})

// heldLeaseFake is a lease handle the caller holds: it counts Fence calls, can fail the n-th
// Fence, and reports what the log held at each Fence through observe. It has no Release, as the
// ExecLease interface has none; the real row staying in place is checked in execloop_session_test.go.
// An emptyItem handle reports an empty Item.
type heldLeaseFake struct {
	fences    int
	failOn    int
	emptyItem bool
	observe   func() int
	seen      []int
}

func (h *heldLeaseFake) Item() string {
	if h.emptyItem {
		return ""
	}
	return "exec:held-fake"
}
func (h *heldLeaseFake) Owner() string { return "run:held-fake" }
func (h *heldLeaseFake) Token() int64  { return 7 }
func (h *heldLeaseFake) Fence(context.Context) error {
	h.fences++
	if h.observe != nil {
		h.seen = append(h.seen, h.observe())
	}
	if h.fences == h.failOn {
		return store.ErrLeaseFenced
	}
	return nil
}

// heldLoopFixture is a loop config with the fake handle; the fixture's real handle stays held on the
// session's exec item, so any acquire by the loop fails.
func heldLoopFixture(streamer *execLoopStreamer, held *heldLeaseFake) ExecLoopConfig {
	cfg := execLoopFixture(streamer)
	cfg.Lease = held
	return cfg
}

var _ = Describe("Execution loop given a held lease", func() {
	var (
		held *heldLeaseFake
		cfg  ExecLoopConfig
		res  *ExecLoopResult
		err  error
	)
	BeforeEach(func() { held = &heldLeaseFake{} })

	Context("when the run succeeds", func() {
		BeforeEach(func() {
			cfg = heldLoopFixture(&execLoopStreamer{turns: [][]provider.StreamEvent{{provText("done"), provFinish()}}}, held)
			res, err = RunExecLoop(context.Background(), cfg)
		})
		It("runs without acquiring a lease of its own", func() { Expect(res.FinalContent).To(Equal("done")) })
		It("fences the handle at the start and before the request", func() { Expect(held.fences).To(Equal(2)) })
	})

	Context("when the third fence of the handle fails", func() {
		var streamer *execLoopStreamer
		BeforeEach(func() {
			held.failOn = 3
			streamer = &execLoopStreamer{turns: toolsEveryTurn(4)}
			cfg = heldLoopFixture(streamer, held)
			cfg.MaxTurns = 4
			res, err = RunExecLoop(context.Background(), cfg)
		})
		It("ends the run fenced", func() { Expect(err).To(MatchError(ErrExecLoopFenced)) })
		It("makes no provider request after the failed fence", func() { Expect(streamer.reqs).To(HaveLen(1)) })
	})

	Context("when the first fence of the handle fails", func() {
		BeforeEach(func() {
			held.failOn = 1
			cfg = heldLoopFixture(&execLoopStreamer{}, held)
			_, err = RunExecLoop(context.Background(), cfg)
		})
		It("ends the run fenced, not with the raw fence error", func() { Expect(err).To(MatchError(ErrExecLoopFenced)) })
	})

	Context("when the fence before the compaction append fails", func() {
		BeforeEach(func() {
			held.failOn = 5
			cfg, _ = autoConfig(usedTurn("c1", 1, 100), usedTurn("c2", 1, 100), overflowTurn(), summaryTurn("the summary"), doneTurn())
			cfg.Lease = held
			_, err = RunExecLoop(context.Background(), cfg)
		})
		It("ends the run fenced", func() { Expect(err).To(MatchError(ErrExecLoopFenced)) })
		It("appends no compaction", func() { Expect(countOf(cfg, TypeContextCompacted)).To(BeZero()) })
	})

	Context("when the handle has an empty item", func() {
		It("is a misconfiguration, refused before the loop starts", func() {
			held.emptyItem = true
			cfg = heldLoopFixture(&execLoopStreamer{}, held)
			Expect(runExec(context.Background(), cfg).ErrConfig).To(BeTrue())
		})
	})

	Context("when a request overflows the window and the retry succeeds", func() {
		BeforeEach(func() {
			cfg, _ = autoConfig(usedTurn("c1", 1, 100), usedTurn("c2", 1, 100), overflowTurn(), summaryTurn("the summary"), doneTurn())
			cfg.Lease = held
			held.observe = func() int { return countOf(cfg, TypeContextCompacted) }
			_, err = RunExecLoop(context.Background(), cfg)
		})
		It("fences the handle at the start, each request, before the compaction append and before the retry", func() {
			Expect(held.seen).To(Equal([]int{0, 0, 0, 0, 0, 1}))
		})
		It("completes the compaction under the handle", func() { Expect(countOf(cfg, TypeContextCompacted)).To(Equal(1)) })
	})

	Context("when it is given no handle", func() {
		BeforeEach(func() {
			cfg = execLoopFixture(&execLoopStreamer{turns: [][]provider.StreamEvent{{provText("never")}}})
			_ = cfg.Lease.(*store.RunLease).Release(context.Background())
			cfg.Lease = nil
			_, err = RunExecLoop(context.Background(), cfg)
		})
		It("is a misconfiguration", func() { Expect(err).To(MatchError(ErrExecLoopConfig)) })
		It("takes no lease", func() {
			_, found, rerr := cfg.Store.ReadLease(context.Background(), "exec:"+cfg.Session.ID)
			if rerr != nil {
				panic(rerr)
			}
			Expect(found).To(BeFalse())
		})
		It("makes no provider request", func() { Expect(cfg.Streamer.(*execLoopStreamer).reqs).To(BeEmpty()) })
		It("appends no event beyond the seed", func() {
			events, rerr := cfg.Store.Replay(context.Background(), cfg.Session.ID)
			if rerr != nil {
				panic(rerr)
			}
			Expect(events).To(HaveLen(1))
		})
	})
})

var _ = Describe("the max_tokens of a provider request", func() {
	requested := func(providerName, model string, explicit int64) int64 {
		streamer := &execLoopStreamer{turns: [][]provider.StreamEvent{{{Kind: provider.StreamEventFinish, FinishReason: "stop"}}}}
		cfg := execLoopFixture(streamer)
		cfg.Session.Provider, cfg.Session.ModelID, cfg.MaxTokens = providerName, model, explicit
		if out := runExec(context.Background(), cfg); !out.NoErr {
			panic("the scripted run failed")
		}
		return streamer.reqs[0].MaxTokens
	}
	It("defaults to the capped table maximum for a known model", func() {
		Expect(requested("z.ai", "glm-5.3", 0)).To(Equal(int64(32768)))
	})
	It("defaults to 8192 for an unknown model", func() {
		Expect(requested("scripted", "scripted-model", 0)).To(Equal(int64(8192)))
	})
	It("takes the explicit value for a known model", func() {
		Expect(requested("z.ai", "glm-5.3", 1000)).To(Equal(int64(1000)))
	})
})
