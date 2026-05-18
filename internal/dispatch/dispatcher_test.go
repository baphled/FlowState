package dispatch_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/dispatch"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/swarm"
)

func TestDispatch(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Dispatch Suite")
}

// dripStreamer mimics the real engine streamer for ctx-binding specs:
// returns the chunks channel BEFORE all chunks emit, drips them over
// time, and honours ctx.Done() by emitting a final {Error: ctx.Err(),
// Done: true} chunk. Mirrors internal/api/server_test.go::dripStreamer
// so the Dispatcher's ctx-detach contract can be observed under the
// same fixture shape the API suite uses.
type dripStreamer struct {
	mu              sync.Mutex
	chunks          []provider.StreamChunk
	emitInterval    time.Duration
	capturedAgentID string
	capturedMessage string
	streamCtx       context.Context
}

func (d *dripStreamer) Stream(ctx context.Context, agentID, message string) (<-chan provider.StreamChunk, error) {
	d.mu.Lock()
	d.capturedAgentID = agentID
	d.capturedMessage = message
	d.streamCtx = ctx
	d.mu.Unlock()
	out := make(chan provider.StreamChunk, 1)
	go func() {
		defer close(out)
		for _, c := range d.chunks {
			select {
			case <-ctx.Done():
				select {
				case out <- provider.StreamChunk{Error: ctx.Err(), Done: true}:
				default:
				}
				return
			case <-time.After(d.emitInterval):
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

func (d *dripStreamer) lastAgentID() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.capturedAgentID
}

func (d *dripStreamer) lastMessage() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.capturedMessage
}

func (d *dripStreamer) lastCtx() context.Context {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.streamCtx
}

// recordingConsumer captures every chunk + the terminal Done call so
// the spec can assert "consumer saw N content chunks followed by Done"
// without needing to wire the full SSE/writer stack.
type recordingConsumer struct {
	mu      sync.Mutex
	chunks  []string
	errs    []error
	doneCnt int
}

func (r *recordingConsumer) WriteChunk(content string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.chunks = append(r.chunks, content)
	return nil
}

func (r *recordingConsumer) WriteError(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errs = append(r.errs, err)
}

func (r *recordingConsumer) Done() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.doneCnt++
}

func (r *recordingConsumer) joinedChunks() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.chunks, "")
}

func (r *recordingConsumer) doneCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.doneCnt
}

// fakeDispatchEngine satisfies swarm.DispatchEngine. Counts the swarm
// lifecycle calls so the swarm-dispatch spec can pin that Dispatcher
// reached the engine surface, not just the streamer.
//
// Phase 3 addition: records every lifecycle call (SetSwarmContext +
// RestoreManifest + FlushSwarmLifecycle) into an ordered events log so
// the handshake spec can assert non-interleaved ordering across
// consecutive turns. Each event captures the call name and (when
// applicable) the swarm context's SwarmID so manifest-isolation can be
// pinned per-turn.
type fakeDispatchEngine struct {
	mu               sync.Mutex
	installedContext *swarm.Context
	flushCalls       int
	snapshotCalls    int
	restoreCalls     int
	events           []lifecycleEvent
}

// lifecycleEvent records one lifecycle method call on the engine fake.
// swarmID is "" for RestoreManifest / FlushSwarmLifecycle (no context
// arg); SetSwarmContext records the installed context's SwarmID.
type lifecycleEvent struct {
	call    string
	swarmID string
}

func (f *fakeDispatchEngine) SetSwarmContext(ctx *swarm.Context) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.installedContext = ctx
	evt := lifecycleEvent{call: "SetSwarmContext"}
	if ctx != nil {
		evt.swarmID = ctx.SwarmID
	}
	f.events = append(f.events, evt)
}

func (f *fakeDispatchEngine) FlushSwarmLifecycle(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.flushCalls++
	f.events = append(f.events, lifecycleEvent{call: "FlushSwarmLifecycle"})
	return nil
}

func (f *fakeDispatchEngine) ManifestSnapshot() any {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snapshotCalls++
	f.events = append(f.events, lifecycleEvent{call: "ManifestSnapshot"})
	return nil
}

func (f *fakeDispatchEngine) RestoreManifest(_ any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restoreCalls++
	f.events = append(f.events, lifecycleEvent{call: "RestoreManifest"})
}

func (f *fakeDispatchEngine) SkipAgentFiles() bool    { return false }
func (f *fakeDispatchEngine) SetSkipAgentFiles(_ bool) {}
func (f *fakeDispatchEngine) installed() *swarm.Context {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.installedContext
}

func (f *fakeDispatchEngine) flushCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.flushCalls
}

// eventLog returns a copy of the recorded lifecycle events. Used by the
// Phase 3 handshake spec to assert the [SetSwarmContext(turn1), …,
// RestoreManifest, SetSwarmContext(turn2), …, RestoreManifest] order is
// preserved across consecutive POSTs on the same session.
func (f *fakeDispatchEngine) eventLog() []lifecycleEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]lifecycleEvent, len(f.events))
	copy(out, f.events)
	return out
}

// withSetSwarmContextHook registers a per-instance callback fired on
// every SetSwarmContext call. The cross-session non-blocking spec uses
// this to record wall-clock times for sess-A and sess-B's first
// SetSwarmContext arrivals and assert they land inside a 200ms window
// even when each turn's drip emission spans 5s.
type hookedDispatchEngine struct {
	*fakeDispatchEngine
	onSetSwarmContext func(*swarm.Context)
}

func (h *hookedDispatchEngine) SetSwarmContext(ctx *swarm.Context) {
	h.fakeDispatchEngine.SetSwarmContext(ctx)
	if h.onSetSwarmContext != nil {
		h.onSetSwarmContext(ctx)
	}
}

