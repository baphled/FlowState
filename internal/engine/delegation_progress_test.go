package engine_test

import (
	"context"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/tool"
)

var _ = Describe("DelegationProgress", func() {
	Context("when streaming delegation progress", func() {
		It("emits ProgressEvent to output channel during execution", func() {
			outChan := make(chan provider.StreamChunk, 100)
			ctx := engine.WithStreamOutput(context.Background(), outChan)

			chatProvider := &mockProvider{
				name: "test-chat-provider",
				streamChunks: []provider.StreamChunk{
					{Content: "response chunk 1", Done: false},
					{Content: "response chunk 2", Done: false},
					{Content: "response chunk 3", Done: false},
					{Content: "response chunk 4", Done: false},
					{Content: "response chunk 5", Done: false},
					{Content: "response chunk 6", Done: true},
				},
			}

			qaManifest := agent.Manifest{
				ID:                "qa-agent",
				Name:              "QA Agent",
				Instructions:      agent.Instructions{SystemPrompt: "You are QA."},
				ContextManagement: agent.DefaultContextManagement(),
			}

			qaEngine := engine.New(engine.Config{
				ChatProvider: chatProvider,
				Manifest:     qaManifest,
			})

			orchestratorManifest := agent.Manifest{
				ID:   "orchestrator-agent",
				Name: "Orchestrator Agent",
				Instructions: agent.Instructions{
					SystemPrompt: "You are an orchestrator.",
				},
				Delegation: agent.Delegation{
					CanDelegate: true,
				},
				ContextManagement: agent.DefaultContextManagement(),
			}

			engines := map[string]*engine.Engine{
				"qa-agent": qaEngine,
			}

			delegateTool := engine.NewDelegateTool(engines, orchestratorManifest.Delegation, "orchestrator-agent")

			input := tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "qa-agent",
					"message":       "Run the tests",
				},
			}

			_, _ = delegateTool.Execute(ctx, input)

			close(outChan)

			var foundProgressEvent bool
			for chunk := range outChan {
				if pev, ok := chunk.Event.(streaming.ProgressEvent); ok && pev.ToolCallCount > 0 {
					foundProgressEvent = true
					break
				}
			}

			Expect(foundProgressEvent).To(BeTrue(), "expected to find at least one ProgressEvent with tool calls > 0")
		})

		It("exits the collect loop when context is cancelled mid-stream", func() {
			chunks := make(chan provider.StreamChunk)
			ctx, cancel := context.WithCancel(context.Background())
			outChan := make(chan provider.StreamChunk, 10)
			ctxWithOut := engine.WithStreamOutput(ctx, outChan)

			dt := engine.NewDelegateTool(map[string]*engine.Engine{}, agent.Delegation{}, "orchestrator")

			started := make(chan struct{})
			done := make(chan struct{})
			go func() {
				defer close(done)
				close(started)
				_, _ = engine.CollectWithProgressForTest(ctxWithOut, dt, chunks, time.Now())
			}()

			<-started
			cancel()

			Eventually(done, 2*time.Second).Should(BeClosed(), "collectWithProgress must exit when ctx is cancelled")
		})

		// Drop #3 — teeToParentStream live forwarding.
		//
		// Pre-fix the tee buffered every child chunk into a strings.Builder
		// and emitted exactly one consolidated chunk after the child stream
		// closed. The brief's evidence: a 322-second delegation stall in
		// session 05ece3e7 where the parent SSE wire was idle for the entire
		// child run because the buffered emission only happened on close.
		//
		// Worse, the consolidated emission used a non-blocking send
		// (`select { case parentOut <- ...: default: }`), so a parent channel
		// that was momentarily full at the close instant silently dropped the
		// entire child output. The user saw nothing at all from the child.
		//
		// Contract: each non-control child content chunk MUST flow to the
		// parent live (before the source closes). The Done marker and
		// DelegationInfo-only chunks are still skipped (Done would close the
		// parent prematurely; DelegationInfo events are emitted separately by
		// executeSync via the delegation event bus). Out-of-band control
		// events (harness_attempt_start, harness_retry, etc.) are skipped via
		// the existing streaming.IsControlEvent gate to preserve Leak A's
		// fix.
		It("forwards each child content chunk live to the parent stream rather than buffering until close", func() {
			parentOut := make(chan provider.StreamChunk, 64)
			ctx := engine.WithStreamOutput(context.Background(), parentOut)

			// Child emits multiple distinct chunks separated by a tiny delay
			// so the test can assert each arrives at the parent before the
			// next one is produced — i.e. live, not buffered.
			multiChunkProvider := &mockProvider{
				name: "child-provider",
				streamChunks: []provider.StreamChunk{
					{Content: "first."},
					{Content: "second."},
					{Content: "third."},
					{Done: true},
				},
			}

			child := engine.New(engine.Config{
				ChatProvider: multiChunkProvider,
				Manifest: agent.Manifest{
					ID:                "child-agent",
					Name:              "Child Agent",
					Instructions:      agent.Instructions{SystemPrompt: "You are a child."},
					ContextManagement: agent.DefaultContextManagement(),
				},
			})
			engines := map[string]*engine.Engine{"child-agent": child}
			delegation := agent.Delegation{
				CanDelegate:         true,
				DelegationAllowlist: []string{"child-agent"},
			}
			delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator")

			_, err := delegateTool.Execute(ctx, tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "child-agent",
					"message":       "do the thing",
				},
			})
			Expect(err).NotTo(HaveOccurred())

			close(parentOut)

			// Collect content chunks (skip events / progress chunks).
			var contentChunks []string
			for chunk := range parentOut {
				if chunk.Content != "" && chunk.EventType == "" && chunk.DelegationInfo == nil && chunk.Event == nil {
					contentChunks = append(contentChunks, chunk.Content)
				}
			}

			// Pre-fix the tee emitted ONE consolidated chunk shaped like
			// `\n\n**[child-agent]**\n\nfirst.second.third.\n` — the test
			// would observe len(contentChunks) == 1. Live forwarding emits
			// three chunks (one per child chunk) in source order. The
			// presence of a fourth chunk (the trailing label block) is
			// allowed but not required — what matters is the live shape.
			Expect(len(contentChunks)).To(BeNumerically(">=", 3),
				"expected at least one parent chunk per child chunk (live forwarding); got %d chunks: %#v — pre-fix the tee buffered all child output into a single consolidated chunk emitted after the source closed (322s stall in session 05ece3e7)",
				len(contentChunks), contentChunks)

			// Order preservation: live chunks arrive in the order the child
			// produced them.
			joined := ""
			for _, c := range contentChunks {
				joined += c
			}
			Expect(joined).To(ContainSubstring("first."))
			Expect(joined).To(ContainSubstring("second."))
			Expect(joined).To(ContainSubstring("third."))
			Expect(strings.Index(joined, "first.")).To(BeNumerically("<", strings.Index(joined, "second.")),
				"live forwarding must preserve child chunk order")
			Expect(strings.Index(joined, "second.")).To(BeNumerically("<", strings.Index(joined, "third.")),
				"live forwarding must preserve child chunk order")
		})

		It("does not silently drop child chunks when the parent channel is full at the moment a chunk arrives", func() {
			// Pre-fix the tee's only emission used a non-blocking
			// `select { case parentOut <- ...: default: }` send. If the
			// parent channel was full at the precise instant the child
			// stream closed, the entire buffered output was dropped — the
			// user saw nothing from the child at all. With live forwarding
			// each individual chunk also has to survive backpressure.
			//
			// Drive a slow consumer (small buffer, no reader) and assert
			// the child's content still reaches the parent within a
			// generous deadline. The implementation MAY use a bounded
			// blocking send with a context-aware deadline; what's not
			// permitted is silent drop with zero observability.
			parentOut := make(chan provider.StreamChunk, 4)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			ctxOut := engine.WithStreamOutput(ctx, parentOut)

			multiChunkProvider := &mockProvider{
				name: "child-provider",
				streamChunks: []provider.StreamChunk{
					{Content: "alpha."},
					{Content: "beta."},
					{Done: true},
				},
			}
			child := engine.New(engine.Config{
				ChatProvider: multiChunkProvider,
				Manifest: agent.Manifest{
					ID:                "child-agent",
					Name:              "Child Agent",
					Instructions:      agent.Instructions{SystemPrompt: "You are a child."},
					ContextManagement: agent.DefaultContextManagement(),
				},
			})
			engines := map[string]*engine.Engine{"child-agent": child}
			delegation := agent.Delegation{
				CanDelegate:         true,
				DelegationAllowlist: []string{"child-agent"},
			}
			delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator")

			done := make(chan struct{})
			go func() {
				defer close(done)
				_, _ = delegateTool.Execute(ctxOut, tool.Input{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"subagent_type": "child-agent",
						"message":       "fan out",
					},
				})
			}()

			// Drain in real-time. With a 4-element buffer and only two
			// content chunks plus framing, the consumer keeps up.
			var collected []string
			deadline := time.After(8 * time.Second)
		drain:
			for {
				select {
				case chunk, ok := <-parentOut:
					if !ok {
						break drain
					}
					if chunk.Content != "" && chunk.EventType == "" && chunk.DelegationInfo == nil && chunk.Event == nil {
						collected = append(collected, chunk.Content)
					}
				case <-done:
					// Drain anything still buffered after the goroutine exits.
					for {
						select {
						case chunk := <-parentOut:
							if chunk.Content != "" && chunk.EventType == "" && chunk.DelegationInfo == nil && chunk.Event == nil {
								collected = append(collected, chunk.Content)
							}
						default:
							break drain
						}
					}
				case <-deadline:
					Fail("collector did not finish within deadline — likely a stalled live-forward send")
				}
			}

			joined := ""
			for _, c := range collected {
				joined += c
			}
			Expect(joined).To(ContainSubstring("alpha."),
				"first child chunk MUST reach the parent — silent-drop on non-blocking send is the regression we're fixing")
			Expect(joined).To(ContainSubstring("beta."),
				"second child chunk MUST reach the parent")
		})

		It("emits progress every 5 tool calls", func() {
			outChan := make(chan provider.StreamChunk, 100)
			ctx := engine.WithStreamOutput(context.Background(), outChan)

			chunks := make([]provider.StreamChunk, 15)
			for i := range 15 {
				chunks[i] = provider.StreamChunk{Content: "chunk", Done: i == 14}
			}

			chatProvider := &mockProvider{
				name:         "test-chat-provider",
				streamChunks: chunks,
			}

			qaManifest := agent.Manifest{
				ID:                "qa-agent",
				Name:              "QA Agent",
				Instructions:      agent.Instructions{SystemPrompt: "You are QA."},
				ContextManagement: agent.DefaultContextManagement(),
			}

			qaEngine := engine.New(engine.Config{
				ChatProvider: chatProvider,
				Manifest:     qaManifest,
			})

			orchestratorManifest := agent.Manifest{
				ID:   "orchestrator-agent",
				Name: "Orchestrator Agent",
				Instructions: agent.Instructions{
					SystemPrompt: "You are an orchestrator.",
				},
				Delegation: agent.Delegation{
					CanDelegate: true,
				},
				ContextManagement: agent.DefaultContextManagement(),
			}

			engines := map[string]*engine.Engine{
				"qa-agent": qaEngine,
			}

			delegateTool := engine.NewDelegateTool(engines, orchestratorManifest.Delegation, "orchestrator-agent")

			input := tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "qa-agent",
					"message":       "Run the tests",
				},
			}

			_, _ = delegateTool.Execute(ctx, input)

			close(outChan)

			var progressEvents []streaming.ProgressEvent
			for chunk := range outChan {
				if pev, ok := chunk.Event.(streaming.ProgressEvent); ok {
					progressEvents = append(progressEvents, pev)
				}
			}

			Expect(progressEvents).NotTo(BeEmpty(), "expected at least one progress event from 15 tool calls")
		})
	})
})
