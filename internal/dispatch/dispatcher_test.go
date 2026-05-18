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
type fakeDispatchEngine struct {
	mu               sync.Mutex
	installedContext *swarm.Context
	flushCalls       int
	snapshotCalls    int
	restoreCalls     int
}

func (f *fakeDispatchEngine) SetSwarmContext(ctx *swarm.Context) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.installedContext = ctx
}

func (f *fakeDispatchEngine) FlushSwarmLifecycle(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.flushCalls++
	return nil
}

func (f *fakeDispatchEngine) ManifestSnapshot() any {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snapshotCalls++
	return nil
}

func (f *fakeDispatchEngine) RestoreManifest(_ any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restoreCalls++
}

func (f *fakeDispatchEngine) SkipAgentFiles() bool                { return false }
func (f *fakeDispatchEngine) SetSkipAgentFiles(_ bool)             {}
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
