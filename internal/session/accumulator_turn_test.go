package session_test

import (
	"context"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
)

// turnRegistryStub is a minimal in-place stand-in for
// internal/turn.Registry's Append method. The accumulator package
// cannot import internal/turn (the import would cycle through
// session.Message), so this stub plays the role the dispatcher's
// closure would play at the session.WithTurnRecorder seam.
type turnRegistryStub struct {
	mu       sync.Mutex
	appended map[string][]session.Message
}

func newTurnRegistryStub() *turnRegistryStub {
	return &turnRegistryStub{appended: make(map[string][]session.Message)}
}

func (s *turnRegistryStub) Append(turnID string, msg session.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appended[turnID] = append(s.appended[turnID], msg)
}

func (s *turnRegistryStub) Get(turnID string) []session.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]session.Message, len(s.appended[turnID]))
	copy(out, s.appended[turnID])
	return out
}

// Phase 1 RED gate — accumulator-to-Turn-registry seam.
// "Turn-Based Post-Then-Poll Architecture (May 2026)" plan's
// mechanism pick: the accumulator at internal/session/accumulator.go
// reads turn_id from ctx and calls a TurnMessageRecorder threaded
// through ctx at every persistence site. This spec pins that
// engine-emitted assistant + thinking + tool_call + tool_result
// messages land on the Turn registry's MessagesAdded slice for the
// turn id the dispatcher injected at POST-handler entry.
var _ = Describe("AccumulateStream Turn integration", func() {
	Context("when streamCtx carries a turn_id + TurnMessageRecorder", func() {
		It("records every engine-emitted message onto the Turn registry's MessagesAdded slice", func() {
			appender := &fakeAppender{}
			turnRegistry := newTurnRegistryStub()

			ctx := context.Background()
			ctx = session.WithAccumulatorTurnID(ctx, "turn-abc-123")
			ctx = session.WithTurnRecorder(ctx, func(id string, msg session.Message) {
				turnRegistry.Append(id, msg)
			})

			rawCh := make(chan provider.StreamChunk, 8)
			out := session.AccumulateStream(ctx, appender, "sess-1", "agent-1", rawCh)

			// Drive a representative chunk sequence: thinking → content →
			// tool_call → tool_result → Done. This mirrors the engine's
			// real wire shape for a multi-round tool turn.
			rawCh <- provider.StreamChunk{Thinking: "let me consider", Signature: "sig-1"}
			rawCh <- provider.StreamChunk{Content: "I'll check the file."}
			rawCh <- provider.StreamChunk{ToolCall: &provider.ToolCall{Name: "Read", Arguments: map[string]any{"path": "/tmp/x"}}}
			rawCh <- provider.StreamChunk{ToolResult: &provider.ToolResultInfo{Content: "file contents"}}
			rawCh <- provider.StreamChunk{Content: "Got it."}
			rawCh <- provider.StreamChunk{Done: true}
			close(rawCh)

			// Drain the output channel so the accumulator goroutine
			// completes.
			for range out {
			}

			// The Turn registry must hold the SAME messages the session
			// appender saw — assistant rows (thinking, content, final
			// assistant) plus tool_call + tool_result. The accumulator's
			// own filtering rules (which messages get appended) are not
			// re-asserted here; this spec pins that whatever the
			// accumulator persists to the session ALSO lands on the
			// turn's MessagesAdded.
			sessionMsgs := appender.messages
			turnMsgs := turnRegistry.Get("turn-abc-123")

			Expect(turnMsgs).To(HaveLen(len(sessionMsgs)),
				"every appender.AppendMessage call must fan out to the Turn registry — Phase 1 mechanism: turnAwareAppender wraps the inner appender so the two sinks stay in lock-step")

			// Role-order parity: turn registry rows must match session
			// rows position-for-position. This is the load-bearing
			// "MessagesAdded grows monotonically in arrival order"
			// contract from acceptance criterion #4.
			for i := range sessionMsgs {
				Expect(turnMsgs[i].Role).To(Equal(sessionMsgs[i].Role),
					"turn message %d role must match session", i)
				Expect(turnMsgs[i].Content).To(Equal(sessionMsgs[i].Content),
					"turn message %d content must match session", i)
			}

			// At minimum, the sequence must include thinking + tool_call
			// + tool_result + an assistant content row — the spec's
			// canonical multi-round-tool turn shape.
			roles := []string{}
			for _, m := range turnMsgs {
				roles = append(roles, m.Role)
			}
			Expect(roles).To(ContainElement("thinking"))
			Expect(roles).To(ContainElement("tool_call"))
			Expect(roles).To(ContainElement("tool_result"))
			Expect(roles).To(ContainElement("assistant"))
		})
	})

	Context("when streamCtx does NOT carry a turn_id", func() {
		It("is a no-op against the Turn registry — preserves backwards compatibility for non-sessioned callers (CLI / orchestrator)", func() {
			appender := &fakeAppender{}
			turnRegistry := newTurnRegistryStub()

			// Recorder is set but turn id is missing — the accumulator
			// MUST skip the fan-out entirely. Without the turn id the
			// recorder would have no key to write under.
			ctx := context.Background()
			ctx = session.WithTurnRecorder(ctx, func(id string, msg session.Message) {
				turnRegistry.Append(id, msg)
			})

			rawCh := make(chan provider.StreamChunk, 4)
			out := session.AccumulateStream(ctx, appender, "sess-1", "agent-1", rawCh)

			rawCh <- provider.StreamChunk{Content: "hello"}
			rawCh <- provider.StreamChunk{Done: true}
			close(rawCh)
			for range out {
			}

			Expect(appender.messages).NotTo(BeEmpty(),
				"the session appender must still persist messages — the wrap is conditional, not destructive")
			Expect(turnRegistry.Get("")).To(BeEmpty(),
				"no turn id in ctx must mean no Turn registry writes — CLI and orchestrator callers must observe zero behavioural change")
		})
	})

	Context("when streamCtx carries a turn_id but no recorder", func() {
		It("is a no-op against the Turn registry", func() {
			appender := &fakeAppender{}

			ctx := session.WithAccumulatorTurnID(context.Background(), "turn-xyz")

			rawCh := make(chan provider.StreamChunk, 4)
			out := session.AccumulateStream(ctx, appender, "sess-1", "agent-1", rawCh)
			rawCh <- provider.StreamChunk{Content: "hello"}
			rawCh <- provider.StreamChunk{Done: true}
			close(rawCh)
			for range out {
			}

			Expect(appender.messages).NotTo(BeEmpty())
			// No recorder means no fan-out — proven by the absence of a
			// stub Get call here; the spec is exercise that the
			// accumulator doesn't panic on a half-configured ctx.
		})
	})
})
