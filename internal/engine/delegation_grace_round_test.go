package engine_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/todo"
)

// capturingScriptedProvider drives an explicit list of (content, toolCalls)
// batches across successive streams and records every ChatRequest's messages
// so specs can assert whether the engine injected the forced-completion grace
// message. Models the coordinator shape captured in session 5841a542: the
// coordinator fans out delegate calls until the iteration backstop trips, then
// is granted one final round to synthesise a user-facing response.
type capturingScriptedProvider struct {
	name   string
	script []scriptedBatch

	mu       sync.Mutex
	calls    int
	requests []provider.ChatRequest
	messages []provider.Message
}

func (p *capturingScriptedProvider) Name() string { return p.name }

func (p *capturingScriptedProvider) Stream(_ context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	p.mu.Lock()
	idx := p.calls
	p.calls++
	p.requests = append(p.requests, req)
	p.messages = append(p.messages, req.Messages...)
	p.mu.Unlock()

	ch := make(chan provider.StreamChunk, 8)
	go func() {
		defer close(ch)
		if idx >= len(p.script) {
			ch <- provider.StreamChunk{Content: "All done.", Done: true}
			return
		}
		batch := p.script[idx]
		if batch.content != "" {
			ch <- provider.StreamChunk{Content: batch.content}
		}
		for _, tc := range batch.toolCalls {
			tcCopy := *tc
			ch <- provider.StreamChunk{EventType: "tool_call", ToolCall: &tcCopy}
		}
		ch <- provider.StreamChunk{Done: true}
	}()
	return ch, nil
}

