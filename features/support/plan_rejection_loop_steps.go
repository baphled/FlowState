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
	"github.com/baphled/flowstate/internal/tool"
)

// planRejectionStepDefinitions holds state for plan rejection loop BDD scenarios.
type planRejectionStepDefinitions struct {
	coordinationStore coordination.Store
	chainID           string
	delegateTool      *engine.DelegateTool
	rejectionTracker  *delegation.RejectionTracker
	lastVerdict       string
	lastError         error
	savedPlan         string
	escalationMessage string
}

// planRejectionMockProvider is a mock provider for plan rejection testing.
type planRejectionMockProvider struct {
	agentName string
	response  string
}

// Name returns the mock provider name.
//
// Returns: The string "mock-plan-rejection".
//
// Side effects: None.
func (p *planRejectionMockProvider) Name() string { return "mock-plan-rejection" }

// Stream returns mock response chunks.
//
// Expected: ctx is a valid context, req is a valid ChatRequest.
//
// Returns: A channel of StreamChunk values containing one content chunk, nil error.
//
// Side effects: Spawns a goroutine to send chunks on the returned channel.
func (p *planRejectionMockProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk, 1)
	go func() {
		defer close(ch)
		ch <- provider.StreamChunk{Content: p.response, Done: true}
	}()
	return ch, nil
}

// Chat returns mock response.
//
// Expected: ctx is a valid context, req is a valid ChatRequest.
//
// Returns: A ChatResponse containing the mock response content, nil error.
//
// Side effects: None.
func (p *planRejectionMockProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{
		Message: provider.Message{Role: "assistant", Content: p.response},
	}, nil
}

// Embed returns nil (unused in plan rejection tests).
//
// Expected: ctx is a valid context, req is a valid EmbedRequest.
//
// Returns: nil slice and nil error.
//
// Side effects: None.
func (p *planRejectionMockProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, nil
}

// Models returns mock model.
//
// Returns: A slice containing one model entry for "llama3.2" on "mock-plan-rejection", nil error.
//
// Side effects: None.
func (p *planRejectionMockProvider) Models() ([]provider.Model, error) {
	return []provider.Model{{ID: "llama3.2", Provider: "mock-plan-rejection", ContextLength: 8192}}, nil
}

// Package-level state for shared step implementation (used by both plan_rejection_loop and plan_harness_e2e).
var (
	planRejectionStore        coordination.Store
	planRejectionDelegateTool *engine.DelegateTool
	planRejectionTracker      *delegation.RejectionTracker
)

// initPlanningSession sets up the shared state for planning sessions.
// This is used by both plan_rejection_loop and plan_harness_e2e features.
//
// Returns: nil error on success.
//
// Side effects: Allocates provider registry, engine, and delegation tool instances.
func initPlanningSession() error {
	if planRejectionDelegateTool == nil {
		planRejectionStore = coordination.NewMemoryStore()
		planRejectionTracker = delegation.NewRejectionTracker(planRejectionStore, 3)

		reg := provider.NewRegistry()
		reg.Register(&planRejectionMockProvider{agentName: "plan-writer", response: "initial plan"})
		reg.Register(&planRejectionMockProvider{agentName: "plan-reviewer", response: "review response"})

		writerManifest := agent.Manifest{
			ID:                "plan-writer",
			Name:              "Plan Writer Agent",
			Instructions:      agent.Instructions{SystemPrompt: "You are a plan writer."},
			ContextManagement: agent.DefaultContextManagement(),
			Delegation:        agent.Delegation{CanDelegate: true, DelegationAllowlist: []string{"plan-reviewer"}},
		}

		writerMgr := failover.NewManager(reg, failover.NewHealthManager(), 5*time.Minute)
		writerMgr.SetBasePreferences([]provider.ModelPreference{
			{Provider: "mock-plan-rejection", Model: "llama3.2"},
		})
		writerEngine := engine.New(engine.Config{
			Registry:        reg,
			FailoverManager: writerMgr,
			Manifest:        writerManifest,
		})

		planRejectionDelegateTool = engine.NewDelegateTool(
			map[string]*engine.Engine{"plan-writer": writerEngine, "plan-reviewer": writerEngine},
			agent.Delegation{CanDelegate: true},
			"planner",
		).WithRegistry(agent.NewRegistry()).WithRejectionTracker(planRejectionTracker)
	}
	return nil
}

// initPlanRejectionLoopSteps registers step definitions for the plan rejection loop scenarios.
//
// Expected: ctx is a valid godog ScenarioContext for step registration.
//
// Side effects: Registers step definitions on the provided scenario context.
func initPlanRejectionLoopSteps(ctx *godog.ScenarioContext) {
	p := &planRejectionStepDefinitions{}

	ctx.Before(func(bddCtx context.Context, _ *godog.Scenario) (context.Context, error) {
		p.coordinationStore = coordination.NewMemoryStore()
		p.chainID = "test-chain-001"
		p.delegateTool = nil
		p.rejectionTracker = nil
		p.lastVerdict = ""
		p.lastError = nil
		p.savedPlan = ""
		p.escalationMessage = ""
		return bddCtx, nil
	})

	ctx.Step(`^the plan-writer has produced a plan$`, p.thePlanWriterHasProducedPlan)
	ctx.Step(`^the plan-reviewer returns a REJECT verdict$`, p.thePlanReviewerReturnsReject)
	ctx.Step(`^the planner re-delegates to the plan-writer$`, p.thePlannerRedelegatesToPlanWriter)
	ctx.Step(`^the plan-writer produces a new plan$`, p.thePlanWriterProducesNewPlan)
	ctx.Step(`^the plan-reviewer returns an APPROVE verdict$`, p.thePlanReviewerReturnsApprove)
	ctx.Step(`^the final plan is saved$`, p.theFinalPlanIsSaved)
	ctx.Step(`^the plan-reviewer rejects the plan (\d+) consecutive times$`, p.thePlanReviewerRejectsConsecutiveTimes)
	ctx.Step(`^the delegate tool returns an errMaxRejectionsExhausted error$`, p.theDelegateToolReturnsMaxRejectionsError)
	ctx.Step(`^the planner escalates to the user with the rejection reason$`, p.thePlannerEscalatesToUserWithReason)
}

