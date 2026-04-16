package support

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/streaming"
	swarmactivity "github.com/baphled/flowstate/internal/tui/components/swarm_activity"
	"github.com/baphled/flowstate/internal/tui/intents/chat"
	"github.com/baphled/flowstate/internal/tui/uikit/navigation"
)

// e2eMultiAgentChatSteps holds state for the end-to-end multi-agent chat UX
// BDD scenarios. It composes components from all three waves (dual-pane
// layout, session trail, swarm activity timeline) into a single cohesive
// journey test.
type e2eMultiAgentChatSteps struct {
	// Wave 1: Chat intent and dual-pane layout.
	chatIntent     *chat.Intent
	terminalWidth  int
	terminalHeight int
	lastRender     string

	// Wave 2: Session trail.
	trail       *navigation.SessionTrail
	trailRender string

	// Wave 3: Swarm activity pane.
	pane       *swarmactivity.SwarmActivityPane
	paneRender string
}

// RegisterMultiAgentChatUXE2ESteps registers step definitions for the
// end-to-end multi-agent chat UX BDD scenarios.
//
// Expected:
//   - sc is a valid godog ScenarioContext.
//
// Side effects:
//   - Registers all E2E step definitions on the scenario context.
//   - Resets per-scenario state via a Before hook.
func RegisterMultiAgentChatUXE2ESteps(sc *godog.ScenarioContext) {
	s := &e2eMultiAgentChatSteps{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return ctx, nil
	})
	registerE2EWave1Steps(sc, s)
	registerE2EWave2Steps(sc, s)
	registerE2EWave3Steps(sc, s)
}

// registerE2EWave1Steps wires up the Wave 1 dual-pane layout and toggle
// steps for the E2E scenario.
//
// Expected:
//   - sc is a valid godog ScenarioContext; s is non-nil.
//
// Side effects:
//   - Registers Wave 1 step definitions on the scenario context.
func registerE2EWave1Steps(sc *godog.ScenarioContext, s *e2eMultiAgentChatSteps) {
	sc.Step(`^a chat intent with a (\d+)x(\d+) terminal$`, s.aChatIntentWithTerminal)
	sc.Step(`^the swarm activity pane is visible by default$`, s.theSwarmActivityPaneIsVisibleByDefault)
	sc.Step(`^the operator views the chat$`, s.theOperatorViewsTheChat)
	sc.Step(`^the output shows a dual-pane layout$`, s.theOutputShowsADualPaneLayout)
	sc.Step(`^the e2e operator toggles the activity pane$`, s.theE2EOperatorTogglesTheActivityPane)
	sc.Step(`^the activity pane is hidden$`, s.theActivityPaneIsHidden)
	sc.Step(`^the activity pane is restored$`, s.theActivityPaneIsRestored)
}

// registerE2EWave2Steps wires up the Wave 2 session trail steps for the
// E2E scenario.
//
// Expected:
//   - sc is a valid godog ScenarioContext; s is non-nil.
//
// Side effects:
//   - Registers Wave 2 step definitions on the scenario context.
func registerE2EWave2Steps(sc *godog.ScenarioContext, s *e2eMultiAgentChatSteps) {
	sc.Step(`^a session hierarchy with root "([^"]*)" and child "([^"]*)"$`, s.aSessionHierarchyWithRootAndChild)
	sc.Step(`^the session trail is rendered for the hierarchy$`, s.theSessionTrailIsRenderedForHierarchy)
	sc.Step(`^the session trail shows "([^"]*)"$`, s.theSessionTrailShows)
}

// registerE2EWave3Steps wires up the Wave 3 swarm activity timeline and
// filtering steps for the E2E scenario.
//
// Expected:
//   - sc is a valid godog ScenarioContext; s is non-nil.
//
// Side effects:
//   - Registers Wave 3 step definitions on the scenario context.
func registerE2EWave3Steps(sc *godog.ScenarioContext, s *e2eMultiAgentChatSteps) {
	sc.Step(`^a swarm activity pane with a delegation event for "([^"]*)"$`, s.aSwarmActivityPaneWithDelegationEvent)
	sc.Step(`^the activity pane is rendered for the e2e scenario$`, s.theActivityPaneIsRenderedForE2E)
	sc.Step(`^the timeline shows "([^"]*)"$`, s.theTimelineShows)
	sc.Step(`^delegation events are hidden via filter$`, s.delegationEventsAreHiddenViaFilter)
	sc.Step(`^the timeline does not show delegation events$`, s.theTimelineDoesNotShowDelegationEvents)
	sc.Step(`^the count shows filtered results$`, s.theCountShowsFilteredResults)
}

// --- Wave 1: Dual-pane layout and toggle steps ---

