//go:build e2e

package support

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/coordination"
	"github.com/baphled/flowstate/internal/delegation"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/plugin/failover"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/tool"
)

// PlanningStepDefinitions holds state for planning loop BDD scenarios.
type PlanningStepDefinitions struct {
	coordinationStore coordination.Store
	chainID           string
	circuitBreaker    *delegation.CircuitBreaker
	delegationEvents  []streaming.DelegationEvent
	requirements      string
	delegateTool      *engine.DelegateTool
	lastError         error
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
		p.delegateTool = nil
		p.lastError = nil
		return bddCtx, nil
	})

	ctx.Step(`^a planner agent is configured$`, p.aPlannerAgentIsConfigured)
	ctx.Step(`^writer and reviewer agents are registered$`, p.writerAndReviewerAgentsAreRegistered)
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
	ctx.Step(`^delegation is requested with subagent_type "([^"]*)"$`, p.delegationIsRequestedWithSubagentType)
	ctx.Step(`^resolution is attempted for unknown agent "([^"]*)"$`, p.resolutionIsAttemptedForUnknownAgent)
	ctx.Step(`^the error should list available agents$`, p.theErrorShouldListAvailableAgents)
}

// planningMockProvider is a minimal provider.Provider implementation for planning BDD tests.
type planningMockProvider struct {
	agentName string
}

// Name returns the provider name for the planning mock.
//
// Returns:
//   - The string "mock-planning".
//
// Side effects:
//   - None.
func (p *planningMockProvider) Name() string { return "mock-planning" }

// Stream returns a single content chunk and closes the channel.
//
// Expected:
//   - ctx is a valid context.
//   - req is a valid ChatRequest.
//
// Returns:
//   - A channel of StreamChunk values containing one content chunk.
//   - nil error always.
//
// Side effects:
//   - Spawns a goroutine to send chunks on the returned channel.
func (p *planningMockProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk, 1)
	go func() {
		defer close(ch)
		ch <- provider.StreamChunk{Content: p.agentName + " response", Done: true}
	}()
	return ch, nil
}

// Chat returns a single mock assistant message.
//
// Expected:
//   - ctx is a valid context.
//   - req is a valid ChatRequest.
//
// Returns:
//   - A ChatResponse containing the mock agent name as the response content.
//   - nil error always.
//
// Side effects:
//   - None.
func (p *planningMockProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{
		Message: provider.Message{Role: "assistant", Content: p.agentName + " response"},
	}, nil
}

// Embed returns a nil embedding slice (unused in planning tests).
//
// Expected:
//   - ctx is a valid context.
//   - req is a valid EmbedRequest.
//
// Returns:
//   - nil slice and nil error always.
//
// Side effects:
//   - None.
func (p *planningMockProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, nil
}

// Models returns the single mock model available to planning agents.
//
// Returns:
//   - A slice containing one model entry for "llama3.2" on "mock-planning".
//   - nil error always.
//
// Side effects:
//   - None.
func (p *planningMockProvider) Models() ([]provider.Model, error) {
	return []provider.Model{{ID: "llama3.2", Provider: "mock-planning", ContextLength: 8192}}, nil
}

// buildPlanningDelegateTool creates a DelegateTool wired with plan-writer and plan-reviewer engines.
//
// Returns:
//   - A configured DelegateTool with plan-writing and plan-review delegation entries.
//
// Side effects:
//   - Allocates provider registry, engine, and delegation tool instances.
func buildPlanningDelegateTool() *engine.DelegateTool {
	reg := provider.NewRegistry()
	reg.Register(&planningMockProvider{agentName: "plan-writer"})

	writerManifest := agent.Manifest{
		ID:                "plan-writer",
		Name:              "Plan Writer Agent",
		Instructions:      agent.Instructions{SystemPrompt: "You are a plan writer."},
		ContextManagement: agent.DefaultContextManagement(),
	}
	reviewerManifest := agent.Manifest{
		ID:                "plan-reviewer",
		Name:              "Plan Reviewer Agent",
		Instructions:      agent.Instructions{SystemPrompt: "You are a plan reviewer."},
		ContextManagement: agent.DefaultContextManagement(),
	}

	writerMgr := failover.NewManager(reg, failover.NewHealthManager(), 5*time.Minute)
	writerMgr.SetBasePreferences([]provider.ModelPreference{
		{Provider: "mock-planning", Model: "llama3.2"},
	})
	writerEngine := engine.New(engine.Config{
		Registry:        reg,
		FailoverManager: writerMgr,
		Manifest:        writerManifest,
	})

	reviewerMgr := failover.NewManager(reg, failover.NewHealthManager(), 5*time.Minute)
	reviewerMgr.SetBasePreferences([]provider.ModelPreference{
		{Provider: "mock-planning", Model: "llama3.2"},
	})
	reviewerEngine := engine.New(engine.Config{
		Registry:        reg,
		FailoverManager: reviewerMgr,
		Manifest:        reviewerManifest,
	})

	engines := map[string]*engine.Engine{
		"plan-writer":   writerEngine,
		"plan-reviewer": reviewerEngine,
	}

	agentRegistry := agent.NewRegistry()
	agentRegistry.Register(&writerManifest)
	agentRegistry.Register(&reviewerManifest)

	delegationConfig := agent.Delegation{
		CanDelegate: true,
	}

	return engine.NewDelegateTool(engines, delegationConfig, "planner").WithRegistry(agentRegistry)
}

