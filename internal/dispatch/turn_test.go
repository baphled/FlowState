package dispatch_test

import (
	"context"
	"regexp"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/dispatch"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/swarm"
	"github.com/baphled/flowstate/internal/turn"
)

// uuidV4Regex matches the google/uuid library's default canonical
// form (8-4-4-4-12 hex, version nibble 4, variant nibble 8|9|a|b).
// The dispatcher mints turn ids via uuid.NewString so the spec can
// pin BOTH "non-empty" and "well-formed UUID" with one regex.
var uuidV4Regex = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// turnProbeStreamer is a dispatcher-shaped Streamer that captures
// the ctx every Stream call receives. Used by the propagation spec
// to assert turn.TurnIDFromContext on the engine-facing ctx matches
// the SessionedHandle.TurnID the dispatcher returned to the caller.
type turnProbeStreamer struct {
	mu           sync.Mutex
	capturedCtxs []context.Context
	chunks       []provider.StreamChunk
	emitInterval time.Duration
}

func (s *turnProbeStreamer) Stream(ctx context.Context, _, _ string) (<-chan provider.StreamChunk, error) {
	s.mu.Lock()
	s.capturedCtxs = append(s.capturedCtxs, ctx)
	chunksCopy := append([]provider.StreamChunk(nil), s.chunks...)
	interval := s.emitInterval
	s.mu.Unlock()

	out := make(chan provider.StreamChunk, 1)
	go func() {
		defer close(out)
		for _, c := range chunksCopy {
			select {
			case <-ctx.Done():
				return
			case <-time.After(interval):
			}
			select {
			case out <- c:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

func (s *turnProbeStreamer) lastCtx() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.capturedCtxs) == 0 {
		return nil
	}
	return s.capturedCtxs[len(s.capturedCtxs)-1]
}

// turnSessionManager is a dispatch.SessionManager that threads the
// caller's streamCtx straight into the streamer's Stream call. Used
// in the propagation spec so the streamer's ctx === the ctx the
// dispatcher injected the turn_id into.
type turnSessionManager struct {
	mu       sync.Mutex
	sess     session.Session
	streamer *turnProbeStreamer
	// streamCtxs captures every ctx threaded into
	// SendMessageWithAttachments. The propagation spec reads
	// streamCtxs[0] to assert turn.TurnIDFromContext succeeds on the
	// dispatcher-supplied ctx.
	streamCtxs []context.Context
	// holdGate is a chan that, when non-nil, blocks
	// SendMessageWithAttachments after the user message is appended
	// so the conflict spec can pin "first turn is still running"
	// without racing the drip's emitInterval.
	holdGate chan struct{}
}

func (m *turnSessionManager) SnapshotSession(_ string) (session.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := m.sess
	out.Messages = append([]session.Message(nil), m.sess.Messages...)
	return out, nil
}

func (m *turnSessionManager) SendMessageWithAttachments(
	ctx context.Context, _, message string, _ []string,
) (<-chan provider.StreamChunk, error) {
	m.mu.Lock()
	m.streamCtxs = append(m.streamCtxs, ctx)
	m.sess.Messages = append(m.sess.Messages, session.Message{
		Role:    "user",
		Content: message,
	})
	streamer := m.streamer
	gate := m.holdGate
	m.mu.Unlock()

	if gate != nil {
		<-gate
	}
	if streamer == nil {
		return nil, nil
	}
	return streamer.Stream(ctx, "fake-agent", message)
}

func (m *turnSessionManager) firstStreamCtx() context.Context {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.streamCtxs) == 0 {
		return nil
	}
	return m.streamCtxs[0]
}

// Phase 1 RED gate per "Turn-Based Post-Then-Poll Architecture
// (May 2026)". These specs pin the Dispatcher's Turn integration —
// the SessionedHandle.TurnID field, ctx-propagation of turn_id
// through the streamer ctx, and the v1 one-in-flight-turn-per-
// session contract (ErrTurnConflict).
var _ = Describe("Dispatcher.DispatchSessioned — Turn integration", func() {
	var (
		reg     *agent.Registry
		swarmer *swarm.Registry
		eng     *fakeDispatchEngine
		broker  *fakeBroker
	)

	BeforeEach(func() {
		reg = agent.NewRegistry()
		reg.Register(&agent.Manifest{ID: "default-assistant", Name: "Default Assistant"})
		swarmer = swarm.NewRegistry()
		eng = &fakeDispatchEngine{}
		broker = newFakeBroker()
	})

	Context("when DispatchSessioned starts a fresh turn", func() {
		It("returns a non-empty UUID TurnID on SessionedHandle", func() {
			probe := &turnProbeStreamer{
				chunks:       []provider.StreamChunk{{Done: true}},
				emitInterval: 1 * time.Millisecond,
			}
			mgr := &turnSessionManager{
				sess:     session.Session{ID: "sess-1", AgentID: "default-assistant"},
				streamer: probe,
			}
			turns := turn.NewRegistry()
			d := dispatch.NewWithTurns(probe, eng, swarmer, reg, mgr, broker, turns)

			handle, err := d.DispatchSessioned(context.Background(), dispatch.DispatchRequest{
				SessionID:    "sess-1",
				AgentID:      "default-assistant",
				Content:      "hello",
				ScanMentions: false,
			}, nil)
			Expect(err).NotTo(HaveOccurred())

			Expect(handle.TurnID).NotTo(BeEmpty(),
				"DispatchSessioned must mint a turn_id at POST-handler entry — Phase 1 of the Turn-Based Post-Then-Poll plan")
			Expect(uuidV4Regex.MatchString(handle.TurnID)).To(BeTrue(),
				"TurnID must match the canonical UUID v4 form so the frontend's poll URL is well-formed")

			// The registry must hold a Running turn under that id.
			t, getErr := turns.Get(handle.TurnID)
			Expect(getErr).NotTo(HaveOccurred())
			Expect(t.SessionID).To(Equal("sess-1"))
			Expect(t.Status).To(Equal(turn.StatusRunning))
		})

		It("propagates turn_id through engine chunks via context", func() {
			probe := &turnProbeStreamer{
				chunks:       []provider.StreamChunk{{Content: "ack"}, {Done: true}},
				emitInterval: 1 * time.Millisecond,
			}
			mgr := &turnSessionManager{
				sess:     session.Session{ID: "sess-1", AgentID: "default-assistant"},
				streamer: probe,
			}
			turns := turn.NewRegistry()
			d := dispatch.NewWithTurns(probe, eng, swarmer, reg, mgr, broker, turns)

			handle, err := d.DispatchSessioned(context.Background(), dispatch.DispatchRequest{
				SessionID:    "sess-1",
				AgentID:      "default-assistant",
				Content:      "probe",
				ScanMentions: false,
			}, nil)
			Expect(err).NotTo(HaveOccurred())

			// The streamCtx the dispatcher passed into the session
			// manager MUST carry turn_id == handle.TurnID. The
			// accumulator reads turn_id off this exact ctx to route
			// messages back into the registry.
			ctxIntoStreamer := mgr.firstStreamCtx()
			Expect(ctxIntoStreamer).NotTo(BeNil(),
				"the dispatcher must have called SendMessageWithAttachments — without that, ctx propagation isn't observable")

			id, ok := turn.TurnIDFromContext(ctxIntoStreamer)
			Expect(ok).To(BeTrue(),
				"the dispatcher must inject turn_id via turn.WithTurnID BEFORE handing the streamCtx to the session manager")
			Expect(id).To(Equal(handle.TurnID),
				"the turn_id in ctx must match the SessionedHandle.TurnID so the accumulator's Append routes to the correct Turn")

			// And the streamer's own captured ctx (handed down from
			// SendMessageWithAttachments) must carry the same id —
			// this is the seam the accumulator reads off in production.
			streamerCtx := probe.lastCtx()
			Expect(streamerCtx).NotTo(BeNil())
			streamerID, streamerOK := turn.TurnIDFromContext(streamerCtx)
			Expect(streamerOK).To(BeTrue())
			Expect(streamerID).To(Equal(handle.TurnID),
				"the streamer's ctx must carry turn_id — the engine pipeline downstream uses this to tag chunks")

			Eventually(broker.publishCount, "2s").Should(Equal(1))
		})
	})

	// Phase-5 §1c-α: wrapWithTurnLifecycle taps `provider_changed` and
	// `model_active` chunks as they drain and calls
	// registry.SetProviderModel(turnID, provider, model) so the long-poll
	// wire surfaces the live (provider, model) pair mid-stream. The pre-1c
	// path stamped the pair only on Complete (Turn.Model is the post-
	// terminal frozen snapshot); 1c-α adds the mid-stream surface via the
	// new CurrentProvider/CurrentModel fields.
	//
	// The tap fires per-event-type so a chunk that doesn't carry a new
	// (provider, model) pair (the common case — every content chunk
	// stamps ProviderID/ModelID but the registry only cares when the pair
	// actually moves) does NOT call SetProviderModel. The registry's own
	// gate suppresses spurious broadcasts, but the dispatcher tap follows
	// the EventType discriminant for clarity.
	Context("when the engine emits provider_changed / model_active chunks mid-stream", func() {
		It("calls SetProviderModel so the Turn registry exposes CurrentProvider/CurrentModel during Running", func() {
			// dripStreamer with a model_active chunk early in the stream
			// (matching the engine's "prepend model_active on every successful
			// stream" pattern documented in engine_test.go:2054). The wrap
			// goroutine must tap it and call SetProviderModel BEFORE the
			// terminal Done chunk's Complete fires.
			probe := &turnProbeStreamer{
				chunks: []provider.StreamChunk{
					{
						EventType:  "model_active",
						ModelID:    "claude-opus-4-7",
						ProviderID: "anthropic",
					},
					{Content: "ack", ModelID: "claude-opus-4-7", ProviderID: "anthropic"},
					{Done: true, ModelID: "claude-opus-4-7", ProviderID: "anthropic"},
				},
				emitInterval: 5 * time.Millisecond,
			}
			mgr := &turnSessionManager{
				sess:     session.Session{ID: "sess-1c", AgentID: "default-assistant"},
				streamer: probe,
			}
			turns := turn.NewRegistry()
			d := dispatch.NewWithTurns(probe, eng, swarmer, reg, mgr, broker, turns)

			handle, err := d.DispatchSessioned(context.Background(), dispatch.DispatchRequest{
				SessionID:    "sess-1c",
				AgentID:      "default-assistant",
				Content:      "trigger",
				ScanMentions: false,
			}, nil)
			Expect(err).NotTo(HaveOccurred())

			// Eventually the wrap goroutine drains all chunks and the
			// terminal Complete fires. Mid-stream, the model_active tap
			// must populate CurrentProvider + CurrentModel BEFORE the
			// terminal transition — Eventually polls the Get to catch the
			// running-state surface.
			Eventually(func() string {
				t, gerr := turns.Get(handle.TurnID)
				if gerr != nil {
					return ""
				}
				return t.CurrentProvider
			}, "2s", "10ms").Should(Equal("anthropic"),
				"the wrap goroutine must tap model_active chunks and call SetProviderModel so the long-poll surface exposes the live provider mid-stream — pre-1c the pair only landed on Complete")
			t, _ := turns.Get(handle.TurnID)
			Expect(t.CurrentModel).To(Equal("claude-opus-4-7"),
				"the model id must accompany the provider — the chip pivots on the pair, not just the provider")
		})

		It("calls SetProviderModel for provider_changed chunks (mid-stream failover surface)", func() {
			// Provider failover: the engine emits a provider_changed chunk
			// when a primary provider rate-limits / quota-trips and the
			// engine swaps to a backup. The chunk carries the NEW pair on
			// ModelID + ProviderID; the wrap tap must reflect that onto
			// the registry so the chat-UI's chip pivots before the stream
			// completes.
			probe := &turnProbeStreamer{
				chunks: []provider.StreamChunk{
					{
						EventType:  "model_active",
						ModelID:    "claude-opus-4-7",
						ProviderID: "anthropic",
					},
					{Content: "before-failover", ModelID: "claude-opus-4-7", ProviderID: "anthropic"},
					{
						EventType:  "provider_changed",
						ModelID:    "glm-4.6",
						ProviderID: "zai",
					},
					{Content: "after-failover", ModelID: "glm-4.6", ProviderID: "zai"},
					{Done: true, ModelID: "glm-4.6", ProviderID: "zai"},
				},
				emitInterval: 5 * time.Millisecond,
			}
			mgr := &turnSessionManager{
				sess:     session.Session{ID: "sess-failover", AgentID: "default-assistant"},
				streamer: probe,
			}
			turns := turn.NewRegistry()
			d := dispatch.NewWithTurns(probe, eng, swarmer, reg, mgr, broker, turns)

			handle, err := d.DispatchSessioned(context.Background(), dispatch.DispatchRequest{
				SessionID:    "sess-failover",
				AgentID:      "default-assistant",
				Content:      "trigger-failover",
				ScanMentions: false,
			}, nil)
			Expect(err).NotTo(HaveOccurred())

			// The wrap drains all chunks; final CurrentProvider/Model must
			// reflect the post-failover pair, AND the terminal Complete must
			// also fire (Model is the post-Complete snapshot).
			Eventually(func() string {
				t, gerr := turns.Get(handle.TurnID)
				if gerr != nil {
					return ""
				}
				return t.CurrentProvider
			}, "2s", "10ms").Should(Equal("zai"),
				"failover: provider_changed chunks must surface the NEW provider via SetProviderModel — CurrentProvider tracks the live pair across the failover boundary, not the original")
			t, _ := turns.Get(handle.TurnID)
			Expect(t.CurrentModel).To(Equal("glm-4.6"))
			// Terminal status — the wrap goroutine finished its drain and
			// fired Complete. Model (post-Complete snapshot) carries the
			// SAME pair the failover ended on.
			Eventually(func() turn.Status {
				out, _ := turns.Get(handle.TurnID)
				return out.Status
			}, "2s", "10ms").Should(Equal(turn.StatusCompleted))
			out, _ := turns.Get(handle.TurnID)
			Expect(out.Model.Provider).To(Equal("zai"))
			Expect(out.Model.Model).To(Equal("glm-4.6"))
		})
	})

	Context("when a second DispatchSessioned fires on the same session while the first is still running", func() {
		It("returns ErrTurnConflict from the second call", func() {
			probe := &turnProbeStreamer{
				chunks:       []provider.StreamChunk{{Content: "slow-ack"}, {Done: true}},
				emitInterval: 200 * time.Millisecond,
			}
			// Hold-gate keeps the first call's SendMessageWithAttachments
			// parked AFTER the user message is appended (so the
			// dispatcher's Start has already fired against the registry)
			// but BEFORE the chunks channel is returned. This pins turn 1
			// in StatusRunning when the second DispatchSessioned fires.
			holdGate := make(chan struct{})
			mgr := &turnSessionManager{
				sess:     session.Session{ID: "sess-1", AgentID: "default-assistant"},
				streamer: probe,
				holdGate: holdGate,
			}
			turns := turn.NewRegistry()
			d := dispatch.NewWithTurns(probe, eng, swarmer, reg, mgr, broker, turns)

			// First call: spawn in a goroutine, parked by holdGate
			// until the spec releases it post-second-call.
			firstDone := make(chan struct{})
			var firstHandle dispatch.SessionedHandle
			var firstErr error
			go func() {
				defer close(firstDone)
				firstHandle, firstErr = d.DispatchSessioned(context.Background(), dispatch.DispatchRequest{
					SessionID:    "sess-1",
					AgentID:      "default-assistant",
					Content:      "first turn",
					ScanMentions: false,
				}, nil)
			}()

			// Wait for turn 1 to register in the Turn registry —
			// the call has progressed past Start but is parked in
			// SendMessageWithAttachments waiting on holdGate. The
			// registry having a Running turn for sess-1 is the
			// preconditon the second call's Start checks.
			Eventually(func() bool {
				mgr.mu.Lock()
				defer mgr.mu.Unlock()
				return len(mgr.streamCtxs) >= 1
			}, "2s").Should(BeTrue(),
				"the first DispatchSessioned must have called Start + opened the streamer ctx before the second fires — otherwise the conflict check has nothing to observe")

			// Second call: while turn 1 is still parked, fire turn 2
			// on the same sessionID. Per the plan's v1 "one turn per
			// session" rule, this MUST return ErrTurnConflict.
			_, err := d.DispatchSessioned(context.Background(), dispatch.DispatchRequest{
				SessionID:    "sess-1",
				AgentID:      "default-assistant",
				Content:      "second turn",
				ScanMentions: false,
			}, nil)
			Expect(err).To(MatchError(dispatch.ErrTurnConflict),
				"v1 supports ONE in-flight turn per session — a concurrent POST while turn 1 is StatusRunning must surface dispatch.ErrTurnConflict so the HTTP handler can map to 409")

			// Release turn 1 so its goroutine can complete and the
			// spec exits cleanly.
			close(holdGate)
			Eventually(firstDone, "5s").Should(BeClosed())
			Expect(firstErr).NotTo(HaveOccurred())
			Expect(firstHandle.TurnID).NotTo(BeEmpty())
			Eventually(broker.publishCount, "5s").Should(Equal(1))
		})
	})
})