// aChatIntentWithTerminal creates a chat.Intent configured with the specified
// terminal dimensions and sends an initial WindowSizeMsg.
//
// Expected:
//   - width and height are positive integers.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Populates s.chatIntent, s.terminalWidth, s.terminalHeight.
func (s *e2eMultiAgentChatSteps) aChatIntentWithTerminal(width, height int) error {
	s.terminalWidth = width
	s.terminalHeight = height
	s.chatIntent = chat.NewIntent(chat.IntentConfig{
		AgentID:      "e2e-agent",
		SessionID:    "e2e-session",
		ProviderName: "openai",
		ModelName:    "gpt-4o",
		TokenBudget:  4096,
	})
	s.chatIntent.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return nil
}

// theSwarmActivityPaneIsVisibleByDefault asserts the chat Intent renders
// the activity timeline header on initial view.
//
// Returns:
//   - nil if the header is present; error otherwise.
//
// Side effects:
//   - Captures s.lastRender.
func (s *e2eMultiAgentChatSteps) theSwarmActivityPaneIsVisibleByDefault() error {
	if s.chatIntent == nil {
		return errors.New("chat intent has not been created")
	}
	view := s.chatIntent.View()
	s.lastRender = view
	if !strings.Contains(view, activityTimelineHeader) {
		return fmt.Errorf("expected %q in initial chat view", activityTimelineHeader)
	}
	return nil
}

// theOperatorViewsTheChat refreshes the cached render from the chat Intent.
//
// Returns:
//   - nil on success; error if no intent exists.
//
// Side effects:
//   - Updates s.lastRender.
func (s *e2eMultiAgentChatSteps) theOperatorViewsTheChat() error {
	if s.chatIntent == nil {
		return errors.New("chat intent has not been created")
	}
	s.lastRender = s.chatIntent.View()
	return nil
}

// theOutputShowsADualPaneLayout asserts the cached render contains the
// dual-pane separator and the activity timeline header.
//
// Returns:
//   - nil if both indicators are present; error otherwise.
//
// Side effects:
//   - None.
func (s *e2eMultiAgentChatSteps) theOutputShowsADualPaneLayout() error {
	if s.lastRender == "" {
		return errors.New("no render captured")
	}
	if !strings.Contains(s.lastRender, dualPaneSeparator) {
		return fmt.Errorf("expected dual-pane separator %q in output", dualPaneSeparator)
	}
	if !strings.Contains(s.lastRender, activityTimelineHeader) {
		return fmt.Errorf("expected %q in output", activityTimelineHeader)
	}
	return nil
}

// theActivityPaneIsHidden asserts the chat Intent view no longer contains
// the activity timeline header after a Ctrl+T toggle.
//
// Returns:
//   - nil if the header is absent; error otherwise.
//
// Side effects:
//   - None.
func (s *e2eMultiAgentChatSteps) theActivityPaneIsHidden() error {
	if s.chatIntent == nil {
		return errors.New("chat intent has not been created")
	}
	view := s.chatIntent.View()
	s.lastRender = view
	if strings.Contains(view, activityTimelineHeader) {
		return fmt.Errorf("expected %q to be absent after toggle", activityTimelineHeader)
	}
	return nil
}

// theE2EOperatorTogglesTheActivityPane sends a Ctrl+T key to the chat Intent
// to toggle the activity pane visibility.
//
// Returns:
//   - nil on success; error if no intent exists.
//
// Side effects:
//   - Sends KeyCtrlT to the intent and updates s.lastRender.
func (s *e2eMultiAgentChatSteps) theE2EOperatorTogglesTheActivityPane() error {
	if s.chatIntent == nil {
		return errors.New("chat intent has not been created")
	}
	s.chatIntent.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	s.lastRender = s.chatIntent.View()
	return nil
}

// theActivityPaneIsRestored asserts the activity timeline header is visible
// again after the second Ctrl+T toggle.
//
// Returns:
//   - nil if the header is present; error otherwise.
//
// Side effects:
//   - None.
func (s *e2eMultiAgentChatSteps) theActivityPaneIsRestored() error {
	if s.chatIntent == nil {
		return errors.New("chat intent has not been created")
	}
	view := s.chatIntent.View()
	s.lastRender = view
	if !strings.Contains(view, activityTimelineHeader) {
		return fmt.Errorf("expected %q in view after second Ctrl+T", activityTimelineHeader)
	}
	return nil
}

// --- Wave 2: Session trail steps ---

// aSessionHierarchyWithRootAndChild creates a 2-level session trail.
//
// Expected:
//   - root and child are non-empty label strings.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Populates s.trail.
func (s *e2eMultiAgentChatSteps) aSessionHierarchyWithRootAndChild(root, child string) error {
	items := []navigation.SessionTrailItem{
		{SessionID: "sess-root", AgentID: "agent-root", Label: root},
		{SessionID: "sess-child", AgentID: "agent-child", Label: child},
	}
	s.trail = navigation.NewSessionTrail().WithItems(items)
	return nil
}

// theSessionTrailIsRenderedForHierarchy renders the session trail at width 80.
//
// Returns:
//   - nil on success; error if trail was not initialised.
//
// Side effects:
//   - Captures s.trailRender.
func (s *e2eMultiAgentChatSteps) theSessionTrailIsRenderedForHierarchy() error {
	if s.trail == nil {
		return errors.New("session trail has not been initialised")
	}
	s.trailRender = s.trail.Render(80)
	return nil
}

