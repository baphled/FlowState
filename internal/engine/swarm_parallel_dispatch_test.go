package engine_test

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/delegation"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/swarm"
)

// concurrencyProbeForEngine mirrors internal/swarm/dispatch_test.go's
// probe — same primitive, exposed so the engine-side tests don't
// reinvent the helper. Tracks max concurrent enter()/leave() pairs.
type concurrencyProbeForEngine struct {
	mu        sync.Mutex
	active    int
	maxActive int
	total     int
}

func newProbe() *concurrencyProbeForEngine {
	return &concurrencyProbeForEngine{}
}

func (p *concurrencyProbeForEngine) enter() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.active++
	p.total++
	if p.active > p.maxActive {
		p.maxActive = p.active
	}
}

func (p *concurrencyProbeForEngine) leave() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.active--
}

func (p *concurrencyProbeForEngine) snapshotMax() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.maxActive
}

// sharedBarrier holds the gate state every streamer in a fan-out
// shares. enterAndWait increments the arrival counter and releases
// once releaseAt arrivals have accumulated.
type sharedBarrier struct {
	gate    chan struct{}
	arrived int32
	target  int32
}

func newSharedBarrier(releaseAt int) *sharedBarrier {
	return &sharedBarrier{gate: make(chan struct{}), target: int32(releaseAt)}
}

func (b *sharedBarrier) enterAndWait(ctx context.Context) {
	if atomic.AddInt32(&b.arrived, 1) >= b.target {
		select {
		case <-b.gate:
		default:
			close(b.gate)
		}
	}
	select {
	case <-b.gate:
	case <-ctx.Done():
	}
}

// barrierStreamer returns a streaming.Streamer that blocks every
// member until the shared barrier hits releaseAt arrivals.
func barrierStreamer(probe *concurrencyProbeForEngine, barrier *sharedBarrier) streaming.Streamer {
	return streamerFunc(func(ctx context.Context, _ string, _ string) (<-chan provider.StreamChunk, error) {
		probe.enter()
		barrier.enterAndWait(ctx)
		probe.leave()
		ch := make(chan provider.StreamChunk, 1)
		ch <- provider.StreamChunk{Content: "ok", Done: true}
		close(ch)
		return ch, nil
	})
}

// boundedHoldStreamer marks enter/leave around a bounded sleep so
// MaxParallel clamping can be observed without depending on a fixed
// barrier-arrival count.
func boundedHoldStreamer(probe *concurrencyProbeForEngine, hold time.Duration) streaming.Streamer {
	return streamerFunc(func(ctx context.Context, _ string, _ string) (<-chan provider.StreamChunk, error) {
		probe.enter()
		select {
		case <-time.After(hold):
		case <-ctx.Done():
		}
		probe.leave()
		ch := make(chan provider.StreamChunk, 1)
		ch <- provider.StreamChunk{Content: "ok", Done: true}
		close(ch)
		return ch, nil
	})
}

// orderRecorderForEngine mirrors the swarm-package recorder; tracks
// the start:/end: ordering of member streams.
type orderRecorderForEngine struct {
	mu     sync.Mutex
	events []string
}

func newRecorder() *orderRecorderForEngine {
	return &orderRecorderForEngine{}
}

func (o *orderRecorderForEngine) record(s string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, s)
}

func (o *orderRecorderForEngine) snapshot() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]string, len(o.events))
	copy(out, o.events)
	return out
}

// recordingStreamer marks start: and end: events around a fixed wait
// so the test can confirm sequential mode respects roster order.
func recordingStreamer(rec *orderRecorderForEngine, member string) streaming.Streamer {
	return streamerFunc(func(_ context.Context, _ string, _ string) (<-chan provider.StreamChunk, error) {
		rec.record("start:" + member)
		time.Sleep(2 * time.Millisecond)
		rec.record("end:" + member)
		ch := make(chan provider.StreamChunk, 1)
		ch <- provider.StreamChunk{Content: "ok", Done: true}
		close(ch)
		return ch, nil
	})
}

// parallelManifest builds a swarm manifest pinned to parallel
// dispatch with cap, used to drive DispatchSwarmMembers.
func parallelManifest(id string, members []string, parallel bool, maxParallel int) *swarm.Manifest {
	return &swarm.Manifest{
		SchemaVersion: "1.0.0",
		ID:            id,
		Lead:          "lead",
		Members:       members,
		SwarmType:     swarm.SwarmTypeAnalysis,
		Harness: swarm.HarnessConfig{
			Parallel:    parallel,
			MaxParallel: maxParallel,
		},
	}
}