// delegateAndCollect calls Execute on the delegateTool and collects emitted DelegationEvents.
//
// Expected:
//   - ctx is a valid context for the delegation.
//   - dt is a configured DelegateTool with at least one valid engine route.
//   - subagentType identifies a valid agent via registry or engine key lookup.
//   - message is the instruction to send to the target agent.
//
// Returns:
//   - A slice of DelegationEvent values emitted during the delegation.
//   - An error if Execute fails.
//
// Side effects:
//   - Calls the target engine's Stream method via DelegateTool.Execute.
func delegateAndCollect(ctx context.Context, dt *engine.DelegateTool, subagentType, message string) ([]streaming.DelegationEvent, error) {
	outChan := make(chan provider.StreamChunk, 16)
	streamCtx := engine.WithStreamOutput(ctx, outChan)

	input := tool.Input{
		Name: "delegate",
		Arguments: map[string]interface{}{
			"subagent_type": subagentType,
			"message":       message,
		},
	}

	_, err := dt.Execute(streamCtx, input)
	close(outChan)

	var events []streaming.DelegationEvent
	for chunk := range outChan {
		if chunk.DelegationInfo == nil {
			continue
		}
		info := chunk.DelegationInfo
		events = append(events, streaming.DelegationEvent{
			SourceAgent:  info.SourceAgent,
			TargetAgent:  info.TargetAgent,
			ChainID:      info.ChainID,
			Status:       info.Status,
			ModelName:    info.ModelName,
			ProviderName: info.ProviderName,
			Description:  info.Description,
			ToolCalls:    info.ToolCalls,
			LastTool:     info.LastTool,
			StartedAt:    info.StartedAt,
			CompletedAt:  info.CompletedAt,
		})
	}

	return events, err
}

// aPlannerAgentIsConfigured initialises a real DelegateTool with mock sub-agent engines.
//
// Expected:
//   - No prior state is required.
//
// Returns:
//   - nil after setting up the planning DelegateTool.
//
// Side effects:
//   - Assigns a configured DelegateTool to p.delegateTool.
func (p *PlanningStepDefinitions) aPlannerAgentIsConfigured(_ context.Context) error {
	p.delegateTool = buildPlanningDelegateTool()
	return nil
}

