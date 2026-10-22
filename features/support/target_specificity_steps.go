//go:build e2e

package support

import (
	"context"
	"errors"
	"strings"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/coordination"
	"github.com/baphled/flowstate/internal/swarm"
)

// specificityGateState carries the per-scenario target-specificity BDD
// state: the runner, a real in-memory coord-store holding the member's
// payload, the configured identifiers, and the last gate error.
type specificityGateState struct {
	runner swarm.GateRunner
	store  *coordination.MemoryStore
	ids    []string
	noIDs  bool
	err    error
}

// RegisterTargetSpecificitySteps wires the target-specificity step
// definitions onto the godog scenario context.
//
// Expected: parameters for RegisterTargetSpecificitySteps.
//
// Side effects: None.
func RegisterTargetSpecificitySteps(ctx *godog.ScenarioContext) {
	st := &specificityGateState{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		st.store = coordination.NewMemoryStore()
		st.ids = nil
		st.noIDs = false
		st.err = nil
		st.runner = swarm.NewTargetSpecificityRunner()
		return c, nil
	})
	ctx.Step(`^the target-specificity gate runner is registered$`, st.runnerRegistered)
	ctx.Step(`^a member submits payload "([^"]*)"$`, st.memberSubmits)
	ctx.Step(`^the engagement brief supplies target identifiers "([^"]*)"$`, st.briefSupplies)
	ctx.Step(`^the engagement brief supplies no target identifiers$`, st.briefSuppliesNone)
	ctx.Step(`^the target-specificity gate should fail$`, st.gateFails)
	ctx.Step(`^the target-specificity gate should pass$`, st.gatePasses)
	ctx.Step(`^the failure should mention target-specific evidence$`, st.failureMentionsEvidence)
}

func (s *specificityGateState) gateSpec() swarm.GateSpec {
	policy := map[string]any{}
	if !s.noIDs {
		policy["target_identifiers"] = s.ids
	}
	return swarm.GateSpec{
		Name:   "target-specificity",
		Kind:   swarm.TargetSpecificityGateKind,
		When:   "post-member",
		Target: "Security-Engineer",
		Policy: policy,
	}
}

func (s *specificityGateState) run() error {
	s.err = s.runner.Run(context.Background(), s.gateSpec(), swarm.GateArgs{
		SwarmID:     "dd-swarm",
		ChainPrefix: "dd-swarm",
		MemberID:    "Security-Engineer",
		CoordStore:  s.store,
	})
	return nil
}

func (s *specificityGateState) runnerRegistered() error {
	if s.runner == nil {
		return errors.New("runner not registered")
	}
	return nil
}

func (s *specificityGateState) memberSubmits(payload string) error {
	if err := s.store.Set("dd-swarm/Security-Engineer/output", []byte(payload)); err != nil {
		return err
	}
	return s.run()
}

func (s *specificityGateState) briefSupplies(csv string) error {
	for _, id := range strings.Split(csv, ",") {
		s.ids = append(s.ids, strings.TrimSpace(id))
	}
	return s.run()
}

func (s *specificityGateState) briefSuppliesNone() error {
	s.noIDs = true
	return s.run()
}

func (s *specificityGateState) gateFails() error {
	if s.err == nil {
		return errors.New("expected specificity gate failure, got pass")
	}
	return nil
}

func (s *specificityGateState) gatePasses() error {
	if s.err != nil {
		return s.err
	}
	return nil
}

func (s *specificityGateState) failureMentionsEvidence() error {
	if s.err == nil || !strings.Contains(s.err.Error(), "target-specific evidence") {
		return errors.New("failure should mention target-specific evidence")
	}
	return nil
}
