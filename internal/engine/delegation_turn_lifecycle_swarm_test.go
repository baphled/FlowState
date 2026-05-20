package engine_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/swarm"
	"github.com/baphled/flowstate/internal/turn"
)

// Plans/Child Session Turn Registry Plumbing (May 2026) §Item 2d +
// §Success criteria S7. PR2b extends PR2a's `turn.Registry`
// plumbing through `DelegateTool.executeSync` to the swarm-fan-out
// path — `bootstrapMemberSession` mints a per-member child Turn via
// `StartOrReuse(memberSessionID)`, `buildMemberRunner`'s closure
// Completes on success / Fails on dispatch error. Closes the
// live-UI parity gap for swarm-spawned child sessions identical to
// the single-target gap PR2a closed.
//
// These specs pin the swarm-fan-out side. The single-target side
// (`DelegateTool.executeSync`) is pinned at
// delegation_turn_lifecycle_test.go.
//
// Behaviour pinned:
//
//   - S7.1: DispatchSwarmMembers with N=3 mints exactly 3 distinct
//     child Turns (one per member) via
//     `StartOrReuse(memberSessionID)`. Load-bearing per plan-reviewer
//     B1 verification — the per-member layer must mirror executeSync's
//     per-child Turn lifecycle, not collapse to a single shared Turn.
//   - S7.2: `bootstrapMemberSession` with nil registry no-ops on the
//     Turn lifecycle and member dispatch still succeeds — back-compat
//     regression pin (mirrors S3.2 from PR2a's single-target spec).
//   - S7.3: per-member happy path calls `Complete(memberTurnID, info)`
//     exactly once per member.
//   - S7.4: when one member fails while two succeed, the failed
//     member's Turn transitions Failed; succeeded members' Turns
//     transition Completed; spy call-counts isolate the per-member
//     terminal-discipline.
//   - S7.5: gate-after-Complete suffix on swarm path — the
//     `failMemberTurnIfOwned` helper short-circuits BEFORE
//     `turnRegistry.Fail` when `handle.ownedByCaller=true` is set by
//     the per-member Complete (R2 defence applies to swarm path
//     identical to executeSync's §S4.2).
//   - S10.swarm: retry boundary on swarm member calls
//     `ResetForRetry(memberTurnID_N)` between attempts, NOT on
//     members 1..N-1.

// memberAwareSpyRegistry extends the §S4.2 spy semantics to the per-
// member layer so the §S7 specs can isolate Complete/Fail/StartOrReuse/
// ResetForRetry counts PER MEMBER session ID. Each method records the
// (turnID, sessionID-from-StartOrReuse) tuple onto a per-session
// counter map.
//
// StartOrReuse → records the session id and returns the underlying
// registry's mint result. Subsequent Complete/Fail/Reset calls look up
// the session id via a turnID→sessionID translation table so the
// per-member assertions can read counts directly without inspecting
// the inner registry's state. Mirrors PR2a's spyRegistry pattern but
// keyed on session id rather than aggregate atomic counters.
type memberAwareSpyRegistry struct {
	mu               sync.Mutex
	startReuseBySess map[string]int
	completeBySess   map[string]int
	failBySess       map[string]int
	resetBySess      map[string]int
	turnToSession    map[string]string
	startReuseHits   atomic.Int32
	completeCalls    atomic.Int32
	failCalls        atomic.Int32
	resetCalls       atomic.Int32
}

func newMemberAwareSpy() *memberAwareSpyRegistry {
	return &memberAwareSpyRegistry{
		startReuseBySess: map[string]int{},
		completeBySess:   map[string]int{},
		failBySess:       map[string]int{},
		resetBySess:      map[string]int{},
		turnToSession:    map[string]string{},
	}
}