// theSessionTrailShows asserts the trail render matches the expected string.
//
// Expected:
//   - expected is the exact trail output string.
//
// Returns:
//   - nil if matching; error otherwise.
//
// Side effects:
//   - None.
func (s *e2eMultiAgentChatSteps) theSessionTrailShows(expected string) error {
	if s.trailRender == "" {
		return errors.New("session trail has not been rendered")
	}
	if s.trailRender != expected {
		return fmt.Errorf("expected trail %q, got %q", expected, s.trailRender)
	}
	return nil
}

// --- Wave 3: Swarm activity timeline and filtering steps ---

// aSwarmActivityPaneWithDelegationEvent creates a SwarmActivityPane containing
// a single delegation event for the named agent.
//
// Expected:
//   - agent is a non-empty agent ID string.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Populates s.pane.
func (s *e2eMultiAgentChatSteps) aSwarmActivityPaneWithDelegationEvent(agent string) error {
	events := []streaming.SwarmEvent{
		{
			ID:        "e2e-ev-1",
			Type:      streaming.EventDelegation,
			Status:    "started",
			Timestamp: time.Now(),
			AgentID:   agent,
			Metadata:  map[string]interface{}{"source": "orchestrator"},
		},
	}
	s.pane = swarmactivity.NewSwarmActivityPane().WithEvents(events)
	return nil
}

// theActivityPaneIsRenderedForE2E renders the swarm activity pane at 80x20.
//
// Returns:
//   - nil on success; error if pane was not initialised.
//
// Side effects:
//   - Captures s.paneRender.
func (s *e2eMultiAgentChatSteps) theActivityPaneIsRenderedForE2E() error {
	if s.pane == nil {
		return errors.New("SwarmActivityPane has not been initialised")
	}
	s.paneRender = s.pane.Render(80, 20)
	return nil
}

// theTimelineShows asserts the rendered pane contains the expected substring.
//
// Expected:
//   - expected is a non-empty substring to find in the pane render.
//
// Returns:
//   - nil if found; error otherwise.
//
// Side effects:
//   - None.
func (s *e2eMultiAgentChatSteps) theTimelineShows(expected string) error {
	if s.paneRender == "" {
		return errors.New("activity pane has not been rendered")
	}
	if !strings.Contains(s.paneRender, expected) {
		return fmt.Errorf("expected timeline to contain %q, got:\n%s", expected, s.paneRender)
	}
	return nil
}

// delegationEventsAreHiddenViaFilter applies a visibility filter that hides
// delegation events while keeping all others visible.
//
// Returns:
//   - nil on success; error if pane was not initialised.
//
// Side effects:
//   - Applies WithVisibleTypes to the pane, hiding delegation events.
func (s *e2eMultiAgentChatSteps) delegationEventsAreHiddenViaFilter() error {
	if s.pane == nil {
		return errors.New("SwarmActivityPane has not been initialised")
	}
	filter := map[streaming.SwarmEventType]bool{
		streaming.EventDelegation: false,
		streaming.EventToolCall:   true,
		streaming.EventPlan:       true,
		streaming.EventReview:     true,
	}
	s.pane.WithVisibleTypes(filter)
	return nil
}

// theTimelineDoesNotShowDelegationEvents asserts the rendered pane does not
// contain the delegation event marker.
//
// Returns:
//   - nil if absent; error otherwise.
//
// Side effects:
//   - None.
func (s *e2eMultiAgentChatSteps) theTimelineDoesNotShowDelegationEvents() error {
	if s.paneRender == "" {
		return errors.New("activity pane has not been rendered")
	}
	if strings.Contains(s.paneRender, "▸ delegation") {
		return fmt.Errorf("expected delegation events to be hidden, but found in render:\n%s", s.paneRender)
	}
	return nil
}

// theCountShowsFilteredResults asserts the rendered pane contains a
// "showing X of Y" count summary indicating that filtering is active.
//
// Returns:
//   - nil if found; error otherwise.
//
// Side effects:
//   - None.
func (s *e2eMultiAgentChatSteps) theCountShowsFilteredResults() error {
	if s.paneRender == "" {
		return errors.New("activity pane has not been rendered")
	}
	if !strings.Contains(s.paneRender, "showing") || !strings.Contains(s.paneRender, "of") {
		return fmt.Errorf("expected count summary 'showing X of Y' in render, got:\n%s", s.paneRender)
	}
	return nil
}

// reset clears all per-scenario state. Called from a Before hook so fixtures
// do not leak between scenarios.
//
// Side effects:
//   - Clears every field on the receiver.
func (s *e2eMultiAgentChatSteps) reset() {
	s.chatIntent = nil
	s.terminalWidth = 0
	s.terminalHeight = 0
	s.lastRender = ""
	s.trail = nil
	s.trailRender = ""
	s.pane = nil
	s.paneRender = ""
}
