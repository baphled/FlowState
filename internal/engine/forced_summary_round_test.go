package engine_test

import (
	"context"
	"fmt"
	"strings"
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

	It("fires a forced summary when the iteration backstop trips and no other grace path applies", func() {
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
		script = append(script, scriptedBatch{content: "Final summary of all files read."})

		prov := &capturingScriptedProvider{name: "forced-summary-fires", script: script}

		eng := engine.New(engine.Config{
			ChatProvider: prov,
			Manifest:     manifest,
			Tools:        []tool.Tool{},
		})
		eng.SetMaxToolLoopIterationsForTest(5)
		eng.SetMaxIdenticalToolCallsForTest(0)
		eng.SetMaxToolLoopDurationForTest(0)
		eng.SetMaxSameToolPatternCallsForTest(0)

		chunks, err := eng.Stream(context.Background(), "forced-summary-agent", "Read several files")
		Expect(err).NotTo(HaveOccurred())

		received, closed := drain(chunks)
		Expect(closed).To(BeTrue(),
			"the forced summary round must let the turn complete and close the channel")

		Expect(prov.callCount()).To(Equal(6),
			"5 iterations trip the backstop, then exactly one forced summary round must run")

		for _, c := range received {
			Expect(c.StopReason).NotTo(Equal(session.StopReasonToolLoopExceeded),
				"the forced summary must produce a clean completion, not tool_loop_exceeded")
		}

		var sawSummary bool
		for _, c := range received {
			if strings.Contains(c.Content, "Final summary of all files read.") {
				sawSummary = true
			}
		}
		Expect(sawSummary).To(BeTrue(),
			"the forced summary's text response must reach the stream before Done")

		Expect(prov.sawMessageContaining("tool loop budget is exhausted")).To(BeTrue(),
			"the engine must inject the forced-completion message before the forced summary round")
	})

	It("fires a forced summary when the duration backstop trips", func() {
		// Use a tiny duration (1ns) so the backstop trips on the very first
		// cap check (after batch 0 executes). The forced summary retry
		// consumes batch 1 (another tool call), that tool executes, and
		// the second cap check terminates with StopReasonToolLoopExceeded.
		script := []scriptedBatch{
			{toolCalls: []*provider.ToolCall{{ID: "read_0", Name: "read", Arguments: map[string]any{"path": "/tmp/0.txt"}}}},
			{toolCalls: []*provider.ToolCall{{ID: "read_1", Name: "read", Arguments: map[string]any{"path": "/tmp/1.txt"}}}},
		}

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
			"the duration backstop must let the forced summary complete")

		// After the forced summary retry returns a tool-call batch, the
		// tool executes and the cap check trips immediately. There's no
		// third cap to cleanse — the second trip hits the final else and
		// stamps tool_loop_exceeded.
		reason, gotTerminal := terminalStopReason(received)
		Expect(gotTerminal).To(BeTrue(), "expected a terminal Done chunk")
		Expect(reason).To(Equal(session.StopReasonToolLoopExceeded),
			"the duration backstop must stamp tool_loop_exceeded after forced summary")

		Expect(prov.callCount()).To(Equal(2),
			"1 initial call + 1 forced summary retry")

		Expect(prov.sawMessageContaining("tool loop budget is exhausted")).To(BeTrue(),
			"the engine must inject the forced-completion message when duration trips")
	})

	It("fires the forced summary at most once, then terminates with tool_loop_exceeded on a second backstop trip", func() {
		script := make([]scriptedBatch, 0, 11)
		for i := 0; i < 10; i++ {
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
			"a second backstop trip after the forced summary must terminate the channel")

		// 5 trips → forced summary (call 5, returns tools) → tool execution pushes
		// iterations to 6 → second cap trip → terminate
		// Total: 6 provider calls (5 tool, 1 forced-summary-with-tools)
		Expect(prov.callCount()).To(Equal(6),
			"5 tool calls trip the backstop, the forced summary retry consumes batch 5, then tool execution pushes iterations past cap")

		reason, gotTerminal := terminalStopReason(received)
		Expect(gotTerminal).To(BeTrue(), "expected a terminal Done chunk")
		Expect(reason).To(Equal(session.StopReasonToolLoopExceeded),
			"the second backstop trip after forced summary must terminate with tool_loop_exceeded")

		Expect(prov.sawMessageContaining("tool loop budget is exhausted")).To(BeTrue(),
			"the engine must inject the forced-completion message once")
	})
})
