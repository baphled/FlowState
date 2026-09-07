//go:build e2e

package support

import (
	"context"
	"errors"
	"strings"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/swarm"
	"github.com/baphled/flowstate/internal/turn"
)

// failingGateRunner is a GateRunner whose Run always returns a typed
// gate error. Used to pin halt semantics of swarm.Dispatch without
// wiring a real builtin.
type failingGateRunner struct{}

func (failingGateRunner) Run(_ context.Context, _ swarm.GateSpec, _ swarm.GateArgs) error {
	return &swarm.GateError{
		GateName: "always-fail",
		GateKind: swarm.PersistenceCompletenessGateKind,
		Reason:   "forced failure for BDD",
	}
}

// passingGateRunner is a GateRunner that always passes. Used to pin
// that a passing gate yields a clean DispatchReport.
type passingGateRunner struct{}

func (passingGateRunner) Run(_ context.Context, _ swarm.GateSpec, _ swarm.GateArgs) error {
	return nil
}

// gateBlockingState carries the per-scenario gate-blocking BDD state.
type gateBlockingState struct {
	specs        []swarm.GateSpec
	runner       swarm.GateRunner
	report       swarm.DispatchReport
	haltErr      error
	gateFailure  turn.GateFailure
}

// RegisterGateFailureBlockingSteps wires the gate-blocking step
// definitions onto the godog scenario context.
//
// Expected: parameters for RegisterGateFailureBlockingSteps.
//
// Side effects: None.
func RegisterGateFailureBlockingSteps(ctx *godog.ScenarioContext) {
	st := &gateBlockingState{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		st.specs = nil
		st.runner = nil
		st.report = swarm.DispatchReport{}
		st.haltErr = nil
		return c, nil
	})
	ctx.Step(`^a swarm with a halting post-member gate that always fails$`, st.swarmWithFailingGate)
	ctx.Step(`^the member completes its stream and the post-member gate is dispatched$`, st.dispatchPostMemberGates)
	ctx.Step(`^the gate dispatch reports a halt$`, st.dispatchReportsHalt)
	ctx.Step(`^the delegation returns the gate error instead of a tool result$`, st.delegationReturnsGateError)
	ctx.Step(`^no further provider or tool calls occur for the turn$`, st.noFurtherCalls)
	ctx.Step(`^a swarm manifest whose gate references kind "([^"]*)" with no such gate registered$`, st.manifestReferencesUnregisteredKind)
	ctx.Step(`^the manifest is validated against the gate registry$`, st.validateAgainstRegistry)
	ctx.Step(`^validation fails with an error naming the unregistered gate kind$`, st.validationNamesUnregisteredKind)
	ctx.Step(`^a gate failure is published on the event bus for the session$`, st.gateFailurePublished)
	ctx.Step(`^the turn record carries a gate failure with gate name, kind, lifecycle, member id and reason$`, st.turnRecordCarriesGateFailure)
	ctx.Step(`^the swarm events stream projects the failure as a gate event with status "failed"$`, st.streamProjectsGateEvent)
}

func (s *gateBlockingState) swarmWithFailingGate() error {
	s.specs = []swarm.GateSpec{{
		Name:   "always-fail",
		Kind:   swarm.PersistenceCompletenessGateKind,
		When:   swarm.LifecyclePostMember,
		Target: "reviewer",
	}}
	s.runner = failingGateRunner{}
	return nil
}

func (s *gateBlockingState) dispatchPostMemberGates() error {
	matches := swarm.MemberGatesFor(s.specs, swarm.LifecyclePostMember, "reviewer")
	s.report = swarm.Dispatch(context.Background(), s.runner, matches, swarm.GateArgs{
		SwarmID:  "bdd-swarm",
		MemberID: "reviewer",
	})
	if s.report.Halted {
		s.haltErr = s.report.Err
	}
	return nil
}

func (s *gateBlockingState) dispatchReportsHalt() error {
	if !s.report.Halted {
		return errors.New("expected dispatch report to halt")
	}
	if s.report.HaltedBy != "always-fail" {
		return errors.New("expected HaltedBy to name the failing gate")
	}
	return nil
}

func (s *gateBlockingState) delegationReturnsGateError() error {
	if s.haltErr == nil {
		return errors.New("expected a halt error from dispatch")
	}
	var gateErr *swarm.GateError
	if !errors.As(s.haltErr, &gateErr) {
		return errors.New("expected halt error to be a *swarm.GateError")
	}
	if gateErr.Reason != "forced failure for BDD" {
		return errors.New("unexpected gate error reason: " + gateErr.Reason)
	}
	return nil
}

func (s *gateBlockingState) noFurtherCalls() error {
	if s.report.Halted {
		return nil
	}
	return errors.New("turn continued after halt")
}

func (s *gateBlockingState) manifestReferencesUnregisteredKind(kind string) error {
	s.specs = []swarm.GateSpec{{
		Name:   "ghost-gate",
		Kind:   kind,
		When:   swarm.LifecyclePostMember,
		Target: "reviewer",
	}}
	return nil
}

func (s *gateBlockingState) validateAgainstRegistry() error {
	err := swarm.ValidateGateKindsRegistered(s.specs)
	if err != nil {
		s.haltErr = err
	}
	return nil
}

func (s *gateBlockingState) validationNamesUnregisteredKind() error {
	if s.haltErr == nil {
		return errors.New("expected validation to fail for unregistered kind")
	}
	if !strings.Contains(s.haltErr.Error(), "ext:mental-health-safety") {
		return errors.New("error does not name the unregistered kind: " + s.haltErr.Error())
	}
	return nil
}

func (s *gateBlockingState) gateFailurePublished() error {
	s.gateFailure = turn.GateFailure{
		SwarmID:   "bdd-swarm",
		Lifecycle: swarm.LifecyclePostMember,
		MemberID:  "reviewer",
		GateName:  "always-fail",
		GateKind:  swarm.PersistenceCompletenessGateKind,
		Reason:    "forced failure for BDD",
	}
	return nil
}

func (s *gateBlockingState) turnRecordCarriesGateFailure() error {
	gf := s.gateFailure
	if gf.GateName == "" || gf.GateKind == "" || gf.Lifecycle == "" || gf.MemberID == "" || gf.Reason == "" {
		return errors.New("turn gate failure record is missing structured fields")
	}
	return nil
}

func (s *gateBlockingState) streamProjectsGateEvent() error {
	data := events.GateEventData{
		SwarmID:   "bdd-swarm",
		SessionID: "sess-bdd",
		Lifecycle: swarm.LifecyclePostMember,
		MemberID:  "reviewer",
		GateName:  "always-fail",
		GateKind:  swarm.PersistenceCompletenessGateKind,
		Reason:    "forced failure for BDD",
	}
	ev := events.NewGateFailedEvent(data)
	if !strings.Contains(ev.EventType(), "gate") {
		return errors.New("expected a gate-failed bus event")
	}
	return nil
}
