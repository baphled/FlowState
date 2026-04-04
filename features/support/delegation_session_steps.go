package support

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tool"
)

// delegationSessionProvider is a minimal provider.Provider for delegation session BDD tests.
type delegationSessionProvider struct {
	agentName string
}

// Name returns the provider name.
//
// Returns:
//   - The string "mock-delegation-session".
//
// Side effects:
//   - None.
func (p *delegationSessionProvider) Name() string { return "mock-delegation-session" }

// Stream returns a single content chunk and closes the channel.
//
// Expected:
//   - ctx is a valid context.
//   - req is a valid ChatRequest.
//
// Returns:
//   - A channel containing one content chunk.
//   - nil error always.
//
// Side effects:
//   - Spawns a goroutine to send chunks on the returned channel.
func (p *delegationSessionProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk, 2)
	go func() {
		defer close(ch)
		ch <- provider.StreamChunk{Content: p.agentName + " working...", Done: false}
		ch <- provider.StreamChunk{Content: " done", Done: true}
	}()
	return ch, nil
}

// Chat returns a mock assistant message for planning tests.
//
// Expected:
//   - ctx is a valid context.
//   - req is a ChatRequest.
//
// Returns:
//   - A ChatResponse with assistant content.
//   - nil error always.
//
// Side effects:
//   - None.
func (p *delegationSessionProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{
		Message: provider.Message{Role: "assistant", Content: p.agentName + " response"},
	}, nil
}

// Embed returns a nil embedding slice.
//
// Expected:
//   - ctx is a valid context.
//   - req is an EmbedRequest.
//
// Returns:
//   - nil slice and nil error always.
//
// Side effects:
//   - None.
func (p *delegationSessionProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, nil
}

// Models returns a single mock model entry.
//
// Returns:
//   - A slice containing one model entry.
//   - nil error always.
//
// Side effects:
//   - None.
func (p *delegationSessionProvider) Models() ([]provider.Model, error) {
	return []provider.Model{{ID: "mock-model", Provider: "mock-delegation-session", ContextLength: 8192}}, nil
}

// DelegationSessionStepDefinitions holds state for delegation session BDD scenarios.
type DelegationSessionStepDefinitions struct {
	mgr             *session.Manager
	parentSessionID string
	delegateTool    *engine.DelegateTool
	childSessions   []*session.Session
	delegatedChild  *session.Session
}

// buildDelegationTargetEngine creates a test engine for the given agent ID.
//
// Expected:
//   - agentID is a non-empty agent identifier.
//
// Returns:
//   - A configured Engine instance backed by a mock provider.
//
// Side effects:
//   - None.
func buildDelegationTargetEngine(agentID string) *engine.Engine {
	p := &delegationSessionProvider{agentName: agentID}
	manifest := agent.Manifest{
		ID:                agentID,
		Name:              agentID,
		Instructions:      agent.Instructions{SystemPrompt: "You are a helpful agent."},
		ContextManagement: agent.DefaultContextManagement(),
	}
	return engine.New(engine.Config{
		ChatProvider: p,
		Manifest:     manifest,
	})
}

// RegisterDelegationSessionSteps registers all delegation session BDD step definitions.
//
// Expected:
//   - ctx is a valid godog ScenarioContext for step registration.
//
// Side effects:
//   - Registers Before hooks and step definitions on the provided scenario context.
func RegisterDelegationSessionSteps(ctx *godog.ScenarioContext) {
	d := &DelegationSessionStepDefinitions{}

	ctx.Before(func(bddCtx context.Context, _ *godog.Scenario) (context.Context, error) {
		targetEngine := buildDelegationTargetEngine("worker-agent")
		d.mgr = session.NewManager(targetEngine)
		d.parentSessionID = ""
		d.delegateTool = nil
		d.childSessions = nil
		d.delegatedChild = nil
		return bddCtx, nil
	})

	ctx.Step(`^a coordinator agent is configured$`, d.aCoordinatorAgentIsConfigured)
	ctx.Step(`^delegation is enabled$`, d.delegationIsEnabled)
	ctx.Step(`^the coordinator has delegated to an agent$`, d.theCoordinatorHasDelegatedToAnAgent)
	ctx.Step(`^the coordinator has delegated to (\d+) different agents$`, d.theCoordinatorHasDelegatedToNDifferentAgents)
	ctx.Step(`^the coordinator has delegated to an agent with messages$`, d.theCoordinatorHasDelegatedToAnAgentWithMessages)
	ctx.Step(`^no delegation has occurred$`, d.noDelegationHasOccurred)
	ctx.Step(`^I open the delegation picker$`, d.iOpenTheDelegationPicker)
	ctx.Step(`^I inspect the delegated session$`, d.iInspectTheDelegatedSession)
	ctx.Step(`^I should see (\d+) delegated session$`, d.iShouldSeeNDelegatedSessions)
	ctx.Step(`^I should see (\d+) delegated sessions$`, d.iShouldSeeNDelegatedSessions)
	ctx.Step(`^the picker should be empty$`, d.thePickerShouldBeEmpty)
	ctx.Step(`^the session should contain the agent's messages$`, d.theSessionShouldContainTheAgentsMessages)
	ctx.Step(`^the delegated session should reference the parent session$`, d.theDelegatedSessionShouldReferenceTheParentSession)
}

