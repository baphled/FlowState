package engine_test

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/coordination"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/swarm"
	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/turn"
)

// Plans/Child Session Turn Registry Plumbing (May 2026) §Item 2 +
// §Success criteria S3-S6. PR2a wires `turn.Registry` through
// `DelegateTool` so every single-target delegation
// (`DelegateTool.executeSync`) mints, advances, and terminates a child
// Turn — closing the live-UI-parity gap where the long-poll Turn
// snapshot endpoint had nothing to return for a delegate-spawned child
// mid-stream.
//
// These specs pin the executeSync side of the plumbing. The swarm-fan-
// out side (`bootstrapMemberSession` / `buildMemberRunner`) lands in
// PR2b.
//
// Behaviour pinned:
//
//   - S3.1: executeSync with a registered turnRegistry calls
//     `StartOrReuse(delegateSessionID)` after `resolveOrCreateSession`.
//   - S3.2: executeSync with a nil turnRegistry (legacy test constructor)
//     completes the delegation without touching the Turn lifecycle —
//     back-compat regression pin for the pre-plumbing call-site
//     footprint (NewDelegateTool / NewDelegateToolWithBackground at
//     dozens of sites).
//   - S3.3: the happy path calls `Complete(childTurnID, ModelInfo)`
//     with the (provider, model) pair captured from
//     `target.engine.LastProvider() / LastModel()` BEFORE
//     `closeSessionIfManaged` runs (mirrors dispatcher.go:929-936
//     terminal-then-cleanup ordering).
//   - S4.1: the dispatchErr branch calls `Fail(childTurnID, dispatchErr)`
//     so `byActiveSession` clears for the next call.
//   - S4.2 (load-bearing R2 defence): when `dispatchPostMemberGates`
//     fails AFTER the happy-path Complete fired, the `turnOwnedByWrap`
//     guard short-circuits `failChildTurnIfOwned` BEFORE
//     `turnRegistry.Fail` is invoked. The Turn's terminal Status
//     remains `StatusCompleted` (not flipped to `StatusFailed`),
//     proving Fail was never called on a terminal turn.
//   - S5.1: the runner's retry boundary calls
//     `ResetForRetry(childTurnID)` on attempts > 0 so attempt-N+1's
//     chunks do NOT pile on top of attempt-N's stale partial-stream
//     rows. Closure-internal attempt counter pattern per Plans §Item 2c
//     option (i).
//   - S5.2: first attempt (counter == 0) does NOT call ResetForRetry —
//     the empty-slice initial state is the right starting point.
//   - S6.1: after Start (mid-stream), `FindActiveBySession(childID)`
//     returns `(turnID, true)` — consumer-visible verification of the
//     load-bearing UI fix; the API server's `handleListV1Sessions`
//     projection uses this exact lookup at server.go:1216-1224.

// spyRegistry wraps a real *turn.Registry and counts calls to Fail +
// ResetForRetry so the S4.2 + S5.1 specs can assert their respective
// invariants directly. All other methods pass through to the wrapped
// registry so the production lifecycle (StartOrReuse → Complete) drives
// the underlying state changes the rest of the assertions read.
//
// Plans/Child Session Turn Registry Plumbing (May 2026) §Item 2c +
// §S4 row + §S5.1 row require a spy-based assertion that
// turnRegistry.Fail call-count is 0 on the gate-after-Complete suffix
// and that ResetForRetry call-count == (attempts - 1).
type spyRegistry struct {
	inner          *turn.Registry
	failCalls      atomic.Int32
	resetCalls     atomic.Int32
	completeCalls  atomic.Int32
	startReuseHits atomic.Int32
}

func newSpyRegistry() *spyRegistry {
	return &spyRegistry{inner: turn.NewRegistry()}
}

func (s *spyRegistry) StartOrReuse(sessionID string) (string, error) {
	s.startReuseHits.Add(1)
	return s.inner.StartOrReuse(sessionID)
}