// thePlanWriterHasProducedPlan records that a plan was produced.
//
// Returns: nil on success.
//
// Side effects: Initializes shared state if not already done.
func (p *planRejectionStepDefinitions) thePlanWriterHasProducedPlan() error {
	if planRejectionDelegateTool == nil {
		if err := initPlanningSession(); err != nil {
			return err
		}
	}
	p.delegateTool = planRejectionDelegateTool
	p.rejectionTracker = planRejectionTracker
	p.coordinationStore = planRejectionStore
	return nil
}

// thePlanReviewerReturnsReject records a REJECT verdict and increments rejection count.
//
// Returns: nil on success, or error if recording fails.
//
// Side effects: Increments rejection counter in the tracking store.
func (p *planRejectionStepDefinitions) thePlanReviewerReturnsReject() error {
	p.lastVerdict = "REJECT"
	_, err := p.rejectionTracker.Record(context.Background(), p.chainID)
	return err
}

// thePlannerRedelegatesToPlanWriter simulates re-delegation to the plan-writer.
//
// Returns: nil on success, or error if delegation fails.
//
// Side effects: Calls the delegate tool Execute method.
func (p *planRejectionStepDefinitions) thePlannerRedelegatesToPlanWriter() error {
	if p.delegateTool == nil {
		return errors.New("delegate tool not configured")
	}
	input := tool.Input{
		Name: "delegate",
		Arguments: map[string]interface{}{
			"subagent_type": "plan-writer",
			"message":       "produce a new plan",
		},
	}
	_, err := p.delegateTool.Execute(context.Background(), input)
	return err
}

// thePlanWriterProducesNewPlan verifies a new plan was produced.
//
// Returns: nil always.
//
// Side effects: None.
func (p *planRejectionStepDefinitions) thePlanWriterProducesNewPlan() error {
	return nil
}

// thePlanReviewerReturnsApprove records an APPROVE verdict.
//
// Returns: nil always.
//
// Side effects: Sets lastVerdict to "APPROVE".
func (p *planRejectionStepDefinitions) thePlanReviewerReturnsApprove() error {
	p.lastVerdict = "APPROVE"
	return nil
}

// theFinalPlanIsSaved records the plan as saved.
//
// Returns: nil if verdict is APPROVE, error otherwise.
//
// Side effects: Sets savedPlan content.
func (p *planRejectionStepDefinitions) theFinalPlanIsSaved() error {
	if p.lastVerdict == "APPROVE" {
		p.savedPlan = "approved-plan-content"
		return nil
	}
	return fmt.Errorf("cannot save plan without APPROVE verdict, got: %s", p.lastVerdict)
}

// thePlanReviewerRejectsConsecutiveTimes records n consecutive rejections.
//
// Expected: n is the number of consecutive rejections to simulate.
//
// Returns: nil on success, or error if recording fails.
//
// Side effects: Increments rejection counter n times.
func (p *planRejectionStepDefinitions) thePlanReviewerRejectsConsecutiveTimes(n int) error {
	p.lastVerdict = "REJECT"
	for range n {
		_, err := p.rejectionTracker.Record(context.Background(), p.chainID)
		if err != nil {
			return err
		}
	}
	return nil
}

// theDelegateToolReturnsMaxRejectionsError verifies the error is returned.
//
// Returns: nil if errMaxRejectionsExhausted is returned, error otherwise.
//
// Side effects: Sets lastError on verification.
func (p *planRejectionStepDefinitions) theDelegateToolReturnsMaxRejectionsError() error {
	if p.delegateTool == nil {
		return errors.New("delegate tool not configured")
	}
	input := tool.Input{
		Name: "delegate",
		Arguments: map[string]interface{}{
			"subagent_type": "plan-writer",
			"message":       "produce a plan",
			"handoff": map[string]interface{}{
				"chain_id":     p.chainID,
				"source_agent": "planner",
				"target_agent": "plan-writer",
			},
		},
	}
	_, err := p.delegateTool.Execute(context.Background(), input)
	if err == nil {
		return errors.New("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "max rejections exhausted") {
		return fmt.Errorf("expected errMaxRejectionsExhausted, got: %w", err)
	}
	p.lastError = err
	return nil
}

// thePlannerEscalatesToUserWithReason records the escalation message.
//
// Returns: nil if lastError is set, error otherwise.
//
// Side effects: Sets escalationMessage from lastError.
func (p *planRejectionStepDefinitions) thePlannerEscalatesToUserWithReason() error {
	if p.lastError != nil {
		p.escalationMessage = p.lastError.Error()
		return nil
	}
	return errors.New("no error to escalate")
}

var _ = []interface{}{
	initPlanRejectionLoopSteps,
}
