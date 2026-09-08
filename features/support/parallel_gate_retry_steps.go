//go:build e2e

// Package support provides BDD test step definitions and helpers.
package support

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/coordination"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/swarm"
)

// parallelGateRetrySteps holds state for the parallel-dispatch gate
// retry scenarios (features/gates/parallel_gate_retry.feature). Each
// scenario wires a parallel swarm with a post-member gate runner and
// asserts the re-delegate-on-gate-failure retry also applies on the
// parallel dispatch path, plus the hard "gate not registered" config
// error contract.
type parallelGateRetrySteps struct {
	gateCalls    int32
	memberCalls  int32
	lastMemberMsg atomic.Value // string
	swarmID      string
	err          error
}

// parallelGateRunner fails the first failFor post-member gate
// dispatches with the narrate-without-write GateError shape the
// engine's forced-tool-choice corrective retry keys off, then passes.
// failFor < 0 never passes.
type parallelGateRunner struct {
	failFor int
	calls   *int32
}

func (r *parallelGateRunner) Run(_ context.Context, gate swarm.GateSpec, args swarm.GateArgs) error {
	n := atomic.AddInt32(r.calls, 1)
	if int(n) <= r.failFor || r.failFor < 0 {
		return &swarm.GateError{
			GateName: gate.Name,
			GateKind: gate.Kind,
			When:     gate.When,
			SwarmID:  args.SwarmID,
			MemberID: args.MemberID,
			Reason: "no member output found at [parallel-retry/reviewer/output]: the member did not write its output — " +
				"it likely narrated the write but emitted no tool call. Re-delegate this member with an explicit " +
				"instruction to perform the coordination_store write (do not narrate it).",
		}
	}
	return nil
}

// RegisterParallelGateRetrySteps registers the parallel-dispatch gate
// retry BDD step definitions.
//
// Expected:
//   - ctx is a valid godog ScenarioContext for step registration.
//
// Side effects:
//   - Registers step definitions on the provided scenario context.
func RegisterParallelGateRetrySteps(ctx *godog.ScenarioContext) {
	s := &parallelGateRetrySteps{swarmID: "parallel-retry"}

	ctx.Step(`^a parallel swarm whose reviewer fails the post-member gate once$`, s.swarmWithFlakyGate(1))
	ctx.Step(`^a parallel swarm whose reviewer always fails the post-member gate$`, s.swarmWithFlakyGate(-1))
	ctx.Step(`^a parallel swarm referencing ext:never-registered$`, s.swarmWithUnregisteredGate)
	ctx.Step(`^the swarm is dispatched$`, s.theSwarmIsDispatched)
	ctx.Step(`^the reviewer is re-dispatched with the gate directive$`, s.theReviewerIsRedispatchedWithDirective)
	ctx.Step(`^the swarm completes successfully$`, s.theSwarmCompletesSuccessfully)
	ctx.Step(`^the dispatch fails with the gate error$`, s.theDispatchFailsWithGateError)
	ctx.Step(`^the reviewer was dispatched exactly PostMemberGateMaxAttempts times$`, s.memberDispatchBudgetExhausted)
	ctx.Step(`^the dispatch fails with a configuration error naming the gate kind$`, s.theDispatchFailsWithConfigError)
	ctx.Step(`^the member is not re-dispatched$`, s.theMemberIsNotRedispatched)
}

// swarmWithFlakyGate builds a parallel swarm (lead + reviewer) whose
// post-member result-schema gate fails the first failFor dispatches.
//
// Side effects:
//   - Records gate/member call counters on the step state.
func (s *parallelGateRetrySteps) swarmWithFlakyGate(failFor int) func() {
	return func() {
		gates := []swarm.GateSpec{{
			Name:   "post-member-reviewer-output",
			Kind:   "builtin:result-schema",
			When:   swarm.LifecyclePostMember,
			Target: "reviewer",
		}}
		s.dispatchSwarm(gates, &parallelGateRunner{failFor: failFor, calls: &s.gateCalls})
	}
}