var _ = Describe("Dispatcher.DispatchEphemeral", func() {
	var (
		reg     *agent.Registry
		swarmer *swarm.Registry
		eng     *fakeDispatchEngine
	)

	BeforeEach(func() {
		reg = agent.NewRegistry()
		reg.Register(&agent.Manifest{ID: "test-agent", Name: "Test Agent"})
		swarmer = swarm.NewRegistry()
		eng = &fakeDispatchEngine{}
	})

	Context("given an agent target with no @-mention", func() {
		It("streams from the requested agent and reports clean completion on Done", func() {
			drip := &dripStreamer{
				chunks: []provider.StreamChunk{
					{Content: "hello"},
					{Content: " world"},
					{Done: true},
				},
				emitInterval: 1 * time.Millisecond,
			}
			d := dispatch.New(drip, eng, swarmer, reg, nil, nil)
			cons := &recordingConsumer{}

			handle, err := d.DispatchEphemeral(context.Background(), dispatch.DispatchRequest{
				AgentID:      "test-agent",
				Content:      "hi",
				ScanMentions: true,
			}, cons)
			Expect(err).NotTo(HaveOccurred())
			Expect(handle.Done).NotTo(BeNil())

			Eventually(handle.Done, "2s").Should(Receive(BeNil()))
			Expect(drip.lastAgentID()).To(Equal("test-agent"))
			Expect(drip.lastMessage()).To(Equal("hi"))
			Expect(cons.joinedChunks()).To(Equal("hello world"))
			Expect(cons.doneCount()).To(Equal(1))
		})

		It("survives caller-context cancellation because the streamer ctx is detached", func() {
			drip := &dripStreamer{
				chunks: []provider.StreamChunk{
					{Content: "first"},
					{Content: "second"},
					{Done: true},
				},
				emitInterval: 5 * time.Millisecond,
			}
			d := dispatch.New(drip, eng, swarmer, reg, nil, nil)
			cons := &recordingConsumer{}

			callerCtx, cancel := context.WithCancel(context.Background())
			handle, err := d.DispatchEphemeral(callerCtx, dispatch.DispatchRequest{
				AgentID: "test-agent",
				Content: "hi",
			}, cons)
			Expect(err).NotTo(HaveOccurred())

			// Cancel the caller's ctx IMMEDIATELY — simulates the
			// handler returning before the streamer drains. The
			// Dispatcher must have wrapped via context.WithoutCancel,
			// so the streamer continues to emit all chunks.
			cancel()

			Eventually(handle.Done, "2s").Should(Receive(BeNil()))
			Expect(cons.joinedChunks()).To(Equal("firstsecond"))
			// The streamer's captured ctx must NOT have been cancelled
			// — it's the wrapped, decoupled ctx.
			Expect(drip.lastCtx().Err()).To(BeNil())
		})
	})

	Context("given an @-mention that resolves to a swarm", func() {
		BeforeEach(func() {
			reg.Register(&agent.Manifest{ID: "swarm-lead", Name: "Lead"})
			swarmer.Register(&swarm.Manifest{
				SchemaVersion: "1.0.0",
				ID:            "team",
				Lead:          "swarm-lead",
				Members:       []string{},
			})
		})

		It("dispatches through swarm.DispatchSwarm with the swarm context installed", func() {
			drip := &dripStreamer{
				chunks: []provider.StreamChunk{
					{Content: "swarm-response"},
					{Done: true},
				},
				emitInterval: 1 * time.Millisecond,
			}
			d := dispatch.New(drip, eng, swarmer, reg, nil, nil)
			cons := &recordingConsumer{}

			handle, err := d.DispatchEphemeral(context.Background(), dispatch.DispatchRequest{
				AgentID:      "test-agent",
				Content:      "please @team look at this",
				ScanMentions: true,
			}, cons)
			Expect(err).NotTo(HaveOccurred())
			Eventually(handle.Done, "2s").Should(Receive(BeNil()))

			Expect(drip.lastAgentID()).To(Equal("swarm-lead"))
			Expect(eng.installed()).NotTo(BeNil())
			Expect(eng.installed().SwarmID).To(Equal("team"))
			Expect(eng.flushCallCount()).To(Equal(1))
		})
	})

	Context("when no agent target resolves", func() {
		It("returns an error synchronously without spawning the streamer goroutine", func() {
			drip := &dripStreamer{}
			d := dispatch.New(drip, eng, swarmer, reg, nil, nil)
			cons := &recordingConsumer{}

			handle, err := d.DispatchEphemeral(context.Background(), dispatch.DispatchRequest{
				Content: "no target here",
			}, cons)
			Expect(err).To(HaveOccurred())
			Expect(handle.Done).To(BeNil())
			Expect(cons.doneCount()).To(Equal(0))
		})
	})

	Context("when AgentID is unknown to both registries", func() {
		It("returns a NotFoundError without driving the streamer", func() {
			drip := &dripStreamer{}
			d := dispatch.New(drip, eng, swarmer, reg, nil, nil)
			cons := &recordingConsumer{}

			_, err := d.DispatchEphemeral(context.Background(), dispatch.DispatchRequest{
				AgentID: "ghost",
				Content: "hi",
			}, cons)
			Expect(err).To(HaveOccurred())
			var notFound *swarm.NotFoundError
			Expect(errors.As(err, &notFound)).To(BeTrue())
		})
	})
})