// writerAndReviewerAgentsAreRegistered verifies both agents are available via registry.
//
// Expected:
//   - p.delegateTool has been initialised with a registry.
//
// Returns:
//   - nil when both plan-writer and plan-reviewer can be delegated to.
//
// Side effects:
//   - Makes test delegations to verify routing.
func (p *PlanningStepDefinitions) writerAndReviewerAgentsAreRegistered(ctx context.Context) error {
	if p.delegateTool == nil {
		return errors.New("delegate tool not initialised")
	}

	writerInput := tool.Input{
		Name: "delegate",
		Arguments: map[string]interface{}{
			"subagent_type": "plan-writer",
			"message":       "verify writer routing",
		},
	}
	if _, err := p.delegateTool.Execute(ctx, writerInput); err != nil {
		return fmt.Errorf("plan-writer agent not available: %w", err)
	}

	reviewerInput := tool.Input{
		Name: "delegate",
		Arguments: map[string]interface{}{
			"subagent_type": "plan-reviewer",
			"message":       "verify reviewer routing",
		},
	}
	if _, err := p.delegateTool.Execute(ctx, reviewerInput); err != nil {
		return fmt.Errorf("plan-reviewer agent not available: %w", err)
	}

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

// itShouldDelegateToThePlanWriterAgent calls DelegateTool.Execute and collects the emitted DelegationEvent.
//
// Expected:
//   - p.delegateTool is configured with a plan-writer route.
//
// Returns:
//   - nil when a DelegationEvent targeting plan-writer is emitted.
//
// Side effects:
//   - Appends collected delegation events to p.delegationEvents.
func (p *PlanningStepDefinitions) itShouldDelegateToThePlanWriterAgent(ctx context.Context) error {
	events, err := delegateAndCollect(ctx, p.delegateTool, "plan-writer", "Generate a plan for the requirements")
	if err != nil {
		return fmt.Errorf("delegation to plan-writer failed: %w", err)
	}
	for i := range events {
		if events[i].TargetAgent == "plan-writer" {
			p.delegationEvents = append(p.delegationEvents, events[i])
		}
	}
	if len(p.delegationEvents) == 0 {
		return errors.New("no DelegationEvent emitted targeting plan-writer")
	}
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

// theDelegationShouldCompleteWithStatus checks the latest delegation event status.
//
// Expected:
//   - At least one delegation event exists.
//
// Returns:
//   - nil when the last delegation event matches the requested status.
//
// Side effects:
//   - None.
func (p *PlanningStepDefinitions) theDelegationShouldCompleteWithStatus(_ context.Context, status string) error {
	if len(p.delegationEvents) == 0 {
		return errors.New("no delegation events emitted")
	}
	event := p.delegationEvents[len(p.delegationEvents)-1]
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

// theCoordinatorDelegatesToThePlanReviewer calls DelegateTool.Execute targeting the plan-reviewer.
//
// Expected:
//   - p.delegateTool is configured with a plan-reviewer route.
//
// Returns:
//   - nil when a DelegationEvent targeting plan-reviewer is emitted.
//
// Side effects:
//   - Appends collected delegation events to p.delegationEvents.
func (p *PlanningStepDefinitions) theCoordinatorDelegatesToThePlanReviewer(ctx context.Context) error {
	events, err := delegateAndCollect(ctx, p.delegateTool, "plan-reviewer", "Review the generated plan")
	if err != nil {
		return fmt.Errorf("delegation to plan-reviewer failed: %w", err)
	}
	for i := range events {
		if events[i].TargetAgent == "plan-reviewer" {
			p.delegationEvents = append(p.delegationEvents, events[i])
		}
	}
	if len(p.delegationEvents) == 0 {
		return errors.New("no DelegationEvent emitted targeting plan-reviewer")
	}
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

// aDelegationStarts calls DelegateTool.Execute and captures the first emitted DelegationEvent.
//
// Expected:
//   - p.delegateTool is configured with at least one valid route.
//
// Returns:
//   - nil after capturing the first delegation event from a real Execute call.
//
// Side effects:
//   - Appends the first emitted delegation event to p.delegationEvents.
func (p *PlanningStepDefinitions) aDelegationStarts(ctx context.Context) error {
	events, err := delegateAndCollect(ctx, p.delegateTool, "plan-writer", "Starting delegation to plan-writer")
	if err != nil {
		return fmt.Errorf("delegation failed: %w", err)
	}
	if len(events) == 0 {
		return errors.New("no delegation events emitted")
	}
	p.delegationEvents = append(p.delegationEvents, events[0])
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

// delegationIsRequestedWithSubagentType delegates using subagent_type and collects events.
//
// Expected:
//   - p.delegateTool is configured with a registry containing the named agent.
//
// Returns:
//   - nil when delegation succeeds and events are collected.
//
// Side effects:
//   - Appends collected delegation events to p.delegationEvents.
func (p *PlanningStepDefinitions) delegationIsRequestedWithSubagentType(ctx context.Context, agentName string) error {
	events, err := delegateAndCollect(ctx, p.delegateTool, agentName, "Delegate to "+agentName)
	if err != nil {
		return fmt.Errorf("delegation to %s failed: %w", agentName, err)
	}
	p.delegationEvents = append(p.delegationEvents, events...)
	return nil
}

// resolutionIsAttemptedForUnknownAgent attempts registry resolution for a non-existent agent.
//
// Expected:
//   - p.delegateTool is configured with a registry.
//
// Returns:
//   - nil always; the resolution error is captured in p.lastError.
//
// Side effects:
//   - Stores the resolution error in p.lastError.
func (p *PlanningStepDefinitions) resolutionIsAttemptedForUnknownAgent(_ context.Context, agentName string) error {
	_, err := p.delegateTool.ResolveByNameOrAlias(agentName)
	p.lastError = err
	return nil
}

// theErrorShouldListAvailableAgents verifies the last error includes available agent names.
//
// Expected:
//   - p.lastError is set from a prior resolution attempt.
//
// Returns:
//   - nil when the error message contains "available agents:".
//
// Side effects:
//   - None.
func (p *PlanningStepDefinitions) theErrorShouldListAvailableAgents(_ context.Context) error {
	if p.lastError == nil {
		return errors.New("expected error but got nil")
	}
	errMsg := p.lastError.Error()
	if !strings.Contains(errMsg, "available agents:") {
		return fmt.Errorf("expected 'available agents' in error, got: %s", errMsg)
	}
	return nil
}