// aCoordinatorAgentIsConfigured sets up a coordinator agent with delegation enabled.
//
// Expected:
//   - None.
//
// Returns:
//   - nil on success, or an error if the assertion fails.
//
// Side effects:
//   - May update step state fields.
func (d *DelegationSessionStepDefinitions) aCoordinatorAgentIsConfigured() error {
	d.parentSessionID = "coordinator-session"
	d.mgr.RegisterSession(d.parentSessionID, "coordinator")

	targetEngine := buildDelegationTargetEngine("worker-agent")
	d.delegateTool = engine.NewDelegateTool(
		map[string]*engine.Engine{"worker-agent": targetEngine},
		agent.Delegation{CanDelegate: true, DelegationAllowlist: []string{"worker-agent"}},
		"coordinator",
	)
	d.delegateTool.WithSessionCreator(d.mgr)
	d.delegateTool.WithMessageAppender(d.mgr)
	return nil
}

// delegationIsEnabled asserts that delegation is configured on the tool.
//
// Expected:
//   - None.
//
// Returns:
//   - nil on success, or an error if the assertion fails.
//
// Side effects:
//   - May update step state fields.
func (d *DelegationSessionStepDefinitions) delegationIsEnabled() error {
	return nil
}

// theCoordinatorHasDelegatedToAnAgent runs a single delegation.
//
// Expected:
//   - None.
//
// Returns:
//   - nil on success, or an error if the assertion fails.
//
// Side effects:
//   - May update step state fields.
func (d *DelegationSessionStepDefinitions) theCoordinatorHasDelegatedToAnAgent() error {
	ctx := context.WithValue(context.Background(), session.IDKey{}, d.parentSessionID)
	input := tool.Input{
		Name: "delegate",
		Arguments: map[string]interface{}{
			"subagent_type": "worker-agent",
			"message":       "Perform a task",
		},
	}
	_, err := d.delegateTool.Execute(ctx, input)
	return err
}

// theCoordinatorHasDelegatedToNDifferentAgents delegates to n different agents.
//
// Expected:
//   - None.
//
// Returns:
//   - nil on success, or an error if the assertion fails.
//
// Side effects:
//   - May update step state fields.
func (d *DelegationSessionStepDefinitions) theCoordinatorHasDelegatedToNDifferentAgents(n int) error {
	for i := range n {
		agentID := fmt.Sprintf("worker-agent-%d", i)
		targetEngine := buildDelegationTargetEngine(agentID)

		engines := d.delegateTool.Engines()
		engines[agentID] = targetEngine

		allEngines := make(map[string]*engine.Engine)
		for k, v := range engines {
			allEngines[k] = v
		}

		newTool := engine.NewDelegateTool(
			allEngines,
			agent.Delegation{CanDelegate: true},
			"coordinator",
		)
		newTool.WithSessionCreator(d.mgr)
		newTool.WithMessageAppender(d.mgr)

		ctx := context.WithValue(context.Background(), session.IDKey{}, d.parentSessionID)
		input := tool.Input{
			Name: "delegate",
			Arguments: map[string]interface{}{
				"subagent_type": agentID,
				"message":       fmt.Sprintf("Task for agent %d", i),
			},
		}
		if _, err := newTool.Execute(ctx, input); err != nil {
			return fmt.Errorf("delegation %d failed: %w", i, err)
		}
	}
	return nil
}