func (s *spyRegistry) Append(turnID string, msg session.Message) error {
	return s.inner.Append(turnID, msg)
}

func (s *spyRegistry) Complete(turnID string, info turn.ModelInfo) error {
	s.completeCalls.Add(1)
	return s.inner.Complete(turnID, info)
}

func (s *spyRegistry) Fail(turnID string, cause error) error {
	s.failCalls.Add(1)
	return s.inner.Fail(turnID, cause)
}

func (s *spyRegistry) ResetForRetry(turnID string) error {
	s.resetCalls.Add(1)
	return s.inner.ResetForRetry(turnID)
}

func (s *spyRegistry) FindActiveBySession(sessionID string) (string, bool) {
	return s.inner.FindActiveBySession(sessionID)
}

var _ = Describe("DelegateTool child Turn lifecycle (executeSync)", func() {
	var (
		qaProvider *mockProvider
		qaEngine   *engine.Engine
		engines    map[string]*engine.Engine
		delegation agent.Delegation
		mgr        *session.Manager
	)

	BeforeEach(func() {
		qaProvider = &mockProvider{
			name: "qa-provider",
			streamChunks: []provider.StreamChunk{
				{Content: "lifecycle response", Done: true},
			},
		}
		qaManifest := agent.Manifest{
			ID:                "qa-agent",
			Name:              "QA Agent",
			Instructions:      agent.Instructions{SystemPrompt: "You are QA."},
			ContextManagement: agent.DefaultContextManagement(),
		}
		qaEngine = engine.New(engine.Config{
			ChatProvider: qaProvider,
			Manifest:     qaManifest,
		})
		engines = map[string]*engine.Engine{"qa-agent": qaEngine}
		delegation = agent.Delegation{CanDelegate: true}
		mgr = session.NewManager(nil)
	})

	Context("S3.1 — turnRegistry registered, single-target dispatch", func() {
		It("starts a child Turn via StartOrReuse(delegateSessionID) after resolveOrCreateSession", func() {
			parent, err := mgr.CreateSession("orchestrator")
			Expect(err).NotTo(HaveOccurred())

			spy := newSpyRegistry()
			delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator").
				WithSessionManager(mgr).
				WithChildTurnRegistryForTest(spy)

			ctx := context.WithValue(context.Background(), session.IDKey{}, parent.ID)
			input := tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "qa-agent",
					"message":       "Run the tests",
				},
			}
			result, err := delegateTool.Execute(ctx, input)
			Expect(err).NotTo(HaveOccurred())

			childID, ok := result.Metadata["sessionId"].(string)
			Expect(ok).To(BeTrue())
			Expect(childID).NotTo(BeEmpty())

			// executeSync must mint the child Turn via StartOrReuse
			// (NOT Start) — the D1 primitive choice. The spy counts
			// StartOrReuse calls directly; with the lifecycle wired
			// on the single-target path, exactly one call fires per
			// executeSync invocation.
			Expect(spy.startReuseHits.Load()).To(Equal(int32(1)),
				"executeSync must mint exactly one child Turn via StartOrReuse on a single-target dispatch — the canonical S3.1 lifecycle pin")

			// And the happy path must Complete on its own (no
			// dispatchErr, no gate failure).
			Expect(spy.completeCalls.Load()).To(Equal(int32(1)),
				"executeSync's happy path must call Complete exactly once on a single-target dispatch")
			Expect(spy.failCalls.Load()).To(Equal(int32(0)),
				"executeSync's happy path must NOT call Fail")

			// And byActiveSession cleared by Complete — the
			// long-poll handle is observable on the next Start.
			_, present := spy.FindActiveBySession(childID)
			Expect(present).To(BeFalse(),
				"Complete must clear byActiveSession so the next StartOrReuse mints fresh without a stale-entry conflict")
		})
	})

	Context("S3.2 — turnRegistry nil (legacy test constructor)", func() {
		It("completes the delegation without panicking and without minting a Turn", func() {
			parent, err := mgr.CreateSession("orchestrator")
			Expect(err).NotTo(HaveOccurred())

			// No WithTurnRegistry — the historical no-Turn-channel
			// path. Every Turn lifecycle site short-circuits on
			// d.turnRegistry == nil per D7 — back-compat regression
			// pin for the dozens of NewDelegateTool /
			// NewDelegateToolWithBackground callsites that pre-date
			// the plumbing.
			delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator").
				WithSessionManager(mgr)

			ctx := context.WithValue(context.Background(), session.IDKey{}, parent.ID)
			input := tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "qa-agent",
					"message":       "Legacy path",
				},
			}
			result, err := delegateTool.Execute(ctx, input)
			Expect(err).NotTo(HaveOccurred(),
				"a nil turnRegistry must NOT cause delegation to fail — every Turn lifecycle site short-circuits silently per D7")

			childID, ok := result.Metadata["sessionId"].(string)
			Expect(ok).To(BeTrue())
			Expect(childID).NotTo(BeEmpty(),
				"delegation must still succeed and surface the child session id even with the live channel disabled")
		})
	})

	Context("S3.3 — happy path Complete with (provider, model) before closeSessionIfManaged", func() {
		It("completes the child Turn exactly once on the happy path", func() {
			parent, err := mgr.CreateSession("orchestrator")
			Expect(err).NotTo(HaveOccurred())

			spy := newSpyRegistry()
			delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator").
				WithSessionManager(mgr).
				WithChildTurnRegistryForTest(spy)

			ctx := context.WithValue(context.Background(), session.IDKey{}, parent.ID)
			input := tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "qa-agent",
					"message":       "Complete me",
				},
			}
			_, err = delegateTool.Execute(ctx, input)
			Expect(err).NotTo(HaveOccurred())

			Expect(spy.completeCalls.Load()).To(Equal(int32(1)),
				"the happy path must call Complete exactly once")
			Expect(spy.failCalls.Load()).To(Equal(int32(0)),
				"the happy path must NOT call Fail")
		})
	})

	Context("S4.1 — dispatchErr branch failure", func() {
		It("fails the child Turn exactly once on the dispatchErr branch", func() {
			failingProvider := &mockProvider{
				name:      "failing-provider",
				streamErr: errors.New("stream init failed"),
			}
			failingEngine := engine.New(engine.Config{
				ChatProvider: failingProvider,
				Manifest: agent.Manifest{
					ID:                "failing-agent",
					Name:              "Failing Agent",
					Instructions:      agent.Instructions{SystemPrompt: "fail"},
					ContextManagement: agent.DefaultContextManagement(),
				},
			})
			failingEngines := map[string]*engine.Engine{"failing-agent": failingEngine}

			parent, err := mgr.CreateSession("orchestrator")
			Expect(err).NotTo(HaveOccurred())

			spy := newSpyRegistry()
			delegateTool := engine.NewDelegateTool(failingEngines, delegation, "orchestrator").
				WithSessionManager(mgr).
				WithChildTurnRegistryForTest(spy)

			ctx := context.WithValue(context.Background(), session.IDKey{}, parent.ID)
			input := tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "failing-agent",
					"message":       "this will fail",
				},
			}
			_, err = delegateTool.Execute(ctx, input)
			Expect(err).To(HaveOccurred())

			Expect(spy.failCalls.Load()).To(Equal(int32(1)),
				"the dispatchErr branch MUST call Fail exactly once — without this, byActiveSession carries a stale entry and the next StartOrReuse would weaken to a no-op auto-complete on a non-terminal prior")
			Expect(spy.completeCalls.Load()).To(Equal(int32(0)),
				"the dispatchErr branch must NOT call Complete")
		})
	})

	Context("S4.2 — gate-failure suffix after happy-path Complete (R2 load-bearing defence)", func() {
		It("does NOT invoke turnRegistry.Fail when dispatchPostMemberGates errors after Complete", func() {
			// Drive a swarm-gate failure on the post-member hook:
			// the reviewer pattern (swarm_gate_test.go:158-189)
			// writes a malformed verdict into the coord store, so
			// the post-member-plan-reviewer-result-schema gate
			// halts AFTER the streamer drains cleanly (Complete
			// fired) and BEFORE executeSync returns. This is the
			// canonical "gate fails after Complete" surface; the
			// turnOwnedByWrap guard must short-circuit
			// failChildTurnIfOwned so turnRegistry.Fail is NEVER
			// invoked on the terminal Turn. Verified via a
			// spy-registry that counts Fail calls directly — the
			// load-bearing R2 defence per B5 resolution.

			swarm.ClearSchemasForTest()
			Expect(swarm.SeedDefaultSchemas()).To(Succeed())

			engines, _ := reviewerEngines()
			store := coordination.NewMemoryStore()
			Expect(store.Set("planning/plan-reviewer/review", []byte(`{"reasoning":"missing verdict"}`))).To(Succeed())

			parent, err := mgr.CreateSession("planner")
			Expect(err).NotTo(HaveOccurred())

			multi := swarm.NewMultiRunner()
			multi.Register("builtin:result-schema", swarm.NewResultSchemaRunner())
			spy := newSpyRegistry()
			delegateTool := engine.NewDelegateToolWithBackground(
				engines,
				agent.Delegation{CanDelegate: true},
				"planner",
				nil,
				store,
			).
				WithSessionManager(mgr).
				WithGateRunner(multi).
				WithChildTurnRegistryForTest(spy)

			ctx := context.WithValue(context.Background(), session.IDKey{}, parent.ID)
			_, err = delegateTool.Execute(ctx, reviewerDelegateInput())
			Expect(err).To(HaveOccurred())
			var gateErr *swarm.GateError
			Expect(errors.As(err, &gateErr)).To(BeTrue(),
				"the test drives the post-member-result-schema halt that fires AFTER the streamer Completes; the surface is the canonical gate-after-Complete suffix")

			// Load-bearing R2 defence: turnOwnedByWrap = true must
			// short-circuit failChildTurnIfOwned BEFORE the spy's
			// Fail method is invoked. Call-count must be EXACTLY 0
			// on this suffix. Any future refactor that drops the
			// flag (e.g. relying only on the Fail-side terminal
			// silent-swallow) flips this to 1 and the spec fails
			// loudly — the regression pin against the
			// "author-as-only-reviewer + behaviour-pin-in-same-
			// commit" failure mode per
			// project_flowstate_review_pattern_failure.
			Expect(spy.failCalls.Load()).To(Equal(int32(0)),
				"failChildTurnIfOwned MUST short-circuit on turnOwnedByWrap=true; turnRegistry.Fail call-count must remain 0 across the gate-after-Complete suffix (B5 load-bearing R2 defence)")

			// Sanity: the happy-path Complete did fire.
			Expect(spy.completeCalls.Load()).To(Equal(int32(1)),
				"the happy-path branch must call Complete exactly once before the gate-after-Complete suffix runs")

			// And StartOrReuse fired exactly once for the single-
			// target dispatch.
			Expect(spy.startReuseHits.Load()).To(Equal(int32(1)),
				"executeSync must mint exactly one child Turn via StartOrReuse on the single-target path")
		})
	})

	Context("S5.1 — retry boundary calls ResetForRetry on attempt > 0", func() {
		It("invokes ResetForRetry exactly once per retry boundary (attempts-1 times)", func() {
			// Drive a streamer that fails CategoryRetryable on
			// attempt 1 and succeeds on attempt 2. The runner's
			// retry policy (MaxAttempts=3) re-invokes the closure;
			// at the closure entry for attempt 2, the
			// attempt-counter increment from 0→1 triggers
			// ResetForRetry exactly once. ResetForRetry call-count
			// across the dispatch should equal (totalAttempts - 1)
			// — 1 reset between attempts 1 and 2 (no reset for
			// attempt 1 because counter == 0).
			lead, _, leadEngines := buildLeadAndTargetEngines()
			manifest := noJitterRetryManifest("retry-reset-swarm")
			swarmReg := installSwarmCtxOnEngine(lead, manifest)

			calls := &atomic.Int32{}
			streamers := map[string]streaming.Streamer{
				"qa-agent": retryStreamerWith(1, retryableSwarmErr(), calls),
			}

			spy := newSpyRegistry()
			delegateTool := engine.NewDelegateTool(leadEngines, agent.Delegation{CanDelegate: true}, "lead").
				WithStreamers(streamers).
				WithSwarmRegistry(swarmReg).
				WithSessionManager(mgr).
				WithChildTurnRegistryForTest(spy)

			parent, err := mgr.CreateSession("lead")
			Expect(err).NotTo(HaveOccurred())

			ctx := context.WithValue(context.Background(), session.IDKey{}, parent.ID)
			input := tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "qa-agent",
					"message":       "retry me",
				},
			}
			_, err = delegateTool.Execute(ctx, input)
			Expect(err).NotTo(HaveOccurred())
			Expect(calls.Load()).To(Equal(int32(2)),
				"the retry policy must drive exactly 2 attempts (attempt 1 retryable-failed, attempt 2 succeeded)")

			// Closure-internal attempt counter pattern: attempt 0
			// (first call) does NOT invoke ResetForRetry; attempt
			// 1 (the retry) does. Call-count is exactly 1 for the
			// 2-attempt scenario. This is the load-bearing
			// assertion the PR1 disclosure highlighted — without
			// ResetForRetry, attempt-1's accumulator chunks would
			// pile on top of attempt-2's because provider-side
			// message IDs are not generally stable across retries
			// (D5).
			Expect(spy.resetCalls.Load()).To(Equal(int32(1)),
				"closure-internal attempt counter must invoke ResetForRetry exactly once between attempts 1 and 2 (Plans/Child Session Turn Registry Plumbing §Item 2c, PR1 disclosure option (i))")

			// Sanity: exactly one Complete and zero Fail calls on
			// the eventual-success path.
			Expect(spy.completeCalls.Load()).To(Equal(int32(1)),
				"a retry that eventually succeeds must Complete exactly once")
			Expect(spy.failCalls.Load()).To(Equal(int32(0)),
				"a retry that eventually succeeds must NOT call Fail (the CategoryRetryable surface is per-attempt, not per-dispatch)")
		})
	})

	Context("S5.2 — first attempt does NOT call ResetForRetry", func() {
		It("does not invoke ResetForRetry when the first attempt succeeds (counter == 0 guard)", func() {
			// The closure-internal `attempt` counter starts at 0;
			// the `if attempt > 0` guard short-circuits
			// ResetForRetry on the first attempt. Spy verifies
			// call-count is exactly 0 across a happy-path
			// single-attempt dispatch.
			lead, _, leadEngines := buildLeadAndTargetEngines()
			manifest := noJitterRetryManifest("first-attempt-swarm")
			swarmReg := installSwarmCtxOnEngine(lead, manifest)

			calls := &atomic.Int32{}
			streamers := map[string]streaming.Streamer{
				"qa-agent": retryStreamerWith(0, nil, calls), // 0 failures = succeed on attempt 1
			}

			spy := newSpyRegistry()
			delegateTool := engine.NewDelegateTool(leadEngines, agent.Delegation{CanDelegate: true}, "lead").
				WithStreamers(streamers).
				WithSwarmRegistry(swarmReg).
				WithSessionManager(mgr).
				WithChildTurnRegistryForTest(spy)

			parent, err := mgr.CreateSession("lead")
			Expect(err).NotTo(HaveOccurred())

			ctx := context.WithValue(context.Background(), session.IDKey{}, parent.ID)
			input := tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "qa-agent",
					"message":       "first attempt wins",
				},
			}
			_, err = delegateTool.Execute(ctx, input)
			Expect(err).NotTo(HaveOccurred())
			Expect(calls.Load()).To(Equal(int32(1)),
				"the happy first attempt must drive exactly one Stream call")

			Expect(spy.resetCalls.Load()).To(Equal(int32(0)),
				"the `if attempt > 0` guard MUST short-circuit ResetForRetry on attempt 0; call-count must be exactly 0 across a single-attempt dispatch")
		})
	})

	Context("S6.1 — consumer-visible verification via FindActiveBySession", func() {
		It("makes a child session's active Turn observable to long-poll while the stream is in flight", func() {
			// Drive a slow streamer so the test can observe the
			// child's active Turn mid-stream. The streamer emits
			// after a delay, giving us a window where
			// FindActiveBySession should report (turnID, true).
			slowStream := make(chan provider.StreamChunk, 1)
			slowProvider := &controllableStreamProvider{
				name:   "slow-provider",
				stream: slowStream,
			}
			slowEngine := engine.New(engine.Config{
				ChatProvider: slowProvider,
				Manifest: agent.Manifest{
					ID:                "slow-agent",
					Name:              "Slow Agent",
					Instructions:      agent.Instructions{SystemPrompt: "slow"},
					ContextManagement: agent.DefaultContextManagement(),
				},
			})
			slowEngines := map[string]*engine.Engine{"slow-agent": slowEngine}

			parent, err := mgr.CreateSession("orchestrator")
			Expect(err).NotTo(HaveOccurred())

			spy := newSpyRegistry()
			delegateTool := engine.NewDelegateTool(slowEngines, delegation, "orchestrator").
				WithSessionManager(mgr).
				WithChildTurnRegistryForTest(spy)

			done := make(chan struct{})
			go func() {
				defer close(done)
				ctx := context.WithValue(context.Background(), session.IDKey{}, parent.ID)
				_, _ = delegateTool.Execute(ctx, tool.Input{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"subagent_type": "slow-agent",
						"message":       "slowly please",
					},
				})
			}()

			// Eventually a child session appears AND its Turn is
			// observable to FindActiveBySession — the API server's
			// projection at server.go:1216-1224 and the load-bearing
			// UI fix.
			Eventually(func() bool {
				children, listErr := mgr.ChildSessions(parent.ID)
				if listErr != nil || len(children) == 0 {
					return false
				}
				_, present := spy.FindActiveBySession(children[0].ID)
				return present
			}, 2*time.Second, 10*time.Millisecond).Should(BeTrue(),
				"FindActiveBySession(childID) must return (turnID, true) while the child stream is in flight — this is the API server's projection at server.go:1216-1224 and the load-bearing UI fix")

			// Unblock the stream so the goroutine exits cleanly.
			slowStream <- provider.StreamChunk{Content: "released", Done: true}
			close(slowStream)
			Eventually(done, 2*time.Second).Should(BeClosed())
		})
	})
})

// controllableStreamProvider is a minimal Provider whose Stream call
// returns a channel the test owns. Lets S6.1 observe the child's Turn
// while the stream is paused, then release the stream and let
// executeSync's Complete run.
type controllableStreamProvider struct {
	name   string
	stream chan provider.StreamChunk
}

func (p *controllableStreamProvider) Name() string { return p.name }

func (p *controllableStreamProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	return p.stream, nil
}

func (p *controllableStreamProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

func (p *controllableStreamProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, nil
}

func (p *controllableStreamProvider) Models() ([]provider.Model, error) {
	return nil, nil
}