// swarmWithUnregisteredGate builds a parallel swarm whose post-member
// gate references an ext kind that was never registered — the
// dispatch-time hard config error case.
//
// Side effects:
//   - Records gate/member call counters on the step state.
func (s *parallelGateRetrySteps) swarmWithUnregisteredGate() {
	gates := []swarm.GateSpec{{
		Name:   "post-member-reviewer-output",
		Kind:   "ext:never-registered",
		When:   swarm.LifecyclePostMember,
		Target: "reviewer",
	}}
	// Use the REAL MultiRunner with no backends registered so the ext
	// kind resolves through RunGate → DispatchExt and surfaces the
	// hard "is not registered" configuration error.
	s.dispatchSwarm(gates, swarm.NewMultiRunner())
}

// dispatchSwarm wires the parallel swarm and runs DispatchSwarmMembers.
//
// Expected:
//   - gates is the manifest's gate list.
//   - runner is the gate runner installed via WithGateRunner.
//
// Side effects:
//   - Records the dispatch error and the member's last prompt.
func (s *parallelGateRetrySteps) dispatchSwarm(gates []swarm.GateSpec, runner swarm.GateRunner) {
	members := []string{"reviewer"}
	lead := engine.New(engine.Config{
		ChatProvider: &staticProvider{name: "lead"},
		Manifest: agent.Manifest{
			ID:                "lead",
			Name:              "Lead",
			Instructions:      agent.Instructions{SystemPrompt: "lead"},
			Delegation:        agent.Delegation{CanDelegate: true},
			ContextManagement: agent.DefaultContextManagement(),
		},
	})
	revEng := engine.New(engine.Config{
		ChatProvider: &staticProvider{name: "reviewer"},
		Manifest: agent.Manifest{
			ID:                "reviewer",
			Name:              "Reviewer",
			Instructions:      agent.Instructions{SystemPrompt: "review"},
			ContextManagement: agent.DefaultContextManagement(),
		},
	})
	engines := map[string]*engine.Engine{"lead": lead, "reviewer": revEng}

	manifest := &swarm.Manifest{
		SchemaVersion: "1.0.0",
		ID:            s.swarmID,
		Lead:          "lead",
		Members:       members,
		Harness: swarm.HarnessConfig{
			Parallel:    true,
			MaxParallel: 2,
			Gates:       gates,
		},
	}
	reg := swarm.NewRegistry()
	reg.Register(manifest)
	swarmCtx := swarm.NewContext(manifest.ID, manifest)
	lead.SetSwarmContext(&swarmCtx)

	var mu sync.Mutex
	streamers := map[string]streaming.Streamer{
		"reviewer": streamerFuncForEngine(func(_ context.Context, _ string, msg string) (<-chan provider.StreamChunk, error) {
			atomic.AddInt32(&s.memberCalls, 1)
			mu.Lock()
			s.lastMemberMsg.Store(msg)
			mu.Unlock()
			ch := make(chan provider.StreamChunk, 1)
			ch <- provider.StreamChunk{Content: "review complete", Done: true}
			close(ch)
			return ch, nil
		}),
	}

	store := coordination.NewMemoryStore()
	delegateTool := engine.NewDelegateToolWithBackground(
		engines, agent.Delegation{CanDelegate: true}, "lead", nil, store,
	).WithStreamers(streamers).
		WithSwarmRegistry(reg).
		WithOwnerEngine(lead).
		WithGateRunner(runner)

	s.err = delegateTool.DispatchSwarmMembers(context.Background(), &swarmCtx, members, "review the plan")
}

// theSwarmIsDispatched is the When-step; the dispatch runs inside the
// Given wiring so step state is fully populated by the time the
// assertions fire.
//
// Side effects:
//   - None (dispatch already ran).
func (s *parallelGateRetrySteps) theSwarmIsDispatched() {}

// theReviewerIsRedispatchedWithDirective asserts the member was
// re-dispatched after the gate miss AND the retry prompt carries the
// gate failure directive.
//
// Side effects:
//   - None.
func (s *parallelGateRetrySteps) theReviewerIsRedispatchedWithDirective() error {
	if n := atomic.LoadInt32(&s.memberCalls); n < 2 {
		return fmt.Errorf("expected the reviewer to be re-dispatched after the gate miss; got %d dispatch(es)", n)
	}
	msg, _ := s.lastMemberMsg.Load().(string)
	if !strings.Contains(msg, "rejected your output") {
		return fmt.Errorf("expected the re-dispatch prompt to carry the gate directive; got %q", msg)
	}
	return nil
}