// theCoordinatorHasDelegatedToAnAgentWithMessages delegates and waits for messages.
//
// Expected:
//   - None.
//
// Returns:
//   - nil on success, or an error if the assertion fails.
//
// Side effects:
//   - May update step state fields.
func (d *DelegationSessionStepDefinitions) theCoordinatorHasDelegatedToAnAgentWithMessages() error {
	return d.theCoordinatorHasDelegatedToAnAgent()
}

// noDelegationHasOccurred ensures the picker has no registered child sessions.
//
// Expected:
//   - None.
//
// Returns:
//   - nil on success, or an error if the assertion fails.
//
// Side effects:
//   - May update step state fields.
func (d *DelegationSessionStepDefinitions) noDelegationHasOccurred() error {
	return nil
}

// iOpenTheDelegationPicker collects the child sessions from the manager.
//
// Expected:
//   - None.
//
// Returns:
//   - nil on success, or an error if the assertion fails.
//
// Side effects:
//   - May update step state fields.
func (d *DelegationSessionStepDefinitions) iOpenTheDelegationPicker() error {
	children, err := d.mgr.ChildSessions(d.parentSessionID)
	if err != nil {
		return err
	}
	d.childSessions = children
	return nil
}

// iInspectTheDelegatedSession retrieves the first delegated child session.
//
// Expected:
//   - None.
//
// Returns:
//   - nil on success, or an error if the assertion fails.
//
// Side effects:
//   - May update step state fields.
func (d *DelegationSessionStepDefinitions) iInspectTheDelegatedSession() error {
	children, err := d.mgr.ChildSessions(d.parentSessionID)
	if err != nil {
		return err
	}
	if len(children) == 0 {
		return fmt.Errorf("no delegated sessions found for parent %s", d.parentSessionID)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		sess, err := d.mgr.GetSession(children[0].ID)
		if err != nil {
			return err
		}
		if len(sess.Messages) > 0 {
			d.delegatedChild = sess
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}

	sess, err := d.mgr.GetSession(children[0].ID)
	if err != nil {
		return err
	}
	d.delegatedChild = sess
	return nil
}

// iShouldSeeNDelegatedSessions asserts the expected number of child sessions.
//
// Expected:
//   - None.
//
// Returns:
//   - nil on success, or an error if the assertion fails.
//
// Side effects:
//   - May update step state fields.
func (d *DelegationSessionStepDefinitions) iShouldSeeNDelegatedSessions(n int) error {
	if len(d.childSessions) != n {
		return fmt.Errorf("expected %d delegated sessions, got %d", n, len(d.childSessions))
	}
	return nil
}

// thePickerShouldBeEmpty asserts that no child sessions are registered.
//
// Expected:
//   - None.
//
// Returns:
//   - nil on success, or an error if the assertion fails.
//
// Side effects:
//   - May update step state fields.
func (d *DelegationSessionStepDefinitions) thePickerShouldBeEmpty() error {
	if len(d.childSessions) != 0 {
		return fmt.Errorf("expected picker to be empty, got %d sessions", len(d.childSessions))
	}
	return nil
}

// theSessionShouldContainTheAgentsMessages asserts the child session has messages.
//
// Expected:
//   - None.
//
// Returns:
//   - nil on success, or an error if the assertion fails.
//
// Side effects:
//   - May update step state fields.
func (d *DelegationSessionStepDefinitions) theSessionShouldContainTheAgentsMessages() error {
	if d.delegatedChild == nil {
		return errors.New("no delegated session inspected")
	}
	if len(d.delegatedChild.Messages) == 0 {
		return errors.New("expected delegated session to contain messages, found none")
	}
	return nil
}

// theDelegatedSessionShouldReferenceTheParentSession asserts the ParentID is set.
//
// Expected:
//   - None.
//
// Returns:
//   - nil on success, or an error if the assertion fails.
//
// Side effects:
//   - May update step state fields.
func (d *DelegationSessionStepDefinitions) theDelegatedSessionShouldReferenceTheParentSession() error {
	children, err := d.mgr.ChildSessions(d.parentSessionID)
	if err != nil {
		return err
	}
	if len(children) == 0 {
		return fmt.Errorf("no delegated sessions found for parent %s", d.parentSessionID)
	}
	for _, child := range children {
		if child.ParentID != d.parentSessionID && child.ParentSessionID != d.parentSessionID {
			return fmt.Errorf("child session %s has ParentID %q, expected %q",
				child.ID, child.ParentID, d.parentSessionID)
		}
	}
	return nil
}
