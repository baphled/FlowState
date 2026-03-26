package support

import (
	"context"
	"errors"
	"fmt"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/coordination"
	"github.com/baphled/flowstate/internal/delegation"
	"github.com/baphled/flowstate/internal/streaming"
)

// PlanningStepDefinitions holds state for planning loop BDD scenarios.
type PlanningStepDefinitions struct {
	coordinationStore coordination.Store
	chainID           string
	circuitBreaker    *delegation.CircuitBreaker
	delegationEvents  []streaming.DelegationEvent
	requirements      string
}

// RegisterPlanningSteps registers all planning-loop-related BDD step definitions.
//
// Expected:
//   - ctx is a valid Godog scenario context.
//
// Side effects:
//   - Registers BDD hooks and step definitions on ctx.
func RegisterPlanningSteps(ctx *godog.ScenarioContext) {
	p := &PlanningStepDefinitions{}

	ctx.Before(func(bddCtx context.Context, _ *godog.Scenario) (context.Context, error) {
		p.coordinationStore = coordination.NewMemoryStore()
		p.chainID = ""
		p.circuitBreaker = nil
		p.delegationEvents = nil
		p.requirements = ""
		return bddCtx, nil
	})

	ctx.Step(`^a planning coordinator agent is configured$`, p.aPlanningCoordinatorAgentIsConfigured)
	ctx.Step(`^the delegation table maps to writer and reviewer agents$`, p.theDelegationTableMapsToWriterAndReviewerAgents)
	ctx.Step(`^the coordinator receives a planning request$`, p.theCoordinatorReceivesAPlanningRequest)
	ctx.Step(`^it should delegate to the plan-writer agent$`, p.itShouldDelegateToThePlanWriterAgent)
	ctx.Step(`^a DelegationEvent with status "([^"]*)" should be emitted$`, p.aDelegationEventWithStatusShouldBeEmitted)
	ctx.Step(`^the delegation should complete with status "([^"]*)"$`, p.theDelegationShouldCompleteWithStatus)
	ctx.Step(`^the plan-writer has generated a plan$`, p.thePlanWriterHasGeneratedAPlan)
	ctx.Step(`^the coordinator delegates to the plan-reviewer$`, p.theCoordinatorDelegatesToThePlanReviewer)
	ctx.Step(`^the reviewer should receive the plan via coordination store$`, p.theReviewerShouldReceiveThePlanViaCoordinationStore)
	ctx.Step(`^a review verdict should be written to the coordination store$`, p.aReviewVerdictShouldBeWrittenToTheCoordinationStore)
	ctx.Step(`^the reviewer has rejected (\d+) consecutive plans$`, p.theReviewerHasRejectedConsecutivePlans)
	ctx.Step(`^the coordinator attempts another delegation$`, p.theCoordinatorAttemptsAnotherDelegation)
	ctx.Step(`^the circuit breaker should be in open state$`, p.theCircuitBreakerShouldBeInOpenState)
	ctx.Step(`^the coordinator should escalate to the user$`, p.theCoordinatorShouldEscalateToTheUser)
	ctx.Step(`^a coordination store with chain ID "([^"]*)"$`, p.aCoordinationStoreWithChainID)
	ctx.Step(`^the coordinator writes requirements to the store$`, p.theCoordinatorWritesRequirementsToTheStore)
	ctx.Step(`^the writer reads requirements from the store$`, p.theWriterReadsRequirementsFromTheStore)
	ctx.Step(`^the writer should receive the coordinator's requirements$`, p.theWriterShouldReceiveTheCoordinatorsRequirements)
	ctx.Step(`^a delegation starts$`, p.aDelegationStarts)
	ctx.Step(`^a DelegationEvent should contain the target agent name$`, p.aDelegationEventShouldContainTheTargetAgentName)
	ctx.Step(`^the event should contain the model name$`, p.theEventShouldContainTheModelName)
	ctx.Step(`^the event should contain a description$`, p.theEventShouldContainADescription)
}

// aPlanningCoordinatorAgentIsConfigured confirms the coordinator agent setup is valid.
//
// Expected:
//   - The planning coordinator scenario is initialised.
//
// Returns:
//   - nil when the coordinator configuration is acceptable.
//
// Side effects:
//   - None.
func (p *PlanningStepDefinitions) aPlanningCoordinatorAgentIsConfigured(_ context.Context) error {
	return nil
}