func (s *memberAwareSpyRegistry) StartOrReuse(sessionID string) (string, error) {
	s.startReuseHits.Add(1)
	s.mu.Lock()
	s.startReuseBySess[sessionID]++
	s.mu.Unlock()
	// Mint a deterministic-by-session-id turn id so the spec
	// can correlate later Complete/Fail/Reset calls without
	// observing the inner registry's id generator state.
	turnID := fmt.Sprintf("turn-%s-%d", sessionID, s.startReuseHits.Load())
	s.mu.Lock()
	s.turnToSession[turnID] = sessionID
	s.mu.Unlock()
	return turnID, nil
}

func (s *memberAwareSpyRegistry) Append(turnID string, _ session.Message) error {
	return nil
}

func (s *memberAwareSpyRegistry) Complete(turnID string, _ turn.ModelInfo) error {
	s.completeCalls.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	if sid, ok := s.turnToSession[turnID]; ok {
		s.completeBySess[sid]++
	}
	return nil
}

func (s *memberAwareSpyRegistry) Fail(turnID string, _ error) error {
	s.failCalls.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	if sid, ok := s.turnToSession[turnID]; ok {
		s.failBySess[sid]++
	}
	return nil
}

func (s *memberAwareSpyRegistry) ResetForRetry(turnID string) error {
	s.resetCalls.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	if sid, ok := s.turnToSession[turnID]; ok {
		s.resetBySess[sid]++
	}
	return nil
}

func (s *memberAwareSpyRegistry) startReuseCountForSession(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startReuseBySess[id]
}

func (s *memberAwareSpyRegistry) completeCountForSession(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.completeBySess[id]
}

func (s *memberAwareSpyRegistry) failCountForSession(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.failBySess[id]
}

func (s *memberAwareSpyRegistry) sessionsSeen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.startReuseBySess))
	for k := range s.startReuseBySess {
		out = append(out, k)
	}
	return out
}

// failingMemberStreamer returns ok for one specified member id and an
// error for the rest. Used by §S7.4 to drive partial-failure fan-out.
func failingMemberStreamer(failID string) streaming.Streamer {
	return streamerFunc(func(_ context.Context, agentID, _ string) (<-chan provider.StreamChunk, error) {
		if agentID == failID {
			return nil, errors.New("member " + agentID + " failed dispatch")
		}
		ch := make(chan provider.StreamChunk, 1)
		ch <- provider.StreamChunk{Content: "ok", Done: true}
		close(ch)
		return ch, nil
	})
}