// fakeSessionManager satisfies dispatch.SessionManager for the Phase 2
// DispatchSessioned specs. It owns one in-memory session.Session and
// drives the supplied streamer when SendMessageWithAttachments fires —
// the user message append and the chunks-channel handoff observe the
// same ordering the production *session.Manager produces.
type fakeSessionManager struct {
	mu       sync.Mutex
	sess     session.Session
	streamer *dripStreamer
	// streamErr surfaces from SendMessageWithAttachments before the
	// channel is drained, mirroring ErrSessionNotFound /
	// ErrAttachmentNotFound semantics.
	streamErr error
	// lastStreamCtx captures the ctx passed into SendMessageWithAttachments
	// so the spec can assert WithStreamAgentOverride threaded through and
	// context.WithoutCancel decoupled the request ctx.
	lastStreamCtx context.Context
}

func (f *fakeSessionManager) SnapshotSession(_ string) (session.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.sess
	out.Messages = append([]session.Message(nil), f.sess.Messages...)
	return out, nil
}

func (f *fakeSessionManager) SendMessageWithAttachments(
	ctx context.Context, _, message string, _ []string,
) (<-chan provider.StreamChunk, error) {
	f.mu.Lock()
	if f.streamErr != nil {
		err := f.streamErr
		f.mu.Unlock()
		return nil, err
	}
	f.lastStreamCtx = ctx
	// Append the user message inside the lock, matching
	// session/manager.go:1235-1242's critical-section shape.
	f.sess.Messages = append(f.sess.Messages, session.Message{
		Role:    "user",
		Content: message,
	})
	streamer := f.streamer
	f.mu.Unlock()

	if streamer == nil {
		// Production-mode parity: a nil-streamer session manager would
		// return (nil, nil) — the dispatcher's nil-chunks branch handles
		// it by skipping the broker.Publish goroutine.
		return nil, nil
	}
	return streamer.Stream(ctx, "fake-agent", message)
}

// fakeBroker satisfies dispatch.SessionBroker. Captures the published
// chunks so the spec can pin (a) Publish was called, (b) every chunk
// the streamer produced reached the broker (no swarm-lifecycle wrap
// dropouts).
type fakeBroker struct {
	mu             sync.Mutex
	publishedCount int
	collected      []provider.StreamChunk
	done           chan struct{}
}

func newFakeBroker() *fakeBroker {
	return &fakeBroker{done: make(chan struct{}, 1)}
}

func (b *fakeBroker) Publish(_ string, chunks <-chan provider.StreamChunk) {
	b.mu.Lock()
	b.publishedCount++
	b.mu.Unlock()
	for c := range chunks {
		b.mu.Lock()
		b.collected = append(b.collected, c)
		b.mu.Unlock()
	}
	select {
	case b.done <- struct{}{}:
	default:
	}
}

func (b *fakeBroker) collectedChunks() []provider.StreamChunk {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]provider.StreamChunk, len(b.collected))
	copy(out, b.collected)
	return out
}

func (b *fakeBroker) publishCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.publishedCount
}

