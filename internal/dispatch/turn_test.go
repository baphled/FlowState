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

// transientError is a bare-string error used by the Phase-5 §1c-γ
// "non-critical chunk.Error" pin. provider.severityFromKeywords on
// "connection refused" yields SeverityTransient — the dispatcher's
// chunk-tap must NOT stamp CriticalError for such errors.
type transientError struct{ msg string }

func (e *transientError) Error() string { return e.msg }

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
			d := dispatch.NewWithTurns(probe, eng, swarmer, reg, mgr, turns)

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
			d := dispatch.NewWithTurns(probe, eng, swarmer, reg, mgr, turns)

			handle, err := d.DispatchSessioned(context.Background(), dispatch.DispatchRequest{
				SessionID:    "sess-1",
				AgentID:      "default-assistant",
				Content:      "probe",
				ScanMentions: false,
			}, broker)
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
			d := dispatch.NewWithTurns(probe, eng, swarmer, reg, mgr, turns)

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
			d := dispatch.NewWithTurns(probe, eng, swarmer, reg, mgr, turns)

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

	// Phase-5 §1c-β: wrapWithTurnLifecycle also taps `context_usage` and
	// `provider_quota` chunks. The dispatcher parses chunk.Content into the
	// matching typed payload and calls registry.SetContextUsage /
	// registry.UpsertProviderQuota so the long-poll wire surfaces the live
	// figure / per-partition quota state.
	//
	// Plan ref: ~/vaults/baphled/1. Projects/FlowState/Plans/
	//   Phase-5 Turn-Endpoint Event-Type Parity (May 2026).md §1c-β.
	Context("when the engine emits context_usage / provider_quota chunks mid-stream (Phase-5 §1c-β)", func() {
		It("calls SetContextUsage so the Turn registry exposes ContextUsage during Running", func() {
			probe := &turnProbeStreamer{
				chunks: []provider.StreamChunk{
					{
						EventType: "context_usage",
						Content:   `{"input_tokens":1234,"output_reserve":8192,"limit":200000,"percentage":1,"provider":"anthropic","model":"claude-opus-4-7"}`,
					},
					{Content: "ack", ModelID: "claude-opus-4-7", ProviderID: "anthropic"},
					{Done: true, ModelID: "claude-opus-4-7", ProviderID: "anthropic"},
				},
				emitInterval: 5 * time.Millisecond,
			}
			mgr := &turnSessionManager{
				sess:     session.Session{ID: "sess-1c-beta-cu", AgentID: "default-assistant"},
				streamer: probe,
			}
			turns := turn.NewRegistry()
			d := dispatch.NewWithTurns(probe, eng, swarmer, reg, mgr, turns)

			handle, err := d.DispatchSessioned(context.Background(), dispatch.DispatchRequest{
				SessionID:    "sess-1c-beta-cu",
				AgentID:      "default-assistant",
				Content:      "trigger",
				ScanMentions: false,
			}, nil)
			Expect(err).NotTo(HaveOccurred())

			// Eventually the wrap goroutine drains the context_usage chunk
			// and SetContextUsage populates Turn.ContextUsage BEFORE the
			// terminal Complete fires.
			Eventually(func() int {
				t, gerr := turns.Get(handle.TurnID)
				if gerr != nil || t.ContextUsage == nil {
					return 0
				}
				return t.ContextUsage.InputTokens
			}, "2s", "10ms").Should(Equal(1234),
				"the wrap goroutine must tap context_usage chunks, parse chunk.Content, and call SetContextUsage — the long-poll surface then exposes the live figure without an SSE side-channel")

			t, _ := turns.Get(handle.TurnID)
			Expect(t.ContextUsage.Limit).To(Equal(200000))
			Expect(t.ContextUsage.Provider).To(Equal("anthropic"))
			Expect(t.ContextUsage.Model).To(Equal("claude-opus-4-7"))
		})

		It("silently absorbs a malformed context_usage chunk payload (no panic, no mutation)", func() {
			probe := &turnProbeStreamer{
				chunks: []provider.StreamChunk{
					{EventType: "context_usage", Content: `{not valid json`},
					{Content: "ack"},
					{Done: true},
				},
				emitInterval: 5 * time.Millisecond,
			}
			mgr := &turnSessionManager{
				sess:     session.Session{ID: "sess-malformed-cu", AgentID: "default-assistant"},
				streamer: probe,
			}
			turns := turn.NewRegistry()
			d := dispatch.NewWithTurns(probe, eng, swarmer, reg, mgr, turns)

			handle, err := d.DispatchSessioned(context.Background(), dispatch.DispatchRequest{
				SessionID:    "sess-malformed-cu",
				AgentID:      "default-assistant",
				Content:      "trigger",
				ScanMentions: false,
			}, nil)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() turn.Status {
				t, _ := turns.Get(handle.TurnID)
				return t.Status
			}, "2s", "10ms").Should(Equal(turn.StatusCompleted),
				"the wrap goroutine must drain the malformed chunk without panicking and still fire terminal Complete")
			t, _ := turns.Get(handle.TurnID)
			Expect(t.ContextUsage).To(BeNil(),
				"malformed payload must NOT mutate the stored figure — parse-fail absorbs silently to mirror the SSE writer's forward-compat policy")
		})

		It("calls UpsertProviderQuota — same key REPLACES not duplicates; different key APPENDS", func() {
			// Drip two provider_quota chunks: same partition key updated
			// (replaces the prior figure), then a different key (appends a
			// new partition). The brief calls this out explicitly as the
			// regression-pin for Option B partition-key semantics.
			probe := &turnProbeStreamer{
				chunks: []provider.StreamChunk{
					{
						EventType: "provider_quota",
						Content:   `{"provider":"anthropic","account_hash":"acc-1","model":"claude-opus-4-7","observed_at":"2026-05-19T00:00:00Z","variant":"token_spend","token_spend":{"spent_minor":1000,"spent_currency":"USD","period":"monthly","period_start":"2026-05-01T00:00:00Z","period_end":"2026-05-31T23:59:59Z","threshold_amber":70,"threshold_red":90}}`,
					},
					{Content: "mid"},
					{
						EventType: "provider_quota",
						Content:   `{"provider":"anthropic","account_hash":"acc-1","model":"claude-opus-4-7","observed_at":"2026-05-19T00:00:01Z","variant":"token_spend","token_spend":{"spent_minor":2500,"spent_currency":"USD","period":"monthly","period_start":"2026-05-01T00:00:00Z","period_end":"2026-05-31T23:59:59Z","threshold_amber":70,"threshold_red":90}}`,
					},
					{
						EventType: "provider_quota",
						Content:   `{"provider":"zai","account_hash":"acc-z","model":"glm-4.6","observed_at":"2026-05-19T00:00:02Z","variant":"token_spend","token_spend":{"spent_minor":500,"spent_currency":"USD","period":"monthly","period_start":"2026-05-01T00:00:00Z","period_end":"2026-05-31T23:59:59Z","threshold_amber":70,"threshold_red":90}}`,
					},
					{Done: true},
				},
				emitInterval: 5 * time.Millisecond,
			}
			mgr := &turnSessionManager{
				sess:     session.Session{ID: "sess-1c-beta-quota", AgentID: "default-assistant"},
				streamer: probe,
			}
			turns := turn.NewRegistry()
			d := dispatch.NewWithTurns(probe, eng, swarmer, reg, mgr, turns)

			handle, err := d.DispatchSessioned(context.Background(), dispatch.DispatchRequest{
				SessionID:    "sess-1c-beta-quota",
				AgentID:      "default-assistant",
				Content:      "trigger",
				ScanMentions: false,
			}, nil)
			Expect(err).NotTo(HaveOccurred())

			// Wait until two partitions are present (the first replaced, the
			// second appended). If the dispatcher tap were APPENDing instead
			// of UPSERTing the same partition key, len would be 3.
			Eventually(func() int {
				t, gerr := turns.Get(handle.TurnID)
				if gerr != nil {
					return 0
				}
				return len(t.ProviderQuotas)
			}, "2s", "10ms").Should(Equal(2),
				"after two same-key + one different-key provider_quota chunks, ProviderQuotas must have exactly 2 entries — partition-key dedup REPLACES same-key snapshots in place")

			t, _ := turns.Get(handle.TurnID)
			// Find the anthropic entry — its TokenSpend must reflect the
			// LATEST figure (2500), not the original 1000.
			var anthSpent int64
			var zaiSpent int64
			for _, q := range t.ProviderQuotas {
				if q.Provider == "anthropic" && q.TokenSpend != nil {
					anthSpent = q.TokenSpend.SpentMinor
				}
				if q.Provider == "zai" && q.TokenSpend != nil {
					zaiSpent = q.TokenSpend.SpentMinor
				}
			}
			Expect(anthSpent).To(Equal(int64(2500)),
				"same-partition replacement must surface the newest figure — the second anthropic chunk overrides the first")
			Expect(zaiSpent).To(Equal(int64(500)),
				"different-partition append must surface the zai chunk's figure")
		})

		It("silently absorbs a malformed provider_quota chunk payload (no panic, no mutation)", func() {
			probe := &turnProbeStreamer{
				chunks: []provider.StreamChunk{
					{EventType: "provider_quota", Content: `not-json-at-all`},
					{Content: "ack"},
					{Done: true},
				},
				emitInterval: 5 * time.Millisecond,
			}
			mgr := &turnSessionManager{
				sess:     session.Session{ID: "sess-malformed-quota", AgentID: "default-assistant"},
				streamer: probe,
			}
			turns := turn.NewRegistry()
			d := dispatch.NewWithTurns(probe, eng, swarmer, reg, mgr, turns)

			handle, err := d.DispatchSessioned(context.Background(), dispatch.DispatchRequest{
				SessionID:    "sess-malformed-quota",
				AgentID:      "default-assistant",
				Content:      "trigger",
				ScanMentions: false,
			}, nil)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() turn.Status {
				t, _ := turns.Get(handle.TurnID)
				return t.Status
			}, "2s", "10ms").Should(Equal(turn.StatusCompleted))
			t, _ := turns.Get(handle.TurnID)
			Expect(t.ProviderQuotas).To(BeEmpty(),
				"malformed provider_quota payload must NOT append to the slice — parse-fail absorbs silently")
		})
	})

	// Phase-5 §1c-γ — the wrap goroutine taps chunk.Error and classifies
	// SeverityCritical via provider.IsCriticalStreamError; when critical,
	// it stamps a sanitised TurnCriticalError onto the Turn so the
	// long-poll wire surfaces the persistent banner without an SSE
	// side-channel.
	//
	// The brief asserted chunk.EventType="stream_critical" as the tap
	// signal, but the actual engine + provider layers signal critical
	// via chunk.Error classified by provider.IsCriticalStreamError;
	// there are no chunks with EventType="stream_critical" in
	// internal/. These specs pin the actual signal path.
	Context("when the engine emits chunk.Error that classifies as critical (Phase-5 §1c-γ)", func() {
		It("stamps Turn.CriticalError with sanitised message + correlation_id when chunk.Error is critical", func() {
			// Use a provider.Error of ErrorTypeAuthFailure — classifies as
			// SeverityCritical per severityFromProviderErrorType. The
			// dispatcher's chunk-tap mints the safeMsg + correlation_id
			// and stamps SetCriticalError BEFORE the terminal Fail.
			critErr := &provider.Error{
				Provider:  "anthropic",
				ErrorType: provider.ErrorTypeAuthFailure,
				Message:   "401 unauthorized",
			}
			probe := &turnProbeStreamer{
				chunks: []provider.StreamChunk{
					{Content: "partial"},
					{Error: critErr, Done: true},
				},
				emitInterval: 5 * time.Millisecond,
			}
			mgr := &turnSessionManager{
				sess:     session.Session{ID: "sess-1c-gamma-crit", AgentID: "default-assistant"},
				streamer: probe,
			}
			turns := turn.NewRegistry()
			d := dispatch.NewWithTurns(probe, eng, swarmer, reg, mgr, turns)

			handle, err := d.DispatchSessioned(context.Background(), dispatch.DispatchRequest{
				SessionID:    "sess-1c-gamma-crit",
				AgentID:      "default-assistant",
				Content:      "trigger",
				ScanMentions: false,
			}, nil)
			Expect(err).NotTo(HaveOccurred())

			// Eventually the wrap goroutine drains the error chunk,
			// classifies it as critical, and stamps CriticalError BEFORE
			// the terminal Fail. The CriticalError pointer survives the
			// terminal transition (snapshotLocked deep-copies it).
			Eventually(func() bool {
				t, gerr := turns.Get(handle.TurnID)
				if gerr != nil {
					return false
				}
				return t.CriticalError != nil
			}, "2s", "10ms").Should(BeTrue(),
				"the wrap goroutine must tap chunk.Error, classify via IsCriticalStreamError, and call SetCriticalError — the long-poll surface then exposes the banner payload without an SSE side-channel")

			t, _ := turns.Get(handle.TurnID)
			Expect(t.CriticalError.Message).To(Equal("critical stream error"),
				"the safeMsg must match clientError's stream_critical category — never the raw provider error text")
			Expect(t.CriticalError.CorrelationID).To(MatchRegexp(`^[0-9a-f]{16}$`),
				"correlation_id must be 16 hex chars — matches the 8-random-bytes shape internal/api/errors.go uses")
			Expect(t.CriticalError.Severity).To(Equal("critical"))
			// The terminal Fail still fires — the chunk.Error capture and
			// the CriticalError stamp are independent paths.
			Expect(t.Status).To(Equal(turn.StatusFailed))
		})

		It("uses the stream_critical_context_exceeded safeMsg for context-window overflow errors", func() {
			critErr := &provider.Error{
				Provider:  "anthropic",
				ErrorType: provider.ErrorTypeContextWindowExceeded,
				Message:   "context_length_exceeded",
			}
			probe := &turnProbeStreamer{
				chunks: []provider.StreamChunk{
					{Error: critErr, Done: true},
				},
				emitInterval: 5 * time.Millisecond,
			}
			mgr := &turnSessionManager{
				sess:     session.Session{ID: "sess-ctx-exceeded", AgentID: "default-assistant"},
				streamer: probe,
			}
			turns := turn.NewRegistry()
			d := dispatch.NewWithTurns(probe, eng, swarmer, reg, mgr, turns)

			handle, err := d.DispatchSessioned(context.Background(), dispatch.DispatchRequest{
				SessionID:    "sess-ctx-exceeded",
				AgentID:      "default-assistant",
				Content:      "trigger",
				ScanMentions: false,
			}, nil)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() bool {
				t, gerr := turns.Get(handle.TurnID)
				if gerr != nil {
					return false
				}
				return t.CriticalError != nil
			}, "2s", "10ms").Should(BeTrue())

			t, _ := turns.Get(handle.TurnID)
			Expect(t.CriticalError.Message).To(ContainSubstring("context window exceeded"),
				"context-window overflow must surface the user-actionable safeMsg — the user can self-recover by trimming or starting a fresh session")
		})

		It("does NOT stamp CriticalError for a transient (non-critical) chunk.Error", func() {
			// A bare-string error classifies through severityFromKeywords;
			// "connection refused" returns SeverityTransient, NOT critical
			// (per internal/provider/stream_error_test.go's existing pin).
			probe := &turnProbeStreamer{
				chunks: []provider.StreamChunk{
					{Error: &transientError{msg: "connection refused"}, Done: true},
				},
				emitInterval: 5 * time.Millisecond,
			}
			mgr := &turnSessionManager{
				sess:     session.Session{ID: "sess-transient", AgentID: "default-assistant"},
				streamer: probe,
			}
			turns := turn.NewRegistry()
			d := dispatch.NewWithTurns(probe, eng, swarmer, reg, mgr, turns)

			handle, err := d.DispatchSessioned(context.Background(), dispatch.DispatchRequest{
				SessionID:    "sess-transient",
				AgentID:      "default-assistant",
				Content:      "trigger",
				ScanMentions: false,
			}, nil)
			Expect(err).NotTo(HaveOccurred())

			// Wait for terminal — the turn will Fail with the transient
			// error, but CriticalError must STAY nil (the banner is for
			// critical-only signals).
			Eventually(func() turn.Status {
				t, _ := turns.Get(handle.TurnID)
				return t.Status
			}, "2s", "10ms").Should(Equal(turn.StatusFailed))

			t, _ := turns.Get(handle.TurnID)
			Expect(t.CriticalError).To(BeNil(),
				"non-critical errors MUST NOT stamp CriticalError — the persistent banner is for unrecoverable provider errors only")
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
			d := dispatch.NewWithTurns(probe, eng, swarmer, reg, mgr, turns)

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
				}, broker)
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
