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
			d := dispatch.New(drip, eng, swarmer, reg, nil)
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
			d := dispatch.New(drip, eng, swarmer, reg, nil)
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
			d := dispatch.New(drip, eng, swarmer, reg, nil)
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
			d := dispatch.New(drip, eng, swarmer, reg, nil)
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
			d := dispatch.New(drip, eng, swarmer, reg, nil)
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

var _ = Describe("Dispatcher.DispatchSessioned", func() {
	It("Phase 1 stub returns an explicit not-yet-wired error", func() {
		d := dispatch.New(nil, nil, nil, nil, nil)
		_, err := d.DispatchSessioned(context.Background(), dispatch.DispatchRequest{}, nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("Phase 2"))
	})
})