// Phase 2 GREEN gate per "Dispatcher Service Unification (May 2026)"
// v6. These specs subsume the deleted server.go helpers' tests
// (resolveAutoDispatchSwarm @ 07b0480e, resolveInContentMention @
// 48380376, wrapWithSwarmLifecycle as a side-effect of the same
// commits). The handler-thinness regression at
// internal/api/handler_thinness_test.go pins that no banned symbols
// remain in handleSessionMessage; THIS file pins the behaviour the
// migration preserves.
var _ = Describe("Dispatcher.DispatchSessioned", func() {
	var (
		reg     *agent.Registry
		swarmer *swarm.Registry
		eng     *fakeDispatchEngine
		mgr     *fakeSessionManager
		broker  *fakeBroker
		drip    *dripStreamer
	)

	BeforeEach(func() {
		reg = agent.NewRegistry()
		reg.Register(&agent.Manifest{ID: "default-assistant", Name: "Default Assistant"})
		reg.Register(&agent.Manifest{ID: "coordinator", Name: "Coordinator"})
		reg.Register(&agent.Manifest{ID: "team-lead", Name: "Team Lead"})

		swarmer = swarm.NewRegistry()
		// meta-swarm with coordinator as auto-dispatch lead — pins the
		// "session whose agent_id is an auto-dispatch lead" branch
		// (subsumes 07b0480e).
		swarmer.Register(&swarm.Manifest{
			SchemaVersion:      "1.0.0",
			ID:                 "meta-swarm",
			Lead:               "coordinator",
			Members:            []string{"a-team", "dev-swarm"},
			AutoDispatchOnLead: true,
		})
		// a-team for the @<swarm-id> mention path — no
		// AutoDispatchOnLead so the spec proves mention dispatch works
		// even when the swarm is not auto-dispatch enabled (subsumes
		// 48380376).
		swarmer.Register(&swarm.Manifest{
			SchemaVersion: "1.0.0",
			ID:            "a-team",
			Lead:          "team-lead",
			Members:       []string{"team-lead", "default-assistant"},
		})

		eng = &fakeDispatchEngine{}
		broker = newFakeBroker()
		drip = &dripStreamer{
			chunks: []provider.StreamChunk{
				{Content: "ack"},
				{Done: true},
			},
			emitInterval: 1 * time.Millisecond,
		}
		mgr = &fakeSessionManager{
			sess: session.Session{
				ID:      "sess-1",
				AgentID: "default-assistant",
			},
			streamer: drip,
		}
	})

	Context("given a session whose agent_id is an auto-dispatch swarm lead", func() {
		// Subsumes server_test.go::Describe("POST /api/v1/sessions/{id}/messages
		// swarm auto-dispatch") ~lines 5028-5157. Moved into Dispatcher
		// per Phase 2 of the v6 plan.
		BeforeEach(func() {
			mgr.sess.AgentID = "coordinator"
		})

		It("installs the swarm context on the engine and snapshots the manifest before streaming", func() {
			d := dispatch.New(drip, eng, swarmer, reg, mgr, broker)

			handle, err := d.DispatchSessioned(context.Background(), dispatch.DispatchRequest{
				SessionID:    "sess-1",
				AgentID:      "coordinator",
				Content:      "please plan something",
				ScanMentions: true,
			}, nil)
			Expect(err).NotTo(HaveOccurred())

			// Snapshot returns synchronously WITH the new user message
			// appended. This is the load-bearing async-POST contract:
			// the caller writes the Snapshot back as JSON BEFORE the
			// streamer drains.
			Expect(handle.Snapshot.Messages).To(HaveLen(1))
			Expect(handle.Snapshot.Messages[0].Content).To(Equal("please plan something"))
			Expect(handle.Snapshot.Messages[0].Role).To(Equal("user"))

			// Swarm context installed BEFORE the streamer began — captured
			// here after the Dispatcher returned because SetSwarmContext
			// fires synchronously on the dispatch path.
			Expect(eng.installed()).NotTo(BeNil(),
				"a session whose agent_id leads an auto-dispatch swarm must install the swarm context on the engine before streaming")
			Expect(eng.installed().SwarmID).To(Equal("meta-swarm"))
			Expect(eng.installed().LeadAgent).To(Equal("coordinator"))
			Expect(eng.installed().Members).To(ConsistOf("a-team", "dev-swarm"))

			// Broker.Publish goroutine spawned + chunks drained.
			Eventually(broker.publishCount, "2s").Should(Equal(1))
			Eventually(broker.done, "2s").Should(Receive(),
				"broker.Publish must drain the chunks channel — proves the FlushSwarmLifecycle goroutine doesn't dead-lock chunk delivery")

			// Manifest restore fired after chunks drained. Pins the
			// swarm-lifecycle wrap moved from the deleted
			// wrapWithSwarmLifecycle helper.
			Eventually(eng.flushCallCount, "2s").Should(Equal(1))
		})
	})

	Context("given a session whose agent_id is a plain agent", func() {
		It("skips the swarm lifecycle entirely and forwards chunks to the broker", func() {
			d := dispatch.New(drip, eng, swarmer, reg, mgr, broker)

			handle, err := d.DispatchSessioned(context.Background(), dispatch.DispatchRequest{
				SessionID:    "sess-1",
				AgentID:      "default-assistant",
				Content:      "hello",
				ScanMentions: true,
			}, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(handle.Snapshot.Messages).To(HaveLen(1))

			// No swarm context, no manifest snapshot, no flush — same
			// pass-through contract the pre-Phase-2 resolveAutoDispatchSwarm
			// helper produced when AutoDispatchSwarmFor returned false.
			Eventually(broker.publishCount, "2s").Should(Equal(1))
			Eventually(broker.done, "2s").Should(Receive())
			Expect(eng.installed()).To(BeNil())
			Expect(eng.flushCallCount()).To(Equal(0))
		})
	})

	Context("given a plain-agent session whose message body contains a swarm @-mention", func() {
		// Subsumes server_test.go::Describe(...) "when the message body
		// contains a swarm @-mention" Context @ lines 5177-5308. Moved
		// into Dispatcher per Phase 2 of the v6 plan.

		It("redirects this turn through the mention's swarm lead", func() {
			d := dispatch.New(drip, eng, swarmer, reg, mgr, broker)

			handle, err := d.DispatchSessioned(context.Background(), dispatch.DispatchRequest{
				SessionID:    "sess-1",
				AgentID:      "default-assistant",
				Content:      "@a-team please help",
				ScanMentions: true,
			}, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(handle.Snapshot.Messages).To(HaveLen(1))

			Expect(eng.installed()).NotTo(BeNil(),
				"the in-content @<swarm-id> mention must install the swarm context on the engine even when the session's agent_id is a plain agent")
			Expect(eng.installed().SwarmID).To(Equal("a-team"))
			Expect(eng.installed().LeadAgent).To(Equal("team-lead"))

			// WithStreamAgentOverride must thread through to
			// SendMessageWithAttachments's ctx so the streamer drives
			// under the swarm's lead — pin via context extractor.
			override := session.StreamAgentOverrideFromContext(mgr.lastStreamCtx)
			Expect(override).To(Equal("team-lead"),
				"the mention's lead must thread through ctx via session.WithStreamAgentOverride so the engine stamps the assistant turn under the swarm's lead")

			Eventually(broker.publishCount, "2s").Should(Equal(1))
		})

		It("does NOT mutate the session's persistent agent_id (Option A per-turn semantics)", func() {
			d := dispatch.New(drip, eng, swarmer, reg, mgr, broker)

			_, err := d.DispatchSessioned(context.Background(), dispatch.DispatchRequest{
				SessionID:    "sess-1",
				AgentID:      "default-assistant",
				Content:      "@a-team do the thing",
				ScanMentions: true,
			}, nil)
			Expect(err).NotTo(HaveOccurred())

			// The Dispatcher must NOT have written CurrentAgentID on
			// the session — the redirect is per-turn. This matches the
			// deleted spec at server_test.go:5252-5256.
			mgr.mu.Lock()
			defer mgr.mu.Unlock()
			Expect(mgr.sess.AgentID).To(Equal("default-assistant"),
				"in-content @-mentions must not mutate the session's creation agent_id")
			Expect(mgr.sess.CurrentAgentID).To(Equal(""),
				"in-content @-mentions must not write CurrentAgentID")
		})

		It("falls back to the session's persistent agent when no swarm mention resolves", func() {
			d := dispatch.New(drip, eng, swarmer, reg, mgr, broker)

			handle, err := d.DispatchSessioned(context.Background(), dispatch.DispatchRequest{
				SessionID:    "sess-1",
				AgentID:      "default-assistant",
				Content:      "plain message, no @-mention here",
				ScanMentions: true,
			}, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(handle.Snapshot.Messages).To(HaveLen(1))

			Expect(eng.installed()).To(BeNil(),
				"absent any swarm @-mention the session's plain agent must keep driving the stream")
			Eventually(broker.publishCount, "2s").Should(Equal(1))
		})
	})

	Context("when the caller's context is cancelled after DispatchSessioned returns", func() {
		// Subsumes the e4bf9632 + 51fb416c pin: handler ctx-cancel must
		// NOT propagate to the streamer because Dispatcher applies
		// context.WithoutCancel internally. Drives the same dripStreamer
		// shape the server_test.go::"delivers full content stream after
		// handler returns" spec uses.
		It("streamer continues to completion because Dispatcher applied context.WithoutCancel", func() {
			drip.chunks = []provider.StreamChunk{
				{Content: "Hello"},
				{Content: " world"},
				{Done: true},
			}
			drip.emitInterval = 5 * time.Millisecond
			d := dispatch.New(drip, eng, swarmer, reg, mgr, broker)

			callerCtx, cancel := context.WithCancel(context.Background())
			handle, err := d.DispatchSessioned(callerCtx, dispatch.DispatchRequest{
				SessionID:    "sess-1",
				AgentID:      "default-assistant",
				Content:      "hi",
				ScanMentions: false,
			}, nil)
			Expect(err).NotTo(HaveOccurred())

			// Cancel the caller's ctx IMMEDIATELY — simulates the
			// handler returning before the broker drains.
			cancel()

			Eventually(broker.done, "2s").Should(Receive(),
				"broker.Publish must drain the chunks channel even after handler-ctx cancel — context.WithoutCancel detaches the streamer ctx from the caller's")
			// The captured stream ctx must NOT be cancelled — it's the
			// WithoutCancel-wrapped ctx, not the caller's.
			Expect(mgr.lastStreamCtx.Err()).To(BeNil(),
				"the ctx threaded to SendMessageWithAttachments must NOT carry cancellation from the caller — Dispatcher must apply context.WithoutCancel at the seam")

			// All content chunks reached the broker; no Error: context.Canceled.
			collected := broker.collectedChunks()
			var got []string
			for _, c := range collected {
				if c.Content != "" {
					got = append(got, c.Content)
				}
				Expect(c.Error).To(BeNil(),
					"no chunk may carry a context.Canceled error — handler ctx must be decoupled")
			}
			Expect(strings.Join(got, "")).To(Equal("Hello world"))
			_ = handle
		})
	})

	Context("when sessionManager returns an error before streaming starts", func() {
		It("returns the error synchronously and restores the manifest when swarm was already active", func() {
			mgr.sess.AgentID = "coordinator"
			mgr.streamErr = errors.New("simulated attachment not found")
			d := dispatch.New(drip, eng, swarmer, reg, mgr, broker)

			_, err := d.DispatchSessioned(context.Background(), dispatch.DispatchRequest{
				SessionID:    "sess-1",
				AgentID:      "coordinator",
				Content:      "x",
				ScanMentions: true,
			}, nil)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("simulated attachment"))

			// Manifest must be restored on the failure path so the
			// engine isn't left re-identified as the swarm lead.
			Expect(eng.restoreCalls).To(Equal(1))
			// Broker.Publish must NOT have been called — no chunks
			// channel existed.
			Expect(broker.publishCount()).To(Equal(0))
		})
	})

	Context("when no session manager is wired", func() {
		It("returns an explicit error so test-surface compositions fail loudly", func() {
			d := dispatch.New(drip, eng, swarmer, reg, nil, nil)
			_, err := d.DispatchSessioned(context.Background(), dispatch.DispatchRequest{}, nil)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("sessionManager not configured"))
		})
	})
})

// multiSessionManager satisfies dispatch.SessionManager for the Phase 3
// handshake specs. Unlike fakeSessionManager, it (a) tracks state for
// multiple sessions independently, (b) builds a FRESH per-turn drip
// channel every SendMessageWithAttachments call so consecutive turns
// have non-overlapping channel lifetimes, and (c) emits a turn-specific
// timing signal so the spec can observe ordering across turns. Each
// session's drips share the same per-mgr emitInterval to keep timing
// deterministic.
type multiSessionManager struct {
	mu           sync.Mutex
	sessions     map[string]*session.Session
	emitInterval time.Duration
	// chunks is the per-turn chunk template; every turn drips a fresh
	// copy of this slice into a new channel.
	chunks []provider.StreamChunk
	// streamCtxByCall captures every ctx threaded to
	// SendMessageWithAttachments so cross-session specs can assert
	// per-call ctx propagation.
	streamCtxByCall []context.Context
}

func newMultiSessionManager(emitInterval time.Duration, chunks []provider.StreamChunk) *multiSessionManager {
	return &multiSessionManager{
		sessions:     make(map[string]*session.Session),
		emitInterval: emitInterval,
		chunks:       chunks,
	}
}

func (m *multiSessionManager) seedSession(id, agentID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[id] = &session.Session{ID: id, AgentID: agentID}
}

func (m *multiSessionManager) SnapshotSession(id string) (session.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return session.Session{}, errors.New("multiSessionManager: unknown session " + id)
	}
	out := *s
	out.Messages = append([]session.Message(nil), s.Messages...)
	return out, nil
}

