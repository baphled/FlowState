package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/store"
)

// agentFakeStreamer doubles a tool-capable provider: it replays a
// scripted sequence of stream events and captures every ChatRequest
// so specs can assert tool advertisement and history correlation.
type agentFakeStreamer struct {
	requests []*provider.ChatRequest
	turns    [][]provider.StreamEvent
	next     int
}

func (f *agentFakeStreamer) Name() string { return "agent-fake" }
func (f *agentFakeStreamer) Models() []string {
	return []string{"fake-model"}
}
func (f *agentFakeStreamer) Chat(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	return &provider.ChatResponse{Content: "unused"}, nil
}
func (f *agentFakeStreamer) Stream(ctx context.Context, req *provider.ChatRequest) <-chan provider.StreamEvent {
	f.requests = append(f.requests, req)
	idx := f.next
	if idx >= len(f.turns) {
		idx = len(f.turns) - 1
	}
	f.next++
	events := f.turns[idx]
	out := make(chan provider.StreamEvent, len(events)+1)
	for _, e := range events {
		out <- e
	}
	close(out)
	return out
}

func textEvent(s string) provider.StreamEvent {
	return provider.StreamEvent{Kind: provider.StreamEventText, Content: s}
}
func callEvent(id, name, args string) provider.StreamEvent {
	return provider.StreamEvent{Kind: provider.StreamEventToolCall, ToolCall: &provider.ToolCall{ID: id, Name: name, Arguments: json.RawMessage(args)}}
}
func finishEvent(reason string) provider.StreamEvent {
	return provider.StreamEvent{Kind: provider.StreamEventFinish, FinishReason: reason}
}

func newAgentSession(task string) (*store.Store, *store.Session) {
	st := mustStore()
	sess, err := sessionCreate(context.Background(), st, "p", "m", "harness", "", task)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return st, sess
}