func (p *capturingScriptedProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

func (p *capturingScriptedProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return []float64{0.1, 0.2, 0.3}, nil
}

func (p *capturingScriptedProvider) Models() ([]provider.Model, error) { return nil, nil }

func (p *capturingScriptedProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func (p *capturingScriptedProvider) sawMessageContaining(needle string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, m := range p.messages {
		if strings.Contains(m.Content, needle) {
			return true
		}
	}
	return false
}

func (p *capturingScriptedProvider) requestAt(index int) (provider.ChatRequest, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if index < 0 || index >= len(p.requests) {
		return provider.ChatRequest{}, false
	}
	return p.requests[index], true
}

func delegateBatch(i int) []*provider.ToolCall {
	return []*provider.ToolCall{{
		ID:        fmt.Sprintf("del_%d", i),
		Name:      "delegate",
		Arguments: map[string]any{"target": fmt.Sprintf("agent-%d", i)},
	}}
}

var _ = Describe("Engine delegation grace round", func() {
	var manifest agent.Manifest

	BeforeEach(func() {
		manifest = agent.Manifest{
			ID:   "grace-agent",
			Name: "Grace Agent",
			Capabilities: agent.Capabilities{
				Tools: []string{"delegate", "read"},
			},
		}
	})

	drain := func(chunks <-chan provider.StreamChunk) ([]provider.StreamChunk, bool) {
		var received []provider.StreamChunk
		done := make(chan struct{})
		go func() {
			defer close(done)
			for c := range chunks {
				received = append(received, c)
			}
		}()
		select {
		case <-done:
			return received, true
		case <-time.After(5 * time.Second):
			return received, false
		}
	}

	terminalStopReason := func(received []provider.StreamChunk) (string, bool) {
		for i := range received {
			if received[i].Done {
				return received[i].StopReason, true
			}
		}
		return "", false
	}

	It("grants one grace round when the iteration backstop trips on a delegate batch and the model then produces text", func() {
		script := make([]scriptedBatch, 0, 6)
		for i := 0; i < 5; i++ {
			script = append(script, scriptedBatch{toolCalls: delegateBatch(i)})
		}
		script = append(script, scriptedBatch{content: "Here is my summary of the delegation findings."})

		prov := &capturingScriptedProvider{name: "grace-fires", script: script}

		eng := engine.New(engine.Config{
			ChatProvider: prov,
			Manifest:     manifest,
			Tools:        []tool.Tool{},
		})
		eng.SetMaxToolLoopIterationsForTest(5)
		eng.SetMaxIdenticalToolCallsForTest(0)
		eng.SetMaxToolLoopDurationForTest(0)
		eng.SetMaxSameToolPatternCallsForTest(0)

		chunks, err := eng.Stream(context.Background(), "grace-agent", "Delegate then summarise")
		Expect(err).NotTo(HaveOccurred())

		received, closed := drain(chunks)
		Expect(closed).To(BeTrue(),
			"the grace round must let the turn complete and close the channel")

		Expect(prov.callCount()).To(Equal(6),
			"5 delegate batches trip the backstop, then exactly one grace round must run")

		for _, c := range received {
			Expect(c.StopReason).NotTo(Equal(session.StopReasonToolLoopExceeded),
				"the grace round must produce a clean completion, not tool_loop_exceeded")
		}

		var sawSummary bool
		for _, c := range received {
			if strings.Contains(c.Content, "summary of the delegation findings") {
				sawSummary = true
			}
		}
		Expect(sawSummary).To(BeTrue(),
			"the grace round's text response must reach the stream before Done")

		Expect(prov.sawMessageContaining("tool loop budget is exhausted")).To(BeTrue(),
			"the engine must inject the forced-completion grace message before the final round")
	})

	It("does NOT grant a grace round when the backstop trips on a non-delegation batch", func() {
		script := make([]scriptedBatch, 0, 6)
		for i := 0; i < 5; i++ {
			script = append(script, scriptedBatch{
				toolCalls: []*provider.ToolCall{{
					ID:        fmt.Sprintf("read_%d", i),
					Name:      "read",
					Arguments: map[string]any{"path": fmt.Sprintf("/tmp/%d.txt", i)},
				}},
			})
		}
		// 6th batch: forced summary retry returns a tool call, so the
		// second cap check terminates with StopReasonToolLoopExceeded.
		script = append(script, scriptedBatch{
			toolCalls: []*provider.ToolCall{{
				ID:        "read_5",
				Name:      "read",
				Arguments: map[string]any{"path": "/tmp/5.txt"},
			}},
		})

		prov := &capturingScriptedProvider{name: "no-grace-read", script: script}

		eng := engine.New(engine.Config{
			ChatProvider: prov,
			Manifest:     manifest,
			Tools:        []tool.Tool{},
		})
		eng.SetMaxToolLoopIterationsForTest(5)
		eng.SetMaxIdenticalToolCallsForTest(0)
		eng.SetMaxToolLoopDurationForTest(0)
		eng.SetMaxSameToolPatternCallsForTest(0)

		chunks, err := eng.Stream(context.Background(), "grace-agent", "Read several files")
		Expect(err).NotTo(HaveOccurred())

		received, closed := drain(chunks)
		Expect(closed).To(BeTrue(),
			"a non-delegation loop must terminate without a grace round")

		// 5 trips → forced summary (call 5 returns batch 5) → tool execution
		// pushes iterations past cap → second cap trip → terminate
		Expect(prov.callCount()).To(Equal(6),
			"no grace round, but the forced summary fires once and the retry batch triggers a second cap trip")

		reason, gotTerminal := terminalStopReason(received)
		Expect(gotTerminal).To(BeTrue(), "expected a terminal Done chunk")
		Expect(reason).To(Equal(session.StopReasonToolLoopExceeded),
			"the forced summary retry returns tools → second cap trip → tool_loop_exceeded")

		Expect(prov.sawMessageContaining("tool loop budget is exhausted")).To(BeTrue(),
			"the forced summary injects the completion message for any backstop trip")
	})

	It("grants the grace round at most once even if the model keeps calling delegate", func() {
		script := make([]scriptedBatch, 0, 11)
		for i := 0; i < 11; i++ {
			script = append(script, scriptedBatch{toolCalls: delegateBatch(i)})
		}

		prov := &capturingScriptedProvider{name: "single-grace", script: script}

		eng := engine.New(engine.Config{
			ChatProvider: prov,
			Manifest:     manifest,
			Tools:        []tool.Tool{},
		})
		eng.SetMaxToolLoopIterationsForTest(5)
		eng.SetMaxIdenticalToolCallsForTest(0)
		eng.SetMaxToolLoopDurationForTest(0)
		eng.SetMaxSameToolPatternCallsForTest(0)

		chunks, err := eng.Stream(context.Background(), "grace-agent", "Keep delegating")
		Expect(err).NotTo(HaveOccurred())

		received, closed := drain(chunks)
		Expect(closed).To(BeTrue(),
			"the second backstop trip must terminate the turn after a single grace round")

		// 5 trips → grace (call 5) → 5 more trips → second cap trip → forced
		// summary (call 10 returns batch 10) → tool execution → third cap
		// trip → terminate. No second grace round.
		Expect(prov.callCount()).To(Equal(11),
			"5 trips, delegation grace, 5 more trips, then forced summary fires and the retry tool call triggers termination")

		reason, gotTerminal := terminalStopReason(received)
		Expect(gotTerminal).To(BeTrue(), "expected a terminal Done chunk")
		Expect(reason).To(Equal(session.StopReasonToolLoopExceeded),
			"the third backstop trip must terminate with tool_loop_exceeded, not a third chance")
	})

	It("does NOT grant a grace round when the duration backstop trips even if the last call was delegate", func() {
		script := make([]scriptedBatch, 0, 300)
		for i := 0; i < 300; i++ {
			script = append(script, scriptedBatch{toolCalls: delegateBatch(i)})
		}

		prov := &capturingScriptedProvider{name: "duration-trip", script: script}

		eng := engine.New(engine.Config{
			ChatProvider: prov,
			Manifest:     manifest,
			Tools:        []tool.Tool{},
		})
		eng.SetMaxToolLoopIterationsForTest(0)
		eng.SetMaxIdenticalToolCallsForTest(0)
		eng.SetMaxToolLoopDurationForTest(time.Millisecond)
		eng.SetMaxSameToolPatternCallsForTest(0)

		chunks, err := eng.Stream(context.Background(), "grace-agent", "Long delegation loop")
		Expect(err).NotTo(HaveOccurred())

		received, closed := drain(chunks)
		Expect(closed).To(BeTrue(),
			"the duration backstop must terminate the turn")

		reason, gotTerminal := terminalStopReason(received)
		Expect(gotTerminal).To(BeTrue(), "expected a terminal Done chunk")
		Expect(reason).To(Equal(session.StopReasonToolLoopExceeded),
			"the duration backstop must stamp tool_loop_exceeded")

		Expect(prov.callCount()).To(BeNumerically("<", 300),
			"the duration backstop must trip well below the script length")

		Expect(prov.sawMessageContaining("tool loop budget is exhausted")).To(BeTrue(),
			"the delegation grace round is suppressed when the duration backstop trips, but the forced summary fires instead")
	})

	It("grants a grace round when all todos are complete even without a delegate call", func() {
		sessionID := "grace-todos-complete-session"
		ctx := context.WithValue(context.Background(), session.IDKey{}, sessionID)

		todoStore := todo.NewMemoryStore()
		todoStore.Set(sessionID, []todo.Item{
			{Content: "Task 1", Status: "completed", Priority: "medium"},
			{Content: "Task 2", Status: "completed", Priority: "low"},
		})

		script := make([]scriptedBatch, 0, 6)
		for i := 0; i < 5; i++ {
			script = append(script, scriptedBatch{
				toolCalls: []*provider.ToolCall{{
					ID:        fmt.Sprintf("read_%d", i),
					Name:      "read",
					Arguments: map[string]any{"path": fmt.Sprintf("/tmp/%d.txt", i)},
				}},
			})
		}
		script = append(script, scriptedBatch{content: "All tasks done, here is the summary."})

		prov := &capturingScriptedProvider{name: "grace-todos-complete", script: script}

		eng := engine.New(engine.Config{
			ChatProvider: prov,
			Manifest:     manifest,
			Tools:        []tool.Tool{},
		})
		eng.SetMaxToolLoopIterationsForTest(5)
		eng.SetMaxIdenticalToolCallsForTest(0)
		eng.SetMaxToolLoopDurationForTest(0)
		eng.SetMaxSameToolPatternCallsForTest(0)
		eng.SetTodoStoreForTest(todoStore)

		chunks, err := eng.Stream(ctx, "grace-agent", "Read files and summarise")
		Expect(err).NotTo(HaveOccurred())

		received, closed := drain(chunks)
		Expect(closed).To(BeTrue(),
			"the grace round must let the turn complete and close the channel")

		Expect(prov.callCount()).To(Equal(6),
			"5 read calls trip the backstop, then exactly one grace round must run")

		for _, c := range received {
			Expect(c.StopReason).NotTo(Equal(session.StopReasonToolLoopExceeded),
				"the grace round must produce a clean completion, not tool_loop_exceeded")
		}

		var sawSummary bool
		for _, c := range received {
			if strings.Contains(c.Content, "All tasks done") {
				sawSummary = true
			}
		}
		Expect(sawSummary).To(BeTrue(),
			"the grace round's text response must reach the stream before Done")

		Expect(prov.sawMessageContaining("tool loop budget is exhausted")).To(BeTrue(),
			"the engine must inject the forced-completion grace message before the final round")
	})

	It("does NOT grant a grace round when todos are still incomplete", func() {
		sessionID := "grace-incomplete-session"
		ctx := context.WithValue(context.Background(), session.IDKey{}, sessionID)

		todoStore := todo.NewMemoryStore()
		todoStore.Set(sessionID, []todo.Item{
			{Content: "Task 1", Status: "in_progress", Priority: "high"},
		})

		script := make([]scriptedBatch, 0, 5)
		for i := 0; i < 5; i++ {
			script = append(script, scriptedBatch{
				toolCalls: []*provider.ToolCall{{
					ID:        fmt.Sprintf("read_%d", i),
					Name:      "read",
					Arguments: map[string]any{"path": fmt.Sprintf("/tmp/%d.txt", i)},
				}},
			})
		}

		prov := &capturingScriptedProvider{name: "no-grace-incomplete-todos", script: script}

		eng := engine.New(engine.Config{
			ChatProvider: prov,
			Manifest:     manifest,
			Tools:        []tool.Tool{},
		})
		eng.SetMaxToolLoopIterationsForTest(5)
		eng.SetMaxIdenticalToolCallsForTest(0)
		eng.SetMaxToolLoopDurationForTest(0)
		eng.SetMaxSameToolPatternCallsForTest(0)
		eng.SetTodoStoreForTest(todoStore)

		chunks, err := eng.Stream(ctx, "grace-agent", "Read files")
		Expect(err).NotTo(HaveOccurred())

		_, closed := drain(chunks)
		Expect(closed).To(BeTrue(),
			"a non-delegation loop with incomplete todos must terminate")

		Expect(prov.callCount()).To(BeNumerically(">", 5),
			"the todo continuation must fire instead of terminating immediately")

		Expect(prov.sawMessageContaining("tool loop budget is exhausted")).To(BeFalse(),
			"the grace message must not be injected when todos are still incomplete")
	})
})
