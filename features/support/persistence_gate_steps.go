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

// persistenceGateState carries the per-scenario persistence-completeness
// BDD state: the runner under test, a real in-memory coord-store, the
// last gate error, and the policy under test.
type persistenceGateState struct {
	runner swarm.GateRunner
	store  *coordination.MemoryStore
	err    error
	policy map[string]any
}

// RegisterPersistenceCompletenessSteps wires the persistence-
// completeness step definitions onto the godog scenario context.
//
// Expected: parameters for RegisterPersistenceCompletenessSteps.
//
// Side effects: None.
func RegisterPersistenceCompletenessSteps(ctx *godog.ScenarioContext) {
	st := &persistenceGateState{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		st.store = coordination.NewMemoryStore()
		st.err = nil
		st.runner = swarm.NewPersistenceCompletenessRunner()
		st.policy = map[string]any{
			"required_members":     []string{"tech-lead"},
			"output_key":           "verdict",
			"mediation_reason_key": "mediation",
		}
		return c, nil
	})
	ctx.Step(`^the persistence-completeness gate runner is registered$`, st.runnerRegistered)
	ctx.Step(`^gated member "([^"]*)" has no coordination-store entry at "([^"]*)"$`, st.memberHasNoEntry)
	ctx.Step(`^gated member "([^"]*)" has an empty coordination-store entry at "([^"]*)"$`, st.memberHasEmptyEntry)
	ctx.Step(`^gated member "([^"]*)" has a non-empty coordination-store entry at "([^"]*)"$`, st.memberHasNonEmptyEntry)
	ctx.Step(`^the coordination store records a coordinator-mediated write for member "([^"]*)" with reason "([^"]*)"$`, st.coordinatorMediatedWrite)
	ctx.Step(`^the persistence-completeness gate should fail$`, st.gateFails)
	ctx.Step(`^the persistence-completeness gate should pass$`, st.gatePasses)
	ctx.Step(`^the failure should name the member "([^"]*)"$`, st.failureNamesMember)
	ctx.Step(`^the failure should name the key "([^"]*)"$`, st.failureNamesKey)
}

func (s *persistenceGateState) gateSpec() swarm.GateSpec {
	return swarm.GateSpec{
		Name:   "persistence-precheck",
		Kind:   swarm.PersistenceCompletenessGateKind,
		When:   "post",
		Policy: s.policy,
	}
}

func (s *persistenceGateState) run() error {
	s.err = s.runner.Run(context.Background(), s.gateSpec(), swarm.GateArgs{
		SwarmID:     "dd-swarm",
		ChainPrefix: "dd-swarm",
		MemberID:    "coordinator",
		CoordStore:  s.store,
	})
	return nil
}

func (s *persistenceGateState) runnerRegistered() error {
	if s.runner == nil {
		return errors.New("runner not registered")
	}
	return nil
}

func (s *persistenceGateState) memberHasNoEntry(member, key string) error {
	return s.run()
}

func (s *persistenceGateState) memberHasEmptyEntry(member, key string) error {
	if err := s.store.Set(key, []byte("   \n  ")); err != nil {
		return err
	}
	return s.run()
}

func (s *persistenceGateState) memberHasNonEmptyEntry(member, key string) error {
	if err := s.store.Set(key, []byte(`{"verdict":"proceed","confidence_pct":80}`)); err != nil {
		return err
	}
	return s.run()
}

func (s *persistenceGateState) coordinatorMediatedWrite(member, reason string) error {
	payload := `{"reason":"` + reason + `","by":"coordinator"}`
	if reason == "" {
		payload = `{"reason":""}`
	}
	if err := s.store.Set("dd-swarm/coordinator/mediation/"+strings.ToLower(member), []byte(payload)); err != nil {
		return err
	}
	return s.run()
}

func (s *persistenceGateState) gateFails() error {
	if s.err == nil {
		return errors.New("expected persistence gate failure, got pass")
	}
	return nil
}

func (s *persistenceGateState) gatePasses() error {
	if s.err != nil {
		return s.err
	}
	return nil
}

func (s *persistenceGateState) failureNamesMember(member string) error {
	// Policy member ids are lower-cased (coord-store key convention);
	// compare the feature's member name case-insensitively.
	if s.err == nil || !strings.Contains(strings.ToLower(s.err.Error()), strings.ToLower(member)) {
		return errors.New("failure should name member: " + member)
	}
	return nil
}

func (s *persistenceGateState) failureNamesKey(key string) error {
	// Member ids are lower-cased on key resolution (joinKey
	// convention), so compare the feature's key case-insensitively.
	if s.err == nil || !strings.Contains(strings.ToLower(s.err.Error()), strings.ToLower(key)) {
		return errors.New("failure should name key: " + key)
	}
	return nil
}