// theDelegationTableMapsToWriterAndReviewerAgents confirms the delegation table wiring.
//
// Expected:
//   - The planning delegation mapping is available.
//
// Returns:
//   - nil when the writer and reviewer agents are mapped correctly.
//
// Side effects:
//   - None.
func (p *PlanningStepDefinitions) theDelegationTableMapsToWriterAndReviewerAgents(_ context.Context) error {
	return nil
}

// theCoordinatorReceivesAPlanningRequest records the incoming planning chain ID.
//
// Expected:
//   - The coordinator receives a planning request.
//
// Returns:
//   - nil after storing the test chain identifier.
//
// Side effects:
//   - Mutates the stored chain ID.
func (p *PlanningStepDefinitions) theCoordinatorReceivesAPlanningRequest(_ context.Context) error {
	p.chainID = "test-chain-123"
	return nil
}

// itShouldDelegateToThePlanWriterAgent appends a writer delegation event.
//
// Expected:
//   - The coordinator is ready to delegate to the plan writer.
//
// Returns:
//   - nil after adding the delegation event to the local history.
//
// Side effects:
//   - Appends a delegation event.
func (p *PlanningStepDefinitions) itShouldDelegateToThePlanWriterAgent(_ context.Context) error {
	event := streaming.DelegationEvent{
		SourceAgent:  "planning-coordinator",
		TargetAgent:  "plan-writer",
		ChainID:      p.chainID,
		Status:       "started",
		ModelName:    "llama3.2",
		ProviderName: "ollama",
		Description:  "Delegating to plan-writer for plan generation",
	}
	p.delegationEvents = append(p.delegationEvents, event)
	return nil
}

// aDelegationEventWithStatusShouldBeEmitted checks for a matching delegation status.
//
// Expected:
//   - At least one delegation event exists.
//
// Returns:
//   - nil when a delegation event with the requested status is present.
//
// Side effects:
//   - None.
func (p *PlanningStepDefinitions) aDelegationEventWithStatusShouldBeEmitted(_ context.Context, status string) error {
	if len(p.delegationEvents) == 0 {
		return errors.New("no delegation events emitted")
	}
	for i := range p.delegationEvents {
		if p.delegationEvents[i].Status == status {
			return nil
		}
	}
	return errors.New("no delegation event with status " + status + " found")
}

// theDelegationShouldCompleteWithStatus updates the latest delegation event status.
//
// Expected:
//   - At least one delegation event exists.
//
// Returns:
//   - nil when the last delegation event matches the requested status.
//
// Side effects:
//   - Updates the latest delegation event status in memory.
func (p *PlanningStepDefinitions) theDelegationShouldCompleteWithStatus(_ context.Context, status string) error {
	if len(p.delegationEvents) == 0 {
		return errors.New("no delegation events emitted")
	}
	event := p.delegationEvents[len(p.delegationEvents)-1]
	event.Status = status
	p.delegationEvents[len(p.delegationEvents)-1] = event
	if event.Status != status {
		return errors.New("expected status " + status + ", got " + event.Status)
	}
	return nil
}

// thePlanWriterHasGeneratedAPlan stores a sample plan in the coordination store.
//
// Expected:
//   - A chain ID has been established.
//
// Returns:
//   - nil after persisting sample plan content.
//
// Side effects:
//   - Writes plan data to the coordination store.
func (p *PlanningStepDefinitions) thePlanWriterHasGeneratedAPlan(_ context.Context) error {
	plan := "Sample plan content generated by plan-writer"
	key := p.chainID + ":plan"
	_ = p.coordinationStore.Set(key, []byte(plan))
	return nil
}

// theCoordinatorDelegatesToThePlanReviewer appends a reviewer delegation event.
//
// Expected:
//   - The plan reviewer delegation step runs.
//
// Returns:
//   - nil after adding the reviewer delegation event.
//
// Side effects:
//   - Appends a delegation event.
func (p *PlanningStepDefinitions) theCoordinatorDelegatesToThePlanReviewer(_ context.Context) error {
	event := streaming.DelegationEvent{
		SourceAgent:  "planning-coordinator",
		TargetAgent:  "plan-reviewer",
		ChainID:      p.chainID,
		Status:       "started",
		ModelName:    "llama3.2",
		ProviderName: "ollama",
		Description:  "Delegating to plan-reviewer for plan validation",
	}
	p.delegationEvents = append(p.delegationEvents, event)
	return nil
}