func (m *multiSessionManager) SendMessageWithAttachments(
	ctx context.Context, sessionID, message string, _ []string,
) (<-chan provider.StreamChunk, error) {
	m.mu.Lock()
	s, ok := m.sessions[sessionID]
	if !ok {
		m.mu.Unlock()
		return nil, errors.New("multiSessionManager: unknown session " + sessionID)
	}
	s.Messages = append(s.Messages, session.Message{Role: "user", Content: message})
	m.streamCtxByCall = append(m.streamCtxByCall, ctx)
	chunksTemplate := append([]provider.StreamChunk(nil), m.chunks...)
	interval := m.emitInterval
	m.mu.Unlock()

	out := make(chan provider.StreamChunk, 1)
	go func() {
		defer close(out)
		for _, c := range chunksTemplate {
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

func (m *multiSessionManager) sessionAgentID(id string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[id]; ok {
		return s.AgentID
	}
	return ""
}

// recordingBroker captures published chunks per session and signals
// completion via a per-session done channel so consecutive-POST specs
// can wait for turn N's broker.Publish drain before issuing turn N+1.
type recordingBroker struct {
	mu       sync.Mutex
	published map[string]int
	doneBy    map[string]chan struct{}
}

func newRecordingBroker() *recordingBroker {
	return &recordingBroker{
		published: make(map[string]int),
		doneBy:    make(map[string]chan struct{}),
	}
}

func (b *recordingBroker) Publish(sessionID string, chunks <-chan provider.StreamChunk) {
	b.mu.Lock()
	b.published[sessionID]++
	// One done channel per session, replaced per turn so callers can
	// await the LATEST turn's drain.
	done := make(chan struct{}, 1)
	b.doneBy[sessionID] = done
	b.mu.Unlock()

	for range chunks {
	}
	select {
	case done <- struct{}{}:
	default:
	}
}

func (b *recordingBroker) waitFor(sessionID string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		b.mu.Lock()
		done, ok := b.doneBy[sessionID]
		b.mu.Unlock()
		if ok {
			select {
			case <-done:
				return true
			case <-time.After(10 * time.Millisecond):
				continue
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func (b *recordingBroker) publishedCount(sessionID string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.published[sessionID]
}

// Phase 3 GREEN gate per "Dispatcher Service Unification (May 2026)"
// v6. Closes S2: the swarm-lifecycle race surface that preserves through
// Phase 2. DispatchSessioned for the SAME sessionID must serialise
// turns through a per-session lifecycle gate — turn N+1's
// SetSwarmContext MUST observe turn N's FlushSwarmLifecycle +
// RestoreManifest. Per-session keying preserves cross-session
// concurrency (anti-pattern per the plan: a single Dispatcher-wide
// mutex would silently serialise ALL /messages globally).
var _ = Describe("Swarm lifecycle handshake across consecutive POSTs", func() {
	var (
		reg     *agent.Registry
		swarmer *swarm.Registry
	)

	BeforeEach(func() {
		reg = agent.NewRegistry()
		reg.Register(&agent.Manifest{ID: "coordinator", Name: "Coordinator"})
		reg.Register(&agent.Manifest{ID: "team-lead", Name: "Team Lead"})
		reg.Register(&agent.Manifest{ID: "default-assistant", Name: "Default Assistant"})

		swarmer = swarm.NewRegistry()
		swarmer.Register(&swarm.Manifest{
			SchemaVersion:      "1.0.0",
			ID:                 "meta-swarm",
			Lead:               "coordinator",
			Members:            []string{"a-team"},
			AutoDispatchOnLead: true,
		})
		swarmer.Register(&swarm.Manifest{
			SchemaVersion: "1.0.0",
			ID:            "a-team",
			Lead:          "team-lead",
			Members:       []string{"team-lead", "default-assistant"},
		})
	})

	Context("two consecutive DispatchSessioned calls against the SAME sessionID", func() {
		It("serialises swarm lifecycle: turn 2's SetSwarmContext follows turn 1's FlushSwarmLifecycle + RestoreManifest", func() {
			eng := &fakeDispatchEngine{}
			// Drip interval is long enough that without the gate, turn 2's
			// SetSwarmContext would interleave with turn 1's still-draining
			// chunks (broker.Publish loop) and its post-drain lifecycle.
			mgr := newMultiSessionManager(20*time.Millisecond, []provider.StreamChunk{
				{Content: "ack"},
				{Done: true},
			})
			mgr.seedSession("sess-1", "coordinator")
			broker := newRecordingBroker()

			d := dispatch.New(nil, eng, swarmer, reg, mgr, broker)

			// Turn 1 routes through meta-swarm (auto-dispatch on coordinator).
			_, err := d.DispatchSessioned(context.Background(), dispatch.DispatchRequest{
				SessionID:    "sess-1",
				AgentID:      "coordinator",
				Content:      "plan something",
				ScanMentions: true,
			}, nil)
			Expect(err).NotTo(HaveOccurred())

			// Turn 2 routes through a-team via in-content mention. Issue
			// the call IMMEDIATELY — the gate is the only thing that may
			// make turn 2 wait for turn 1's lifecycle.
			_, err = d.DispatchSessioned(context.Background(), dispatch.DispatchRequest{
				SessionID:    "sess-1",
				AgentID:      "coordinator",
				Content:      "@a-team take over",
				ScanMentions: true,
			}, nil)
			Expect(err).NotTo(HaveOccurred())

			// Wait for both turns' broker drains to complete so the
			// lifecycle event log is final.
			Eventually(func() int { return broker.publishedCount("sess-1") }, "3s").Should(Equal(2))
			Eventually(func() bool { return broker.waitFor("sess-1", 2*time.Second) }, "3s").Should(BeTrue())
			Eventually(eng.flushCallCount, "3s").Should(Equal(2))

			// The recorded lifecycle event log MUST show turn 1's full
			// lifecycle (SetSwarmContext meta-swarm → ... → RestoreManifest)
			// BEFORE turn 2's SetSwarmContext. Filter to the relevant calls
			// and pin the exact order.
			events := eng.eventLog()
			var setCtx []string
			var restoreIdx, flushIdx []int
			for i, evt := range events {
				switch evt.call {
				case "SetSwarmContext":
					setCtx = append(setCtx, evt.swarmID)
				case "RestoreManifest":
					restoreIdx = append(restoreIdx, i)
				case "FlushSwarmLifecycle":
					flushIdx = append(flushIdx, i)
				}
			}

			Expect(setCtx).To(HaveLen(2),
				"each turn must call SetSwarmContext exactly once with the resolved swarm id")
			Expect(setCtx[0]).To(Equal("meta-swarm"),
				"turn 1 routes through meta-swarm (coordinator auto-dispatch)")
			Expect(setCtx[1]).To(Equal("a-team"),
				"turn 2 routes through a-team (in-content @-mention)")

			// Critical ordering assertion — locate turn 1's RestoreManifest
			// and assert it appears BEFORE turn 2's SetSwarmContext in the
			// event log. If the defer ordering is wrong (close-before-restore)
			// or the gate is missing entirely, turn 2's SetSwarmContext lands
			// inside turn 1's still-running lifecycle window.
			Expect(restoreIdx).To(HaveLen(2),
				"each turn must call RestoreManifest exactly once after its lifecycle drains")
			turn2SetIdx := -1
			turn1SetIdx := -1
			for i, evt := range events {
				if evt.call == "SetSwarmContext" {
					if evt.swarmID == "meta-swarm" && turn1SetIdx == -1 {
						turn1SetIdx = i
					}
					if evt.swarmID == "a-team" {
						turn2SetIdx = i
					}
				}
			}
			Expect(turn1SetIdx).To(BeNumerically(">=", 0))
			Expect(turn2SetIdx).To(BeNumerically(">", turn1SetIdx))
			Expect(restoreIdx[0]).To(BeNumerically("<", turn2SetIdx),
				"turn 1's RestoreManifest MUST land BEFORE turn 2's SetSwarmContext — the per-session lifecycle gate sequences the turns")
			Expect(flushIdx[0]).To(BeNumerically("<", turn2SetIdx),
				"turn 1's FlushSwarmLifecycle MUST also land BEFORE turn 2's SetSwarmContext")
		})

		It("isolates manifest state across turns: turn 2's ManifestSnapshot fires AFTER turn 1's RestoreManifest", func() {
			eng := &fakeDispatchEngine{}
			mgr := newMultiSessionManager(20*time.Millisecond, []provider.StreamChunk{
				{Content: "ack"},
				{Done: true},
			})
			mgr.seedSession("sess-1", "coordinator")
			broker := newRecordingBroker()

			d := dispatch.New(nil, eng, swarmer, reg, mgr, broker)

			_, err := d.DispatchSessioned(context.Background(), dispatch.DispatchRequest{
				SessionID:    "sess-1",
				AgentID:      "coordinator",
				Content:      "plan something",
				ScanMentions: true,
			}, nil)
			Expect(err).NotTo(HaveOccurred())

			_, err = d.DispatchSessioned(context.Background(), dispatch.DispatchRequest{
				SessionID:    "sess-1",
				AgentID:      "coordinator",
				Content:      "@a-team take over",
				ScanMentions: true,
			}, nil)
			Expect(err).NotTo(HaveOccurred())

			Eventually(eng.flushCallCount, "3s").Should(Equal(2))

			// Manifest-isolation contract: turn 2's ManifestSnapshot
			// (captured AT THE START of turn 2's dispatch) MUST come
			// AFTER turn 1's RestoreManifest. If the gate is missing or
			// the defer ordering is wrong (close-before-restore), turn 2
			// would snapshot the engine while it still carries turn 1's
			// swarm-lead manifest as the baseline. The snapshot then
			// reverts to STALE state when turn 2 fails / completes.
			events := eng.eventLog()
			var snapshotIdx, restoreIdx []int
			for i, evt := range events {
				switch evt.call {
				case "ManifestSnapshot":
					snapshotIdx = append(snapshotIdx, i)
				case "RestoreManifest":
					restoreIdx = append(restoreIdx, i)
				}
			}
			Expect(snapshotIdx).To(HaveLen(2),
				"each turn must call ManifestSnapshot once to capture pre-dispatch engine state")
			Expect(restoreIdx).To(HaveLen(2))
			Expect(snapshotIdx[1]).To(BeNumerically(">", restoreIdx[0]),
				"turn 2's ManifestSnapshot MUST land AFTER turn 1's RestoreManifest — the per-session gate ensures the engine is in baseline state when turn 2 captures its snapshot. Without the gate, turn 2 snapshots a half-restored engine and inherits stale manifest residue on its own RestoreManifest.")

			// And the LAST installed swarm context is the a-team lead
			// (turn 2's resolved context), proving the dispatch sequence
			// reached its target.
			Expect(eng.installed()).NotTo(BeNil())
			Expect(eng.installed().SwarmID).To(Equal("a-team"))
			Expect(eng.installed().LeadAgent).To(Equal("team-lead"))
		})
	})

	Context("two concurrent DispatchSessioned calls against DIFFERENT sessionIDs", func() {
		It("does NOT serialise across sessions — sess-A and sess-B's first SetSwarmContext land inside a 200ms window despite a 5s drip", func() {
			// 500ms emit interval × 10 chunks per turn = ~5s total drip
			// duration. If the gate were keyed Dispatcher-wide (anti-
			// pattern per the plan), sess-B would not reach
			// SetSwarmContext until sess-A's full ~5s lifecycle drained.
			// With per-session keying both Set calls land within ~200ms.
			chunks := []provider.StreamChunk{
				{Content: "a"}, {Content: "b"}, {Content: "c"}, {Content: "d"},
				{Content: "e"}, {Content: "f"}, {Content: "g"}, {Content: "h"},
				{Content: "i"}, {Done: true},
			}
			mgr := newMultiSessionManager(500*time.Millisecond, chunks)
			mgr.seedSession("sess-A", "coordinator")
			mgr.seedSession("sess-B", "coordinator")
			broker := newRecordingBroker()

			fakeEng := &fakeDispatchEngine{}
			times := make(map[string]time.Time)
			var timesMu sync.Mutex
			eng := &hookedDispatchEngine{
				fakeDispatchEngine: fakeEng,
				onSetSwarmContext: func(ctx *swarm.Context) {
					if ctx == nil {
						return
					}
					timesMu.Lock()
					defer timesMu.Unlock()
					// Record the FIRST SetSwarmContext per swarm context
					// id; this is keyed by SwarmID (meta-swarm) since both
					// sessions route through coordinator → meta-swarm
					// auto-dispatch. We instead key by call ordinal: first
					// arrival is sess-A or sess-B depending on goroutine
					// scheduling, second is the other.
					if _, ok := times["first"]; !ok {
						times["first"] = time.Now()
						return
					}
					if _, ok := times["second"]; !ok {
						times["second"] = time.Now()
					}
				},
			}

			d := dispatch.New(nil, eng, swarmer, reg, mgr, broker)

			var wg sync.WaitGroup
			wg.Add(2)
			start := time.Now()
			go func() {
				defer wg.Done()
				_, err := d.DispatchSessioned(context.Background(), dispatch.DispatchRequest{
					SessionID: "sess-A", AgentID: "coordinator", Content: "go A", ScanMentions: true,
				}, nil)
				Expect(err).NotTo(HaveOccurred())
			}()
			go func() {
				defer wg.Done()
				_, err := d.DispatchSessioned(context.Background(), dispatch.DispatchRequest{
					SessionID: "sess-B", AgentID: "coordinator", Content: "go B", ScanMentions: true,
				}, nil)
				Expect(err).NotTo(HaveOccurred())
			}()
			wg.Wait()
			handlerReturn := time.Since(start)

			// Both DispatchSessioned calls must have returned promptly
			// (well under the 5s drip). The handlers don't block on
			// stream completion — that's the async-POST contract.
			Expect(handlerReturn).To(BeNumerically("<", 1500*time.Millisecond),
				"DispatchSessioned MUST return synchronously after snapshot; concurrent calls on different sessions MUST NOT block each other at the handler boundary")

			// Both SetSwarmContext invocations must have fired
			// concurrently inside a 200ms window. A global gate (anti-
			// pattern) would push the second arrival 5s after the first.
			timesMu.Lock()
			first, firstOK := times["first"]
			second, secondOK := times["second"]
			timesMu.Unlock()
			Expect(firstOK).To(BeTrue(),
				"first SetSwarmContext must have been observed")
			Expect(secondOK).To(BeTrue(),
				"second SetSwarmContext must have been observed")
			gap := second.Sub(first)
			Expect(gap).To(BeNumerically("<", 200*time.Millisecond),
				"cross-session SetSwarmContext arrivals must land inside 200ms — per-session keying preserves cross-session concurrency (anti-pattern: a Dispatcher-wide mutex would make sess-B wait ~5s)")

			// Let both turns' lifecycles complete before the spec exits
			// so the goroutines drain cleanly.
			Eventually(fakeEng.flushCallCount, "10s").Should(Equal(2))
		})
	})

	Context("error-path leak: DispatchSessioned returns an error mid-turn", func() {
		It("releases the per-session gate so the NEXT call for the same sessionID can proceed", func() {
			eng := &fakeDispatchEngine{}
			// Use the original fakeSessionManager with streamErr to drive
			// the synchronous-error path BEFORE the chunks channel is
			// created — exercises the early-return branch that must still
			// close the gate via defer.
			drip := &dripStreamer{
				chunks:       []provider.StreamChunk{{Done: true}},
				emitInterval: 1 * time.Millisecond,
			}
			mgr := &fakeSessionManager{
				sess:      session.Session{ID: "sess-err", AgentID: "coordinator"},
				streamer:  drip,
				streamErr: errors.New("simulated swarm.ErrNotFound"),
			}
			broker := newFakeBroker()
			d := dispatch.New(drip, eng, swarmer, reg, mgr, broker)

			// First call: fails on the synchronous error path. The gate
			// MUST close via defer so the SECOND call doesn't deadlock.
			_, err := d.DispatchSessioned(context.Background(), dispatch.DispatchRequest{
				SessionID:    "sess-err",
				AgentID:      "coordinator",
				Content:      "boom",
				ScanMentions: true,
			}, nil)
			Expect(err).To(HaveOccurred())

			// Second call on the same session — clear the error, expect
			// it to proceed without blocking.
			mgr.mu.Lock()
			mgr.streamErr = nil
			mgr.mu.Unlock()

			done := make(chan struct{})
			go func() {
				defer close(done)
				_, err := d.DispatchSessioned(context.Background(), dispatch.DispatchRequest{
					SessionID:    "sess-err",
					AgentID:      "coordinator",
					Content:      "retry",
					ScanMentions: true,
				}, nil)
				Expect(err).NotTo(HaveOccurred())
			}()

			Eventually(done, "2s").Should(BeClosed(),
				"after the synchronous-error path, the per-session gate MUST be released — a permanently-blocked gate would deadlock the next call on this sessionID")
		})
	})
})
