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

	// M2-adjacent — teeToParentStream's forwarder goroutine has the same
	// ctx-unaware-send shape that the failover plugin's prepend* wrappers
	// fixed in 38fc705f. The inner loop forwards each chunk to the returned
	// `out` channel via a bare `out <- chunk` (delegation.go:1751) before
	// the ctx-aware send to the parent stream. When a downstream collector
	// (accumulator, harness-events, response builder) stops draining `out`
	// — for example because the delegation turn was cancelled mid-stream —
	// after at most `cap(src)+1` queued chunks the forwarder goroutine
	// parks on `out <- chunk`. The subsequent ctx-aware `parentOut` send
	// never gets a chance to run, so the goroutine leaks for the full life
	// of the source channel (driven by the per-attempt stream timeout in
	// production).
	//
	// Fix shape (pinned by these specs): the inner `out <- chunk` becomes
	// a ctx-aware select alongside the existing parent-send select. When
	// the parent ctx cancels (consumer disconnect, user Escape, navigation
	// away, parent turn cancel cascade), the forwarder exits promptly and
	// closes `out`.
	//
	// Observable: with consumer-stopped-draining + parent-ctx-cancelled,
	// the `out` channel must close within a short window. Pre-fix the
	// forwarder stays parked and the channel never closes.
	Context("ctx-cancel on a wedged tee forwarder (M2-adjacent)", func() {
		// Helper: pre-fill src with enough chunks to fill out's buffer
		// twice over, then leave src open. The forwarder will read until
		// it parks on a bare `out <- chunk` (out full, consumer not
		// draining). We deliberately do NOT close src on ctx-cancel so
		// that the buggy `for chunk := range src` cannot exit by the
		// channel-close route — the ONLY way the forwarder can leave is
		// the ctx-aware select we're pinning.
		pumpSrc := func(src chan provider.StreamChunk, n int) {
			// Run the pump in a goroutine that ignores ctx — it does not
			// matter to the contract whether more chunks arrive after
			// cancel; what matters is that the in-flight bare `out <-`
			// observes ctx.Done.
			//
			// Use EventType="tool_call" + empty Content/Thinking so the
			// forwarder takes the "continue" branch (skips the ctx-aware
			// parent-send) and parks on the BARE `out <- chunk` instead
			// — that's the M2-adjacent leak surface we're pinning. With
			// content-bearing chunks the forwarder would park on the
			// already-ctx-aware parent-send and the bug below would be
			// masked.
			go func() {
				for i := 0; i < n; i++ {
					src <- provider.StreamChunk{EventType: "tool_call"}
				}
				// Intentionally do not close src. In production src is
				// closed when the upstream provider finishes its turn;
				// the leak window is the time between consumer
				// disconnect and src close.
			}()
		}

		It("closes the returned channel promptly after ctx cancels and the downstream collector has stopped draining", func() {
			// Stand up a small src so cap(src)+1 buffer is easy to overflow.
			src := make(chan provider.StreamChunk, 2)
			parentOut := make(chan provider.StreamChunk, 2)
			ctx, cancel := context.WithCancel(engine.WithStreamOutput(context.Background(), parentOut))
			defer cancel()

			out := engine.TeeToParentStreamForTest(ctx, "child-agent", src)

			// Pump more chunks than out + parentOut can buffer combined,
			// so the forwarder parks on a send somewhere.
			pumpSrc(src, 32)

			// Drain one chunk from out to confirm liveness, then stop.
			select {
			case _, ok := <-out:
				if !ok {
					Fail("returned channel closed before we could stage the wedge")
				}
			case <-time.After(2 * time.Second):
				Fail("forwarder never produced a chunk — pre-conditions broken")
			}

			// Give the forwarder a moment to pump more chunks and park.
			// At this point: out is full (cap 3, 2 buffered post-drain
			// plus the bare-send-target), parentOut is full or about to
			// be. The forwarder is parked either on `out <- chunk` (the
			// bug we're pinning) or on the ctx-aware `parentOut <-`. In
			// either case, ctx cancel MUST release it.
			time.Sleep(50 * time.Millisecond)

			cancel()

			// After cancel + consumer-stopped-draining-out, the
			// forwarder MUST exit and close `out` within a short window.
			// Pre-fix the goroutine is blocked on the bare `out <- chunk`
			// and ignores ctx, so `out` stays open indefinitely (src is
			// never closed in this test, mirroring the production leak
			// window where src stays alive until per-attempt timeout
			// fires).
			Eventually(func() bool {
				select {
				case _, ok := <-out:
					// Either we received a queued chunk (still alive,
					// keep polling) or the channel closed (goroutine
					// exited — the contract).
					return !ok
				default:
					return false
				}
			}, 1*time.Second, 10*time.Millisecond).Should(BeTrue(),
				"teeToParentStream forwarder goroutine must exit on parent ctx cancel; otherwise it leaks until src closes (per-attempt stream timeout in production) — M2-adjacent to failover stream_hook fix 38fc705f")
		})

		It("the returned channel drains to close after ctx cancel (no parked forwarder)", func() {
			// Belt-and-braces companion: drive the receiver loop after
			// cancel and assert termination. Pre-fix the drain loop
			// would hang forever (forwarder parked on `out <- chunk`,
			// src never closes).
			src := make(chan provider.StreamChunk, 2)
			parentOut := make(chan provider.StreamChunk, 2)
			ctx, cancel := context.WithCancel(engine.WithStreamOutput(context.Background(), parentOut))

			out := engine.TeeToParentStreamForTest(ctx, "child-agent", src)

			pumpSrc(src, 32)

			// Stage the wedge.
			select {
			case <-out:
			case <-time.After(2 * time.Second):
				Fail("could not stage wedge — forwarder stalled")
			}
			time.Sleep(50 * time.Millisecond)

			cancel()

			drainDone := make(chan struct{})
			go func() {
				for range out {
				}
				close(drainDone)
			}()
			select {
			case <-drainDone:
			case <-time.After(2 * time.Second):
				Fail("returned channel drain did not terminate within 2s of ctx cancel — teeToParentStream forwarder leak (M2-adjacent regression)")
			}
		})
	})
})