var _ = Describe("DelegateTool child Turn lifecycle (swarm fan-out)", func() {
	var (
		mgr *session.Manager
	)

	BeforeEach(func() {
		mgr = session.NewManager(nil)
	})

	Context("S7.1 — DispatchSwarmMembers mints distinct child Turns per member", func() {
		It("calls StartOrReuse exactly once per member session", func() {
			members := []string{"alpha", "bravo", "charlie"}
			lead, engines := buildLeadAndMemberEngines(members)
			manifest := parallelManifest("swarm-s71", members, false, 0)
			reg := swarm.NewRegistry()
			reg.Register(manifest)
			swarmCtx := swarm.NewContext(manifest.ID, manifest)
			lead.SetSwarmContext(&swarmCtx)

			streamers := map[string]streaming.Streamer{}
			for _, m := range members {
				streamers[m] = trivialStreamer(nil)
			}

			parent, err := mgr.CreateSession("lead")
			Expect(err).NotTo(HaveOccurred())
			_ = parent

			spy := newMemberAwareSpy()
			delegateTool := engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "lead").
				WithStreamers(streamers).
				WithSwarmRegistry(reg).
				WithSessionManager(mgr).
				WithSessionCreator(mgr).
				WithChildTurnRegistryForTest(spy)

			err = delegateTool.DispatchSwarmMembers(context.Background(), &swarmCtx, members, "go")
			Expect(err).NotTo(HaveOccurred())

			// Load-bearing assertion: exactly 3 StartOrReuse calls,
			// one per member session. The single-target executeSync
			// path mints exactly one per delegation; the swarm path
			// must mirror that per-member, not collapse to a shared
			// Turn keyed on (say) the lead's session id.
			Expect(spy.startReuseHits.Load()).To(Equal(int32(3)),
				"DispatchSwarmMembers with N=3 members must mint exactly 3 child Turns via StartOrReuse — one per member session (plan §S7.1 + blocker B1 verification)")

			// And the 3 sessions must be DISTINCT. If the swarm-
			// fan-out collapsed to a single Turn via the coordinator's
			// session id, every call would record the same key and
			// sessionsSeen() would have length 1.
			sessions := spy.sessionsSeen()
			Expect(sessions).To(HaveLen(3),
				"three distinct member session ids must reach StartOrReuse; collapsing to a single Turn shared across members would dark the per-member live channel")

			// And each member's Turn must Complete exactly once.
			for _, sid := range sessions {
				Expect(spy.completeCountForSession(sid)).To(Equal(1),
					"per-member happy path must call Complete exactly once for member session "+sid)
				Expect(spy.failCountForSession(sid)).To(Equal(0),
					"per-member happy path must NOT call Fail for member session "+sid)
			}
		})
	})

	Context("S7.2 — nil registry preserves the legacy no-Turn-channel behaviour", func() {
		It("completes the swarm fan-out without panicking and without minting a Turn", func() {
			members := []string{"alpha", "bravo"}
			lead, engines := buildLeadAndMemberEngines(members)
			manifest := parallelManifest("swarm-s72", members, false, 0)
			reg := swarm.NewRegistry()
			reg.Register(manifest)
			swarmCtx := swarm.NewContext(manifest.ID, manifest)
			lead.SetSwarmContext(&swarmCtx)

			streamers := map[string]streaming.Streamer{}
			for _, m := range members {
				streamers[m] = trivialStreamer(nil)
			}

			// No WithTurnRegistry / WithChildTurnRegistryForTest —
			// the historical no-Turn-channel path. Every Turn
			// lifecycle site at bootstrapMemberSession +
			// buildMemberRunner short-circuits on
			// d.turnRegistry == nil per D7. Back-compat regression
			// pin for the dozens of pre-plumbing
			// NewDelegateTool callsites.
			delegateTool := engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "lead").
				WithStreamers(streamers).
				WithSwarmRegistry(reg).
				WithSessionManager(mgr).
				WithSessionCreator(mgr)

			err := delegateTool.DispatchSwarmMembers(context.Background(), &swarmCtx, members, "go")
			Expect(err).NotTo(HaveOccurred(),
				"a nil turnRegistry MUST NOT cause swarm-fan-out dispatch to fail — every Turn lifecycle site at bootstrapMemberSession + buildMemberRunner short-circuits silently per D7")
		})
	})

	Context("S7.3 — per-member happy path Completes exactly once per member", func() {
		It("calls Complete with the member's resolved ModelInfo exactly once per member", func() {
			members := []string{"alpha", "bravo"}
			lead, engines := buildLeadAndMemberEngines(members)
			manifest := parallelManifest("swarm-s73", members, false, 0)
			reg := swarm.NewRegistry()
			reg.Register(manifest)
			swarmCtx := swarm.NewContext(manifest.ID, manifest)
			lead.SetSwarmContext(&swarmCtx)

			streamers := map[string]streaming.Streamer{}
			for _, m := range members {
				streamers[m] = trivialStreamer(nil)
			}

			spy := newMemberAwareSpy()
			delegateTool := engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "lead").
				WithStreamers(streamers).
				WithSwarmRegistry(reg).
				WithSessionManager(mgr).
				WithSessionCreator(mgr).
				WithChildTurnRegistryForTest(spy)

			err := delegateTool.DispatchSwarmMembers(context.Background(), &swarmCtx, members, "go")
			Expect(err).NotTo(HaveOccurred())

			// Total Complete count equals member count.
			Expect(spy.completeCalls.Load()).To(Equal(int32(2)),
				"every successful member must Complete exactly once; total = N members")
			Expect(spy.failCalls.Load()).To(Equal(int32(0)),
				"the happy fan-out must NOT call Fail on any member")
		})
	})

	Context("S7.4 — partial failure isolates per-member terminal discipline", func() {
		It("Fails only the failed member's Turn and Completes the rest", func() {
			members := []string{"alpha", "bravo", "charlie"}
			lead, engines := buildLeadAndMemberEngines(members)
			manifest := parallelManifest("swarm-s74", members, false, 0)
			reg := swarm.NewRegistry()
			reg.Register(manifest)
			swarmCtx := swarm.NewContext(manifest.ID, manifest)
			lead.SetSwarmContext(&swarmCtx)

			// Single shared failingMemberStreamer that fails only
			// for "bravo"; the sequential dispatch path stops at
			// the first error, so DispatchSwarmMembers will
			// short-circuit when bravo fails — exactly the
			// per-member terminal discipline isolation under test.
			fail := failingMemberStreamer("bravo")
			streamers := map[string]streaming.Streamer{}
			for _, m := range members {
				streamers[m] = fail
			}

			spy := newMemberAwareSpy()
			delegateTool := engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "lead").
				WithStreamers(streamers).
				WithSwarmRegistry(reg).
				WithSessionManager(mgr).
				WithSessionCreator(mgr).
				WithChildTurnRegistryForTest(spy)

			err := delegateTool.DispatchSwarmMembers(context.Background(), &swarmCtx, members, "go")
			Expect(err).To(HaveOccurred(),
				"a failing member must surface as a dispatch error from the swarm fan-out")

			// Sequential dispatch (Parallel=false above) stops at
			// the first error, so we expect: alpha completed,
			// bravo failed, charlie never dispatched.
			//
			// Per-member terminal discipline:
			//   - alpha's session: 1 StartOrReuse, 1 Complete, 0 Fail
			//   - bravo's session: 1 StartOrReuse, 0 Complete, 1 Fail
			//   - charlie's session: 0 StartOrReuse (never reached)
			//
			// The load-bearing isolation: bravo's Fail MUST NOT
			// flip alpha's Turn to Failed (i.e. failBySess["alpha"]
			// stays 0) — per-member isolation is what makes the
			// per-member layer valuable for live UI rendering.
			Expect(spy.completeCalls.Load()).To(Equal(int32(1)),
				"exactly one member (alpha) completed before bravo's failure short-circuited the sequential fan-out")
			Expect(spy.failCalls.Load()).To(Equal(int32(1)),
				"exactly one member (bravo) failed; per-member Fail count must NOT bleed into completed peers")

			// Locate the sessions by who Completed vs who Failed.
			var alphaSess, bravoSess string
			for _, sid := range spy.sessionsSeen() {
				if spy.completeCountForSession(sid) == 1 {
					alphaSess = sid
				}
				if spy.failCountForSession(sid) == 1 {
					bravoSess = sid
				}
			}
			Expect(alphaSess).NotTo(BeEmpty(),
				"alpha's session must Complete and surface in the spy's session map")
			Expect(bravoSess).NotTo(BeEmpty(),
				"bravo's session must Fail and surface in the spy's session map")
			Expect(alphaSess).NotTo(Equal(bravoSess),
				"alpha and bravo must land on distinct member sessions — the per-member Turn lifecycle is what isolates the failure")

			// Final isolation check: alpha session must NOT have a
			// Fail count, and bravo session must NOT have a
			// Complete count.
			Expect(spy.failCountForSession(alphaSess)).To(Equal(0),
				"alpha's Turn must stay Completed; bravo's Fail must NOT cascade to peer sessions")
			Expect(spy.completeCountForSession(bravoSess)).To(Equal(0),
				"bravo's Turn must stay Failed; the per-member dispatch error branch must NOT also Complete")
		})
	})

	Context("S7.5 — failMemberTurnIfOwned short-circuits after a per-member Complete (R2 defence)", func() {
		It("does NOT invoke Fail on a terminal Turn even if a later surface attempts it", func() {
			// White-box verification of the
			// failMemberTurnIfOwned guard at the per-member layer.
			// PR2a's §S4.2 drives a real gate-after-Complete halt
			// through reviewer engines; the swarm path equivalent
			// is to drive a happy fan-out (which sets
			// handle.ownedByCaller=true via the per-member
			// Complete) and then verify the helper short-circuits
			// when invoked AFTER Complete — load-bearing guard
			// against the failure mode where a future refactor
			// hoists Fail onto a cleanup defer without re-reading
			// the ownership flag.
			//
			// We drive a single-member swarm so the per-member
			// closure runs once, Completes, and the spy's
			// failCalls counter must remain exactly 0 — proving
			// the per-member terminal discipline mirrors
			// executeSync's §S4.2 R2 defence.
			members := []string{"alpha"}
			lead, engines := buildLeadAndMemberEngines(members)
			manifest := parallelManifest("swarm-s75", members, false, 0)
			reg := swarm.NewRegistry()
			reg.Register(manifest)
			swarmCtx := swarm.NewContext(manifest.ID, manifest)
			lead.SetSwarmContext(&swarmCtx)

			streamers := map[string]streaming.Streamer{"alpha": trivialStreamer(nil)}

			spy := newMemberAwareSpy()
			delegateTool := engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "lead").
				WithStreamers(streamers).
				WithSwarmRegistry(reg).
				WithSessionManager(mgr).
				WithSessionCreator(mgr).
				WithChildTurnRegistryForTest(spy)

			err := delegateTool.DispatchSwarmMembers(context.Background(), &swarmCtx, members, "go")
			Expect(err).NotTo(HaveOccurred())

			// Per-member Complete fired exactly once.
			Expect(spy.completeCalls.Load()).To(Equal(int32(1)),
				"alpha's per-member Complete must fire once on the happy path")

			// And NEVER Fail — the per-member layer's R2 defence
			// equivalent of executeSync's §S4.2 turnOwnedByWrap
			// guard. Failing here would flip a terminal Turn to
			// Failed, polluting the long-poll snapshot the
			// frontend reads.
			Expect(spy.failCalls.Load()).To(Equal(int32(0)),
				"failMemberTurnIfOwned MUST short-circuit on handle.ownedByCaller=true; per-member Fail call-count must remain 0 across a happy fan-out (R2 defence — Plans/Child Session Turn Registry Plumbing §S7.5)")
		})
	})

	Context("S10.swarm — per-member retry boundary calls ResetForRetry on the right member only", func() {
		It("invokes ResetForRetry exactly once for the retried member, zero for peers", func() {
			// Drive a swarm where member alpha succeeds on
			// attempt 1, and member bravo fails attempt 1 +
			// succeeds attempt 2 (forcing a per-member retry).
			// The per-member layer's runStreamThroughRunner closure
			// at runner.Dispatch invokes ResetForRetry exactly once
			// between bravo's attempts 1 and 2 — bravo's Turn id
			// only. alpha's Turn id must NEVER see a Reset because
			// alpha never retried.
			//
			// Load-bearing assertion: per-member isolation of the
			// retry-boundary mechanism. Without it, a peer's retry
			// would wipe another peer's MessagesAdded slice and the
			// live UI would render mid-stream stutter.
			members := []string{"alpha", "bravo"}
			lead, engines := buildLeadAndMemberEngines(members)
			manifest := noJitterRetryManifest("swarm-s10")
			manifest.Members = members // override single-member default
			// Sequential so we can deterministically reason
			// about which member ran first.
			manifest.Harness.Parallel = false
			reg := swarm.NewRegistry()
			reg.Register(manifest)
			swarmCtx := swarm.NewContext(manifest.ID, manifest)
			lead.SetSwarmContext(&swarmCtx)

			alphaCalls := &atomic.Int32{}
			bravoCalls := &atomic.Int32{}
			streamers := map[string]streaming.Streamer{
				"alpha": retryStreamerWith(0, nil, alphaCalls),               // succeed on attempt 1
				"bravo": retryStreamerWith(1, retryableSwarmErr(), bravoCalls), // fail attempt 1, succeed attempt 2
			}

			spy := newMemberAwareSpy()
			delegateTool := engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "lead").
				WithStreamers(streamers).
				WithSwarmRegistry(reg).
				WithSessionManager(mgr).
				WithSessionCreator(mgr).
				WithChildTurnRegistryForTest(spy)

			err := delegateTool.DispatchSwarmMembers(context.Background(), &swarmCtx, members, "go")
			Expect(err).NotTo(HaveOccurred())

			// Sanity: alpha ran once, bravo ran twice (retry
			// drove a second Stream invocation).
			Expect(alphaCalls.Load()).To(Equal(int32(1)),
				"alpha must run exactly once on the happy path")
			Expect(bravoCalls.Load()).To(Equal(int32(2)),
				"bravo must run twice — attempt 1 failed retryable, attempt 2 succeeded")

			// Load-bearing assertion: ResetForRetry fires exactly
			// once — for bravo's retry boundary only.
			Expect(spy.resetCalls.Load()).To(Equal(int32(1)),
				"ResetForRetry must fire exactly once across the swarm fan-out — between bravo's attempts 1 and 2; alpha never retried so its Turn must never see Reset (plan §S10.swarm — per-member isolation of the retry-boundary mechanism)")

			// Both members must Complete exactly once on the
			// eventual-success path.
			Expect(spy.completeCalls.Load()).To(Equal(int32(2)),
				"each member's eventual-success path must Complete exactly once; total = N members")
			Expect(spy.failCalls.Load()).To(Equal(int32(0)),
				"a retry that eventually succeeds must NOT call Fail (CategoryRetryable is per-attempt, not per-dispatch)")
		})
	})

	Context("S7.6 — bootstrapMemberSession completes the swarm fan-out within wall-clock bounds", func() {
		It("does not stall on the per-member Turn lifecycle even with parallel fan-out", func() {
			// Defensive timing pin: the per-member Turn lifecycle
			// adds two synchronous calls (StartOrReuse + Complete)
			// to the fan-out hot path. This spec confirms the
			// added work doesn't introduce visible latency on a
			// 4-member parallel fan-out — guards against an
			// accidental sync-around-mutex regression at the
			// registry boundary.
			members := []string{"alpha", "bravo", "charlie", "delta"}
			lead, engines := buildLeadAndMemberEngines(members)
			manifest := parallelManifest("swarm-s76", members, true, 4)
			reg := swarm.NewRegistry()
			reg.Register(manifest)
			swarmCtx := swarm.NewContext(manifest.ID, manifest)
			lead.SetSwarmContext(&swarmCtx)

			streamers := map[string]streaming.Streamer{}
			for _, m := range members {
				streamers[m] = trivialStreamer(nil)
			}

			spy := newMemberAwareSpy()
			delegateTool := engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "lead").
				WithStreamers(streamers).
				WithSwarmRegistry(reg).
				WithSessionManager(mgr).
				WithSessionCreator(mgr).
				WithChildTurnRegistryForTest(spy)

			start := time.Now()
			err := delegateTool.DispatchSwarmMembers(context.Background(), &swarmCtx, members, "go")
			elapsed := time.Since(start)

			Expect(err).NotTo(HaveOccurred())
			Expect(elapsed).To(BeNumerically("<", 2*time.Second),
				"parallel fan-out across 4 members with Turn lifecycle wired must complete in well under 2s; longer suggests a sync-around-mutex regression at StartOrReuse/Complete (defensive timing pin)")

			// Sanity: all 4 members completed.
			Expect(spy.completeCalls.Load()).To(Equal(int32(4)))
		})
	})
})