// buildLeadAndMemberEngines creates a lead plus N member engines and
// returns the engines map ready for DelegateTool wiring.
func buildLeadAndMemberEngines(memberIDs []string) (*engine.Engine, map[string]*engine.Engine) {
	lead := engine.New(engine.Config{
		ChatProvider: &mockProvider{name: "lead"},
		Manifest: agent.Manifest{
			ID:                "lead",
			Name:              "Lead",
			Instructions:      agent.Instructions{SystemPrompt: "lead"},
			Delegation:        agent.Delegation{CanDelegate: true},
			ContextManagement: agent.DefaultContextManagement(),
		},
	})
	engines := map[string]*engine.Engine{"lead": lead}
	for _, id := range memberIDs {
		engines[id] = engine.New(engine.Config{
			ChatProvider: &mockProvider{name: id},
			Manifest: agent.Manifest{
				ID:                id,
				Name:              id,
				Instructions:      agent.Instructions{SystemPrompt: id},
				ContextManagement: agent.DefaultContextManagement(),
			},
		})
	}
	return lead, engines
}

var _ = Describe("SwarmParallelDispatch", func() {
	Context("with Parallel=true and MaxParallel=2 over three members", func() {
		It("never lets more than MaxParallel members run concurrently", func() {
			members := []string{"alpha", "bravo", "charlie"}
			lead, engines := buildLeadAndMemberEngines(members)
			manifest := parallelManifest("parallel-swarm", members, true, 2)
			reg := swarm.NewRegistry()
			reg.Register(manifest)
			swarmCtx := swarm.NewContext(manifest.ID, manifest)
			lead.SetSwarmContext(&swarmCtx)

			probe := newProbe()
			barrier := newSharedBarrier(2)
			streamers := map[string]streaming.Streamer{}
			for _, m := range members {
				streamers[m] = barrierStreamer(probe, barrier)
			}

			delegateTool := engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "lead").
				WithStreamers(streamers).
				WithSwarmRegistry(reg)

			err := delegateTool.DispatchSwarmMembers(context.Background(), &swarmCtx, members, "go")

			Expect(err).NotTo(HaveOccurred())
			Expect(probe.snapshotMax()).To(BeNumerically("==", 2))
		})
	})

	Context("with Parallel=false (the default)", func() {
		It("runs members one at a time in roster order", func() {
			members := []string{"alpha", "bravo", "charlie"}
			lead, engines := buildLeadAndMemberEngines(members)
			manifest := parallelManifest("seq-swarm", members, false, 0)
			reg := swarm.NewRegistry()
			reg.Register(manifest)
			swarmCtx := swarm.NewContext(manifest.ID, manifest)
			lead.SetSwarmContext(&swarmCtx)

			rec := newRecorder()
			streamers := map[string]streaming.Streamer{}
			for _, m := range members {
				streamers[m] = recordingStreamer(rec, m)
			}

			delegateTool := engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "lead").
				WithStreamers(streamers).
				WithSwarmRegistry(reg)

			err := delegateTool.DispatchSwarmMembers(context.Background(), &swarmCtx, members, "go")

			Expect(err).NotTo(HaveOccurred())
			Expect(rec.snapshot()).To(Equal([]string{
				"start:alpha", "end:alpha",
				"start:bravo", "end:bravo",
				"start:charlie", "end:charlie",
			}))
		})
	})

	Context("when the manifest's MaxParallel exceeds MaxTotalBudget", func() {
		It("clamps the fan-out at the spawn-limits budget", func() {
			members := []string{"alpha", "bravo", "charlie", "delta"}
			lead, engines := buildLeadAndMemberEngines(members)
			manifest := parallelManifest("budget-swarm", members, true, 4)
			reg := swarm.NewRegistry()
			reg.Register(manifest)
			swarmCtx := swarm.NewContext(manifest.ID, manifest)
			lead.SetSwarmContext(&swarmCtx)

			probe := newProbe()
			streamers := map[string]streaming.Streamer{}
			for _, m := range members {
				streamers[m] = boundedHoldStreamer(probe, 25*time.Millisecond)
			}

			limits := delegation.DefaultSpawnLimits()
			limits.MaxTotalBudget = 2
			delegateTool := engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "lead").
				WithStreamers(streamers).
				WithSwarmRegistry(reg).
				WithSpawnLimits(limits)

			err := delegateTool.DispatchSwarmMembers(context.Background(), &swarmCtx, members, "go")

			Expect(err).NotTo(HaveOccurred())
			Expect(probe.snapshotMax()).To(BeNumerically("<=", 2))
		})
	})
})
