package engine_test

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tool"
)

var _ = Describe("Engine forced-summary round", func() {
	var manifest agent.Manifest

	BeforeEach(func() {
		manifest = agent.Manifest{
			ID:   "forced-summary-agent",
			Name: "Forced Summary Agent",
			Capabilities: agent.Capabilities{
				Tools: []string{"read"},
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

	It("stamps tool_loop_exceeded when the provider repeats identical tool calls without behavioural variety", func() {
		script := make([]scriptedBatch, 0, 12)
		for i := 0; i < 11; i++ {
			script = append(script, scriptedBatch{
				toolCalls: []*provider.ToolCall{{
					ID:        fmt.Sprintf("read_%d", i),
					Name:      "read",
					Arguments: map[string]any{"path": "/tmp/same.txt"},
				}},
			})
		}
		script = append(script, scriptedBatch{content: "Final summary of all files read."})

		prov := &capturingScriptedProvider{name: "forced-summary-behavioural-identical", script: script}

		eng := engine.New(engine.Config{
			ChatProvider: prov,
			Manifest:     manifest,
			Tools:        []tool.Tool{},
		})
		eng.SetMaxToolLoopIterationsForTest(0)
		eng.SetMaxIdenticalToolCallsForTest(3)
		eng.SetMaxToolLoopDurationForTest(0)
		eng.SetMaxSameToolPatternCallsForTest(0)

		chunks, err := eng.Stream(context.Background(), "forced-summary-agent", "Read several files")
		Expect(err).NotTo(HaveOccurred())

		received, closed := drain(chunks)
		Expect(closed).To(BeTrue(),
			"the identical-call loop guard must terminate the turn and close the channel")

		reason, gotTerminal := terminalStopReason(received)
		Expect(gotTerminal).To(BeTrue(), "expected a terminal Done chunk")
		Expect(reason).To(Equal(session.StopReasonToolLoopExceeded),
			"three identical tool+args calls in a row must terminate with tool_loop_exceeded")

		Expect(prov.callCount()).To(BeNumerically("<=", 10),
			"the identical fingerprint guard must trip within a small call bound")
	})

	It("keeps the wrap-up prompt on the input side and persists the summary as assistant content", func() {
		script := make([]scriptedBatch, 0, 40)
		for i := 0; i < 39; i++ {
			script = append(script, scriptedBatch{
				toolCalls: []*provider.ToolCall{{
					ID:        fmt.Sprintf("read_%d", i),
					Name:      "read",
					Arguments: map[string]any{"path": fmt.Sprintf("/tmp/%d.txt", i)},
				}},
			})
		}
		script = append(script, scriptedBatch{content: "Final summary of all files read."})

		reader := &executableMockTool{name: "read", execResult: tool.Result{Output: "read"}}
		registry := tool.NewRegistry()
		registry.Register(reader)
		registry.SetPermission(reader.Name(), tool.Allow)

		prov := &capturingScriptedProvider{name: "forced-summary-history", script: script}

		eng := engine.New(engine.Config{
			ChatProvider: prov,
			Manifest:     manifest,
			Tools:        []tool.Tool{reader},
			ToolRegistry: registry,
		})
		eng.SetMaxToolLoopIterationsForTest(5)
		eng.SetMaxIdenticalToolCallsForTest(0)
		eng.SetMaxToolLoopDurationForTest(0)
		eng.SetMaxSameToolPatternCallsForTest(0)

		mgr := session.NewManager(eng)
		sess, err := mgr.CreateSession("forced-summary-agent")
		Expect(err).NotTo(HaveOccurred())

		ch, err := mgr.SendMessage(context.Background(), sess.ID, "Read several files")
		Expect(err).NotTo(HaveOccurred())
		for range ch {
		}

		sess, err = mgr.GetSession(sess.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(sess.Messages).NotTo(BeEmpty())

		for _, msg := range sess.Messages {
			Expect(msg.Content).NotTo(ContainSubstring("tool loop budget is exhausted"))
		}

		Expect(prov.sawMessageContaining("tool loop budget is exhausted")).To(BeTrue())

		next, err := mgr.SendMessage(context.Background(), sess.ID, "What next?")
		Expect(err).NotTo(HaveOccurred())
		for range next {
		}

		req, ok := prov.requestAt(prov.callCount() - 1)
		Expect(ok).To(BeTrue())
		for _, msg := range req.Messages {
			Expect(msg.Content).NotTo(ContainSubstring("tool loop budget is exhausted"))
		}
	})

	It("terminates a duration-capped turn after the soft continuation budget is exhausted", func() {
		script := make([]scriptedBatch, 0, 40)
		for i := 0; i < 39; i++ {
			script = append(script, scriptedBatch{
				toolCalls: []*provider.ToolCall{{
					ID:        fmt.Sprintf("read_%d", i),
					Name:      "read",
					Arguments: map[string]any{"path": fmt.Sprintf("/tmp/%d.txt", i)},
				}},
			})
		}
		script = append(script, scriptedBatch{content: "Done."})

		prov := &capturingScriptedProvider{name: "duration-forced-summary", script: script}

		eng := engine.New(engine.Config{
			ChatProvider: prov,
			Manifest:     manifest,
			Tools:        []tool.Tool{},
		})
		eng.SetMaxToolLoopIterationsForTest(0)
		eng.SetMaxIdenticalToolCallsForTest(0)
		eng.SetMaxToolLoopDurationForTest(time.Nanosecond)
		eng.SetMaxSameToolPatternCallsForTest(0)

		chunks, err := eng.Stream(context.Background(), "forced-summary-agent", "Read many files")
		Expect(err).NotTo(HaveOccurred())

		received, closed := drain(chunks)
		Expect(closed).To(BeTrue(),
			"the duration backstop must terminate the turn once continuations are exhausted")

		reason, gotTerminal := terminalStopReason(received)
		Expect(gotTerminal).To(BeTrue(), "expected a terminal Done chunk")
		Expect(reason).To(Equal(session.StopReasonToolLoopExceeded),
			"the duration backstop must stamp tool_loop_exceeded once continuations are exhausted")

		Expect(prov.callCount()).To(BeNumerically("<=", 12),
			"soft continuations plus the forced summary retry must remain within a small bound")

		Expect(prov.sawMessageContaining("tool loop budget is exhausted")).To(BeTrue(),
			"the engine must inject the forced-completion message when duration trips")
	})

	It("stamps tool_loop_exceeded once the soft continuation budget is spent on a long distinctive-call run", func() {
		script := make([]scriptedBatch, 0, 40)
		for i := 0; i < 39; i++ {
			script = append(script, scriptedBatch{
				toolCalls: []*provider.ToolCall{{
					ID:        fmt.Sprintf("read_%d", i),
					Name:      "read",
					Arguments: map[string]any{"path": fmt.Sprintf("/tmp/%d.txt", i)},
				}},
			})
		}
		script = append(script, scriptedBatch{
			toolCalls: []*provider.ToolCall{{
				ID:        "read_last",
				Name:      "read",
				Arguments: map[string]any{"path": "/tmp/last.txt"},
			}},
		})

		prov := &capturingScriptedProvider{name: "single-forced-summary", script: script}

		eng := engine.New(engine.Config{
			ChatProvider: prov,
			Manifest:     manifest,
			Tools:        []tool.Tool{},
		})
		eng.SetMaxToolLoopIterationsForTest(5)
		eng.SetMaxIdenticalToolCallsForTest(0)
		eng.SetMaxToolLoopDurationForTest(0)
		eng.SetMaxSameToolPatternCallsForTest(0)

		chunks, err := eng.Stream(context.Background(), "forced-summary-agent", "Read many files")
		Expect(err).NotTo(HaveOccurred())

		received, closed := drain(chunks)
		Expect(closed).To(BeTrue(),
			"the turn must terminate once the soft continuation budget is exhausted")

		Expect(prov.callCount()).To(BeNumerically(">", 5),
			"the iteration cap must only softly continue — the run proceeds past the first trip")

		reason, gotTerminal := terminalStopReason(received)
		Expect(gotTerminal).To(BeTrue(), "expected a terminal Done chunk")
		Expect(reason).To(Equal(session.StopReasonToolLoopExceeded),
			"exhausted soft continuations must terminate with tool_loop_exceeded")

		Expect(prov.sawMessageContaining("tool loop budget is exhausted")).To(BeTrue(),
			"the engine must inject the forced-completion message once")
	})
})
