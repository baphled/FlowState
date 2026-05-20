package engine_test

import (
	"context"
	"errors"
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

// stallingStreamer returns a streamer that opens a channel but never
// emits anything (and never closes it) until the per-call ctx is
// cancelled. Models a delegate child that goes silent mid-stream — the
// exact symptom the per-member timeout guards against. The returned
// signal channel closes once ctx.Done() fires so the test can prove
// the cancellation observed by the streamer was deadline-driven.
func stallingStreamer() (streaming.Streamer, <-chan context.Context) {
	cancelled := make(chan context.Context, 4)
	s := streamerFunc(func(ctx context.Context, _ string, _ string) (<-chan provider.StreamChunk, error) {
		ch := make(chan provider.StreamChunk)
		go func() {
			<-ctx.Done()
			select {
			case cancelled <- ctx:
			default:
			}
			close(ch)
		}()
		return ch, nil
	})
	return s, cancelled
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

	// Per-member timeout guards the parent against a stalled child
	// hanging the coordinator forever. Symptom: session 3255e2ee — a
	// coordinator dispatched researcher + executor in parallel; the
	// executor went silent mid-stream and the parent's
	// collectWithProgress await loop had no time.After branch, so the
	// parent session stayed active indefinitely. Fix: HarnessConfig
	// gains MemberTimeout; the per-member dispatch ctx is wrapped with
	// WithTimeout (zero = no deadline preserves current behaviour).
	Context("with Harness.MemberTimeout set and a stalled member stream", func() {
		It("returns the deadline error and cancels the sibling member", func() {
			members := []string{"stalls", "sibling"}
			lead, engines := buildLeadAndMemberEngines(members)
			manifest := parallelManifest("timeout-swarm", members, true, 2)
			manifest.Harness.MemberTimeout = 100 * time.Millisecond
			reg := swarm.NewRegistry()
			reg.Register(manifest)
			swarmCtx := swarm.NewContext(manifest.ID, manifest)
			lead.SetSwarmContext(&swarmCtx)

			stallStreamer, stallCancels := stallingStreamer()
			siblingStreamer, siblingCancels := stallingStreamer()
			streamers := map[string]streaming.Streamer{
				"stalls":  stallStreamer,
				"sibling": siblingStreamer,
			}

			delegateTool := engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "lead").
				WithStreamers(streamers).
				WithSwarmRegistry(reg)

			start := time.Now()
			err := delegateTool.DispatchSwarmMembers(context.Background(), &swarmCtx, members, "go")
			elapsed := time.Since(start)

			Expect(err).To(HaveOccurred(), "stalled member must surface as an error, not a forever-hang")
			Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue(),
				"expected DeadlineExceeded in the error chain; got %v", err)
			Expect(elapsed).To(BeNumerically("<", 5*time.Second),
				"with MemberTimeout=100ms the dispatch must unwind well before any default; got %s", elapsed)

			// Both streamer goroutines must observe their per-call ctx
			// firing — the stalled member from its own deadline, the
			// sibling from dispatchParallel's first-error cancel cascade.
			Eventually(stallCancels, "1s").Should(Receive())
			Eventually(siblingCancels, "1s").Should(Receive())
		})

		It("does not fire when MemberTimeout is zero (the default) and the streamer completes", func() {
			members := []string{"alpha"}
			lead, engines := buildLeadAndMemberEngines(members)
			manifest := parallelManifest("no-timeout-swarm", members, true, 1)
			// MemberTimeout left at zero — backwards-compatible default.
			reg := swarm.NewRegistry()
			reg.Register(manifest)
			swarmCtx := swarm.NewContext(manifest.ID, manifest)
			lead.SetSwarmContext(&swarmCtx)

			probe := newProbe()
			streamers := map[string]streaming.Streamer{
				"alpha": boundedHoldStreamer(probe, 25*time.Millisecond),
			}
			delegateTool := engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "lead").
				WithStreamers(streamers).
				WithSwarmRegistry(reg)

			err := delegateTool.DispatchSwarmMembers(context.Background(), &swarmCtx, members, "go")

			Expect(err).NotTo(HaveOccurred(),
				"zero MemberTimeout must preserve the no-deadline contract")
		})
	})

	// Bug C — Swarm-target dispatch case-fold. Forensic evidence:
	// session 7dfdb197-ce21-45a2-b5da-f2fa62dd293b at 17:57:05.214
	//
	//   swarm-target dispatch "dev-swarm" failed:
	//     no engine for swarm member "tech-Lead"
	//
	// The orchestrator delegated to a swarm whose Members[] referenced
	// `tech-Lead`, but the per-agent engines map was keyed under the
	// canonical `Tech-Lead`. The first lookup (in-swarm gate at
	// resolveTargetWithOptions, fixed by Bug 2 / 576b8156 via
	// containsAgent's strings.EqualFold) passed; the SECOND lookup
	// (d.engines[memberID] at delegation.go:2983 inside buildMemberRunner)
	// is a raw map access that's case-sensitive — so the dispatch
	// surfaces "no engine for swarm member" despite the member being
	// registered under a case-variant of the same id.
	//
	// Pin the case-fold contract end-to-end:
	//   - canonical-registered Tech-Lead resolves under member id
	//     `tech-Lead`, `TECH-LEAD`, and `tech-lead`.
	//   - canonical-registered Senior-Engineer resolves under
	//     `senior-engineer` and `Senior-engineer`.
	//   - completely unknown ids still error with the existing
	//     "no engine for swarm member" sentinel.
	Context("Bug C — case-insensitive engine resolution under swarm-target dispatch", func() {
		It("resolves Tech-Lead engine when the swarm member id case-differs (tech-Lead)", func() {
			// Engines registered under canonical case.
			lead, engines := buildLeadAndMemberEngines([]string{"Tech-Lead"})

			// Manifest Members[] uses the case-variant the orchestrator
			// actually typed in the forensic session.
			memberAsCalled := "tech-Lead"
			manifest := parallelManifest("dev-swarm", []string{memberAsCalled}, false, 0)
			reg := swarm.NewRegistry()
			reg.Register(manifest)
			swarmCtx := swarm.NewContext(manifest.ID, manifest)
			lead.SetSwarmContext(&swarmCtx)

			streamers := map[string]streaming.Streamer{
				"Tech-Lead": trivialStreamer(nil),
			}

			delegateTool := engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "lead").
				WithStreamers(streamers).
				WithSwarmRegistry(reg)

			err := delegateTool.DispatchSwarmMembers(
				context.Background(), &swarmCtx, []string{memberAsCalled}, "go")

			Expect(err).NotTo(HaveOccurred(),
				"buildMemberRunner must case-fold d.engines lookup so a case-variant member id ("+memberAsCalled+") resolves to the canonical engine (Tech-Lead) — mirrors Bug 2 / containsAgent's strings.EqualFold contract")
		})

		It("resolves the canonical engine across multiple case variants", func() {
			// Multiple canonical-cased engines registered.
			lead, engines := buildLeadAndMemberEngines([]string{"Tech-Lead", "Senior-Engineer"})

			cases := []struct {
				name          string
				memberAsTyped string
			}{
				{"upper-case member id", "TECH-LEAD"},
				{"all-lower member id", "tech-lead"},
				{"mixed-case Senior-Engineer", "Senior-engineer"},
				{"all-lower Senior-Engineer", "senior-engineer"},
			}

			for _, tc := range cases {
				By(tc.name)
				manifest := parallelManifest("dev-swarm-"+tc.memberAsTyped, []string{tc.memberAsTyped}, false, 0)
				reg := swarm.NewRegistry()
				reg.Register(manifest)
				swarmCtx := swarm.NewContext(manifest.ID, manifest)
				lead.SetSwarmContext(&swarmCtx)

				streamers := map[string]streaming.Streamer{
					"Tech-Lead":       trivialStreamer(nil),
					"Senior-Engineer": trivialStreamer(nil),
				}

				delegateTool := engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "lead").
					WithStreamers(streamers).
					WithSwarmRegistry(reg)

				err := delegateTool.DispatchSwarmMembers(
					context.Background(), &swarmCtx, []string{tc.memberAsTyped}, "go")

				Expect(err).NotTo(HaveOccurred(),
					"case variant "+tc.memberAsTyped+" must resolve to the canonical engine")
			}
		})

		It("still surfaces 'no engine for swarm member' when the target is genuinely unknown", func() {
			// Negative case — case-folding must NOT swallow real misses.
			// A completely unknown member id (no matching engine under
			// any case) must keep producing the existing sentinel error
			// so honest typos still surface to the model.
			lead, engines := buildLeadAndMemberEngines([]string{"Tech-Lead"})

			unknown := "Completely-Unknown-Agent"
			manifest := parallelManifest("dev-swarm-unknown", []string{unknown}, false, 0)
			reg := swarm.NewRegistry()
			reg.Register(manifest)
			swarmCtx := swarm.NewContext(manifest.ID, manifest)
			lead.SetSwarmContext(&swarmCtx)

			delegateTool := engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "lead").
				WithSwarmRegistry(reg)

			err := delegateTool.DispatchSwarmMembers(
				context.Background(), &swarmCtx, []string{unknown}, "go")

			Expect(err).To(HaveOccurred(),
				"a member id with no canonical match under any case must still error — case-fold must not swallow real misses")
			Expect(err.Error()).To(ContainSubstring("no engine for swarm member"),
				"the sentinel error message stays unchanged so existing log/transcript filters keep working")
			Expect(err.Error()).To(ContainSubstring(unknown),
				"the rejected member id appears verbatim in the error so the model sees what it actually typed")
		})
	})
})