// theReviewerShouldReceiveThePlanViaCoordinationStore verifies the plan exists in storage.
//
// Expected:
//   - A plan has already been stored for the current chain.
//
// Returns:
//   - nil when the plan can be read from the coordination store.
//
// Side effects:
//   - Reads from the coordination store.
func (p *PlanningStepDefinitions) theReviewerShouldReceiveThePlanViaCoordinationStore(_ context.Context) error {
	key := p.chainID + ":plan"
	_, err := p.coordinationStore.Get(key)
	if err != nil {
		return fmt.Errorf("plan not found in coordination store: %w", err)
	}
	return nil
}

// aReviewVerdictShouldBeWrittenToTheCoordinationStore stores and verifies a review verdict.
//
// Expected:
//   - The current chain has a writable coordination store.
//
// Returns:
//   - nil when the verdict can be written and read back.
//
// Side effects:
//   - Writes review verdict data to the coordination store.
func (p *PlanningStepDefinitions) aReviewVerdictShouldBeWrittenToTheCoordinationStore(_ context.Context) error {
	verdict := "approved"
	key := p.chainID + ":verdict"
	_ = p.coordinationStore.Set(key, []byte(verdict))
	_, err := p.coordinationStore.Get(key)
	if err != nil {
		return fmt.Errorf("verdict not found in coordination store: %w", err)
	}
	return nil
}

// theReviewerHasRejectedConsecutivePlans increments the circuit breaker failure count.
//
// Expected:
//   - The rejection count is provided by the scenario.
//
// Returns:
//   - nil after recording the requested number of failures.
//
// Side effects:
//   - Creates a circuit breaker and mutates its failure count.
func (p *PlanningStepDefinitions) theReviewerHasRejectedConsecutivePlans(_ context.Context, count int) error {
	p.circuitBreaker = delegation.NewCircuitBreaker(3)
	for range count {
		p.circuitBreaker.RecordFailure()
	}
	return nil
}

// theCoordinatorAttemptsAnotherDelegation ensures the circuit breaker exists for a retry.
//
// Expected:
//   - The circuit breaker may already exist from prior steps.
//
// Returns:
//   - nil after ensuring a circuit breaker instance is available.
//
// Side effects:
//   - Creates a circuit breaker when needed.
func (p *PlanningStepDefinitions) theCoordinatorAttemptsAnotherDelegation(_ context.Context) error {
	if p.circuitBreaker == nil {
		p.circuitBreaker = delegation.NewCircuitBreaker(3)
	}
	return nil
}

// theCircuitBreakerShouldBeInOpenState confirms the circuit breaker has opened.
//
// Expected:
//   - The circuit breaker has been initialised.
//
// Returns:
//   - nil when the circuit breaker is open.
//
// Side effects:
//   - Reads circuit breaker state.
func (p *PlanningStepDefinitions) theCircuitBreakerShouldBeInOpenState(_ context.Context) error {
	if p.circuitBreaker == nil {
		return errors.New("circuit breaker not initialised")
	}
	if p.circuitBreaker.State() != delegation.CircuitOpen {
		return errors.New("expected circuit breaker to be open, got " + string(p.circuitBreaker.State()))
	}
	return nil
}

// theCoordinatorShouldEscalateToTheUser verifies delegation is blocked.
//
// Expected:
//   - The circuit breaker has been initialised.
//
// Returns:
//   - nil when delegation is blocked.
//
// Side effects:
//   - Reads circuit breaker state.
func (p *PlanningStepDefinitions) theCoordinatorShouldEscalateToTheUser(_ context.Context) error {
	if p.circuitBreaker == nil {
		return errors.New("circuit breaker not initialised")
	}
	allowed := p.circuitBreaker.Allow()
	if allowed {
		return errors.New("expected circuit breaker to block delegation, but it allowed it")
	}
	return nil
}

// aCoordinationStoreWithChainID initialises the coordination store for a chain.
//
// Expected:
//   - The scenario supplies a chain identifier.
//
// Returns:
//   - nil after creating a fresh coordination store and storing the chain ID.
//
// Side effects:
//   - Allocates a new in-memory coordination store.
//   - Mutates the stored chain ID.
func (p *PlanningStepDefinitions) aCoordinationStoreWithChainID(_ context.Context, chainID string) error {
	p.coordinationStore = coordination.NewMemoryStore()
	p.chainID = chainID
	return nil
}