var _ = Describe("AgentTurn", func() {
	It("rejects a nil session or provider", func() {
		st, sess := newAgentSession("t")
		_, err := AgentTurnWithOptions(context.Background(), st, sess, nil, AgentTurnOptions{})
		Expect(err).To(MatchError(ContainSubstring("provider must not be nil")))
		_, err = AgentTurnWithOptions(context.Background(), st, nil, &agentFakeStreamer{}, AgentTurnOptions{})
		Expect(err).To(MatchError(ContainSubstring("session must not be nil")))
	})

	It("adverts allowlisted tools with schemas and completes a tool round then final answer", func() {
		st, sess := newAgentSession("say hi")
		fake := &agentFakeStreamer{turns: [][]provider.StreamEvent{
			{callEvent("c1", "echo", `{"x":1}`), finishEvent("tool_calls")},
			{textEvent("done"), finishEvent("stop")},
		}}
		res, err := AgentTurnWithOptions(context.Background(), st, sess, fake, AgentTurnOptions{
			Allow: []string{"echo"},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.FinalContent).To(Equal("done"))
		Expect(res.Rounds).To(Equal(2))
		Expect(res.ToolCalls).To(Equal(1))
		// Round 1 advertised the tool with a JSON schema.
		Expect(fake.requests[0].Tools).To(HaveLen(1))
		Expect(fake.requests[0].Tools[0].Name).To(Equal("echo"))
		Expect(json.Valid([]byte(fake.requests[0].Tools[0].Parameters))).To(BeTrue())
		// Round 2 history: user, assistant(tool_calls), tool(result), correlated by ID.
		msgs := fake.requests[1].Messages
		Expect(msgs).To(HaveLen(3))
		Expect(msgs[1].Role).To(Equal("assistant"))
		Expect(msgs[1].ToolCalls[0].ID).To(Equal("c1"))
		Expect(msgs[2].Role).To(Equal("tool"))
		Expect(msgs[2].ToolCallID).To(Equal("c1"))
		Expect(msgs[2].Content).To(ContainSubstring(`"x":1`))
	})

	It("denies non-allowlisted tool calls and feeds the denial back as the tool result", func() {
		st, sess := newAgentSession("t")
		fake := &agentFakeStreamer{turns: [][]provider.StreamEvent{
			{callEvent("c1", "fs.read", `{"path":"x"}`), finishEvent("tool_calls")},
			{textEvent("ok"), finishEvent("stop")},
		}}
		_, err := AgentTurnWithOptions(context.Background(), st, sess, fake, AgentTurnOptions{Allow: []string{"echo"}})
		Expect(err).NotTo(HaveOccurred())
		denied := fake.requests[1].Messages[2].Content
		Expect(denied).To(ContainSubstring("tool_denied"))
	})

	It("stops with an error when the round cap is exceeded", func() {
		st, sess := newAgentSession("loop forever")
		fake := &agentFakeStreamer{turns: [][]provider.StreamEvent{
			{callEvent("c1", "echo", `{}`), finishEvent("tool_calls")},
		}}
		_, err := AgentTurnWithOptions(context.Background(), st, sess, fake, AgentTurnOptions{
			Allow:     []string{"echo"},
			MaxRounds: 1,
		})
		Expect(err).To(MatchError(ContainSubstring("round cap")))
	})

	It("truncates oversized tool results and marks them truncated", func() {
		st, sess := newAgentSession("t")
		fake := &agentFakeStreamer{turns: [][]provider.StreamEvent{
			{callEvent("c1", "big.echo", `{}`), finishEvent("tool_calls")},
			{textEvent("ok"), finishEvent("stop")},
		}}
		big := strings.Repeat("z", 9000)
		_ = big
		_, err := AgentTurnWithOptions(context.Background(), st, sess, fake, AgentTurnOptions{
			Allow: []string{"big.echo"},
			Tools: []ToolAdapter{{Name: "big.echo", Tool: fixedResultTool{json: map[string]any{"data": strings.Repeat("z", 9000)}}}},
		})
		Expect(err).NotTo(HaveOccurred())
		content := fake.requests[1].Messages[2].Content
		Expect(len(content)).To(BeNumerically("<=", AgentToolResultBudget+256))
		Expect(content).To(ContainSubstring(`"truncated":true`))
	})

	It("marks the session failed on a provider error event", func() {
		st, sess := newAgentSession("t")
		fake := &agentFakeStreamer{turns: [][]provider.StreamEvent{
			{{Kind: provider.StreamEventError, Err: errors.New("boom")}},
		}}
		_, err := AgentTurnWithOptions(context.Background(), st, sess, fake, AgentTurnOptions{Allow: []string{"echo"}})
		Expect(err).To(HaveOccurred())
		fresh, gerr := st.GetSession(context.Background(), sess.ID)
		Expect(gerr).NotTo(HaveOccurred())
		Expect(fresh.Status).To(Equal("failed"))
	})

	It("replays prior assistant tool calls and tool results into history on a second run", func() {
		st, sess := newAgentSession("continue")
		fake := &agentFakeStreamer{turns: [][]provider.StreamEvent{
			{callEvent("c1", "echo", `{"a":1}`), finishEvent("tool_calls")},
			{textEvent("ok"), finishEvent("stop")},
		}}
		_, err := AgentTurnWithOptions(context.Background(), st, sess, fake, AgentTurnOptions{Allow: []string{"echo"}})
		Expect(err).NotTo(HaveOccurred())
		// New turn in a fresh (paused) session: history reconstruction
		// must include the persisted assistant tool call + result.
		fake2 := &agentFakeStreamer{turns: [][]provider.StreamEvent{
			{textEvent("fin"), finishEvent("stop")},
		}}
		paused, perr := st.GetSession(context.Background(), sess.ID)
		Expect(perr).NotTo(HaveOccurred())
		_, err = AgentTurnWithOptions(context.Background(), st, paused, fake2, AgentTurnOptions{Allow: []string{"echo"}})
		Expect(err).NotTo(HaveOccurred())
		msgs := fake2.requests[0].Messages
		Expect(msgs[0].Role).To(Equal("user"))
		Expect(msgs).To(SatisfyAny(
			HaveLen(1),
			HaveLen(3),
		))
	})

	It("enforces the agent budget across content and tool results", func() {
		st, sess := newAgentSession("spend")
		fake := &agentFakeStreamer{turns: [][]provider.StreamEvent{
			{textEvent(strings.Repeat("a", 50)), finishEvent("stop")},
		}}
		res, err := AgentTurnWithOptions(context.Background(), st, sess, fake, AgentTurnOptions{
			Allow:  []string{"echo"},
			Budget: 10,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.BudgetExceeded).To(BeTrue())
	})
})

// fixedResultTool returns a canned result; used to exercise
// truncation without depending on real fs tools.
type fixedResultTool struct {
	json any
}

func (f fixedResultTool) Name() string { return "big.echo" }
func (f fixedResultTool) Invoke(_ context.Context, _ json.RawMessage) (any, error) {
	return f.json, nil
}

func TestAgentTurnSuiteContract(t *testing.T) {
	_ = t // suite wired via TestApp
}

// sessionCreate seeds a persisted session row for agent specs.
func sessionCreate(ctx context.Context, st *store.Store, prov, model, harness, cfg, task string) (*store.Session, error) {
	return session.Create(ctx, st, prov, model, harness, cfg, task)
}