// theSwarmCompletesSuccessfully asserts the parallel dispatch
// resolved after the bounded retry.
//
// Side effects:
//   - None.
func (s *parallelGateRetrySteps) theSwarmCompletesSuccessfully() error {
	if s.err != nil {
		return fmt.Errorf("expected the parallel swarm to complete after the bounded gate retry; got %v", s.err)
	}
	return nil
}

// theDispatchFailsWithGateError asserts budget exhaustion surfaces the
// GateError terminally.
//
// Side effects:
//   - None.
func (s *parallelGateRetrySteps) theDispatchFailsWithGateError() error {
	var gateErr *swarm.GateError
	if !errors.As(s.err, &gateErr) {
		return fmt.Errorf("expected a *swarm.GateError after exhausting the retry budget; got %v", s.err)
	}
	return nil
}

// memberDispatchBudgetExhausted asserts the member ran exactly
// PostMemberGateMaxAttempts times — the initial attempt plus the
// bounded re-delegations, matching the sequential path's contract.
//
// Side effects:
//   - None.
func (s *parallelGateRetrySteps) memberDispatchBudgetExhausted() error {
	got := int(atomic.LoadInt32(&s.memberCalls))
	if got != engine.PostMemberGateMaxAttempts {
		return fmt.Errorf("expected exactly %d reviewer dispatches; got %d", engine.PostMemberGateMaxAttempts, got)
	}
	return nil
}

// theDispatchFailsWithConfigError asserts the unregistered gate kind
// surfaces as a hard, actionable configuration error naming the kind.
//
// Side effects:
//   - None.
func (s *parallelGateRetrySteps) theDispatchFailsWithConfigError() error {
	if s.err == nil {
		return errors.New("expected a dispatch error for the unregistered gate kind; got nil")
	}
	if !strings.Contains(s.err.Error(), "ext:never-registered") {
		return fmt.Errorf("expected the config error to name the gate kind; got %v", s.err)
	}
	if !strings.Contains(s.err.Error(), "not registered") {
		return fmt.Errorf("expected the 'not registered' config signature; got %v", s.err)
	}
	return nil
}

// theMemberIsNotRedispatched asserts the config error is terminal:
// the member dispatched once and was never re-delegated.
//
// Side effects:
//   - None.
func (s *parallelGateRetrySteps) theMemberIsNotRedispatched() error {
	if n := atomic.LoadInt32(&s.memberCalls); n != 1 {
		return fmt.Errorf("a gate-not-registered config error must be terminal — expected 1 member dispatch; got %d", n)
	}
	return nil
}

// streamerFuncForEngine adapts a closure to streaming.Streamer.
type streamerFuncForEngine func(ctx context.Context, agentID string, msg string) (<-chan provider.StreamChunk, error)

// Stream satisfies streaming.Streamer.
func (f streamerFuncForEngine) Stream(ctx context.Context, agentID string, msg string) (<-chan provider.StreamChunk, error) {
	return f(ctx, agentID, msg)
}

// staticProvider is a minimal provider for engine construction in BDD
// wiring; streams are supplied by the explicit streamers map.
type staticProvider struct{ name string }

// Name identifies the provider.
func (p *staticProvider) Name() string { return p.name }

// Stream satisfies provider.ChatProvider.
func (p *staticProvider) Stream(context.Context, provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk, 1)
	ch <- provider.StreamChunk{Content: "ok", Done: true}
	close(ch)
	return ch, nil
}

// Chat satisfies provider.ChatProvider.
func (p *staticProvider) Chat(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

// Embed satisfies provider.ChatProvider.
func (p *staticProvider) Embed(context.Context, provider.EmbedRequest) ([]float64, error) {
	return []float64{0.1}, nil
}

// Models satisfies provider.ChatProvider.
func (p *staticProvider) Models() ([]provider.Model, error) { return nil, nil }