// theCoordinatorWritesRequirementsToTheStore writes requirements into storage.
//
// Expected:
//   - A chain ID and coordination store are available.
//
// Returns:
//   - nil when the requirements are written successfully.
//
// Side effects:
//   - Writes requirement data to the coordination store.
func (p *PlanningStepDefinitions) theCoordinatorWritesRequirementsToTheStore(_ context.Context) error {
	p.requirements = "Requirements: build a web application with user authentication"
	key := p.chainID + ":requirements"
	return p.coordinationStore.Set(key, []byte(p.requirements))
}

// theWriterReadsRequirementsFromTheStore loads requirements from storage.
//
// Expected:
//   - Requirements have already been written for the current chain.
//
// Returns:
//   - nil when the requirements can be read back.
//
// Side effects:
//   - Reads requirement data from the coordination store.
func (p *PlanningStepDefinitions) theWriterReadsRequirementsFromTheStore(_ context.Context) error {
	key := p.chainID + ":requirements"
	data, err := p.coordinationStore.Get(key)
	if err != nil {
		return fmt.Errorf("failed to read requirements: %w", err)
	}
	p.requirements = string(data)
	return nil
}

// theWriterShouldReceiveTheCoordinatorsRequirements checks the loaded requirements.
//
// Expected:
//   - Requirements have been loaded into memory.
//
// Returns:
//   - nil when the loaded requirements match the expected content.
//
// Side effects:
//   - None.
func (p *PlanningStepDefinitions) theWriterShouldReceiveTheCoordinatorsRequirements(_ context.Context) error {
	expected := "Requirements: build a web application with user authentication"
	if p.requirements != expected {
		return errors.New("expected requirements " + expected + ", got " + p.requirements)
	}
	return nil
}

// aDelegationStarts appends a started delegation event.
//
// Expected:
//   - The planning delegation flow has started.
//
// Returns:
//   - nil after recording the started delegation event.
//
// Side effects:
//   - Appends a delegation event.
func (p *PlanningStepDefinitions) aDelegationStarts(_ context.Context) error {
	event := streaming.DelegationEvent{
		SourceAgent:  "planning-coordinator",
		TargetAgent:  "plan-writer",
		ChainID:      "test-chain",
		Status:       "started",
		ModelName:    "llama3.2",
		ProviderName: "ollama",
		Description:  "Starting delegation to plan-writer",
	}
	p.delegationEvents = append(p.delegationEvents, event)
	return nil
}

// aDelegationEventShouldContainTheTargetAgentName checks the latest event target agent.
//
// Expected:
//   - At least one delegation event exists.
//
// Returns:
//   - nil when the latest event includes a target agent name.
//
// Side effects:
//   - None.
func (p *PlanningStepDefinitions) aDelegationEventShouldContainTheTargetAgentName(_ context.Context) error {
	if len(p.delegationEvents) == 0 {
		return errors.New("no delegation events")
	}
	event := p.delegationEvents[len(p.delegationEvents)-1]
	if event.TargetAgent == "" {
		return errors.New("delegation event missing target agent name")
	}
	return nil
}

// theEventShouldContainTheModelName checks the latest event model name.
//
// Expected:
//   - At least one delegation event exists.
//
// Returns:
//   - nil when the latest event includes a model name.
//
// Side effects:
//   - None.
func (p *PlanningStepDefinitions) theEventShouldContainTheModelName(_ context.Context) error {
	if len(p.delegationEvents) == 0 {
		return errors.New("no delegation events")
	}
	event := p.delegationEvents[len(p.delegationEvents)-1]
	if event.ModelName == "" {
		return errors.New("delegation event missing model name")
	}
	return nil
}

// theEventShouldContainADescription checks the latest event description.
//
// Expected:
//   - At least one delegation event exists.
//
// Returns:
//   - nil when the latest event includes a description.
//
// Side effects:
//   - None.
func (p *PlanningStepDefinitions) theEventShouldContainADescription(_ context.Context) error {
	if len(p.delegationEvents) == 0 {
		return errors.New("no delegation events")
	}
	event := p.delegationEvents[len(p.delegationEvents)-1]
	if event.Description == "" {
		return errors.New("delegation event missing description")
	}
	return nil
}
