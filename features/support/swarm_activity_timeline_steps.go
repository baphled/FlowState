package support

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/streaming"
	swarmactivity "github.com/baphled/flowstate/internal/tui/components/swarm_activity"
	tuiintents "github.com/baphled/flowstate/internal/tui/intents"
	"github.com/baphled/flowstate/internal/tui/intents/chat"
	"github.com/baphled/flowstate/internal/tui/intents/eventdetails"
)

// swarmActivityTimelineSteps holds state for the Wave 3 swarm activity
// timeline BDD scenarios.
//
// It exercises the SwarmEvent model, SwarmActivityPane rendering, event
// details modal, filtering, and JSONL persistence directly without
// constructing a full TUI runtime.
type swarmActivityTimelineSteps struct {
	// SwarmEvent under test.
	event streaming.SwarmEvent

	// SwarmActivityPane fixture.
	pane       *swarmactivity.SwarmActivityPane
	paneRender string

	// Event details intent fixture.
	detailsIntent *eventdetails.Intent

	// Chat intent for Ctrl+E scenario.
	chatIntent *chat.Intent
	lastCmd    tea.Cmd

	// JSONL persistence state.
	originalEvents []streaming.SwarmEvent
	readEvents     []streaming.SwarmEvent
	jsonlBuffer    bytes.Buffer
}

// RegisterSwarmActivityTimelineSteps registers step definitions for the
// Wave 3 swarm activity timeline BDD scenarios.
//
// Expected:
//   - sc is a valid godog ScenarioContext.
//
// Side effects:
//   - Registers all swarm activity timeline step definitions.
//   - Resets per-scenario state via a Before hook.
func RegisterSwarmActivityTimelineSteps(sc *godog.ScenarioContext) {
	s := &swarmActivityTimelineSteps{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return ctx, nil
	})
	registerSwarmEventModelSteps(sc, s)
	registerActivityPaneSteps(sc, s)
	registerCtrlESteps(sc, s)
	registerEventDetailsSteps(sc, s)
	registerFilterSteps(sc, s)
	registerPersistenceSteps(sc, s)
	registerADRLabelSteps(sc, s)
}

// registerSwarmEventModelSteps wires up the SwarmEvent model lifecycle steps.
//
// Expected:
//   - sc is a valid godog ScenarioContext; s is non-nil.
//
// Side effects:
//   - Registers SwarmEvent model step definitions on the scenario context.
func registerSwarmEventModelSteps(sc *godog.ScenarioContext, s *swarmActivityTimelineSteps) {
	sc.Step(`^a SwarmEvent of type "([^"]*)" with status "([^"]*)" and agent "([^"]*)"$`, s.aSwarmEventOfType)
	sc.Step(`^the event type should be "([^"]*)"$`, s.theEventTypeShouldBe)
	sc.Step(`^the event status should be "([^"]*)"$`, s.theEventStatusShouldBe)
	sc.Step(`^the event agent should be "([^"]*)"$`, s.theEventAgentShouldBe)
	sc.Step(`^the event timestamp should be set$`, s.theEventTimestampShouldBeSet)
	sc.Step(`^the event status is updated to "([^"]*)"$`, s.theEventStatusIsUpdatedTo)
	sc.Step(`^the event has metadata key "([^"]*)" with value "([^"]*)"$`, s.theEventHasMetadataKey)
	sc.Step(`^the event metadata should contain key "([^"]*)" with value "([^"]*)"$`, s.theEventMetadataShouldContainKey)
}

// registerActivityPaneSteps wires up the SwarmActivityPane rendering steps.
//
// Expected:
//   - sc is a valid godog ScenarioContext; s is non-nil.
//
// Side effects:
//   - Registers activity pane rendering step definitions on the scenario context.
func registerActivityPaneSteps(sc *godog.ScenarioContext, s *swarmActivityTimelineSteps) {
	sc.Step(`^a SwarmActivityPane with 3 events of different types$`, s.aSwarmActivityPaneWith3Events)
	sc.Step(`^the activity pane is rendered at (\d+)x(\d+)$`, s.theActivityPaneIsRenderedAt)
	sc.Step(`^the rendered pane should contain "([^"]*)"$`, s.theRenderedPaneShouldContain)
	sc.Step(`^the rendered pane should not contain "([^"]*)"$`, s.theRenderedPaneShouldNotContain)
}

// registerCtrlESteps wires up the Ctrl+E drill-down modal steps.
//
// Expected:
//   - sc is a valid godog ScenarioContext; s is non-nil.
//
// Side effects:
//   - Registers Ctrl+E step definitions on the scenario context.
func registerCtrlESteps(sc *godog.ScenarioContext, s *swarmActivityTimelineSteps) {
	sc.Step(`^a chat intent with a swarm event of type "([^"]*)" and status "([^"]*)"$`, s.aChatIntentWithSwarmEvent)
	sc.Step(`^the operator presses Ctrl\+E on the chat intent$`, s.theOperatorPressesCtrlE)
	sc.Step(`^a ShowModalMsg should be emitted with an eventdetails Intent$`, s.aShowModalMsgShouldBeEmitted)
}

// registerEventDetailsSteps wires up the event details modal steps.
//
// Expected:
//   - sc is a valid godog ScenarioContext; s is non-nil.
//
// Side effects:
//   - Registers event details step definitions on the scenario context.
func registerEventDetailsSteps(sc *godog.ScenarioContext, s *swarmActivityTimelineSteps) {
	sc.Step(`^the event details intent is created from the event$`, s.theEventDetailsIntentIsCreated)
	sc.Step(`^the event details view should contain "([^"]*)"$`, s.theEventDetailsViewShouldContain)
}

// registerFilterSteps wires up the event filter steps.
//
// Expected:
//   - sc is a valid godog ScenarioContext; s is non-nil.
//
// Side effects:
//   - Registers filter step definitions on the scenario context.
func registerFilterSteps(sc *godog.ScenarioContext, s *swarmActivityTimelineSteps) {
	sc.Step(`^a SwarmActivityPane with events of types "([^"]*)", "([^"]*)", "([^"]*)"$`, s.aSwarmActivityPaneWithEventTypes)
	sc.Step(`^the visibility filter hides "([^"]*)"$`, s.theVisibilityFilterHides)
	sc.Step(`^the rendered pane should contain a count summary$`, s.theRenderedPaneShouldContainCountSummary)
}

// registerADRLabelSteps wires up the ADR-label regression steps that
// assert the rendered timeline uses the ADR human-readable type labels
// ("Delegation", "Tool Call", "Plan", "Review") and never leaks the
// streaming-layer wire identifiers ("tool_call", "tool_result").
//
// Expected:
//   - sc is a valid godog ScenarioContext; s is non-nil.
//
// Side effects:
//   - Registers ADR-label step definitions on the scenario context.
func registerADRLabelSteps(sc *godog.ScenarioContext, s *swarmActivityTimelineSteps) {
	sc.Step(`^a SwarmActivityPane with events of types "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)"$`, s.aSwarmActivityPaneWithFourEventTypes)
	sc.Step(`^a chat intent seeded with one swarm event of each ADR type$`, s.aChatIntentSeededWithOneEventOfEachADRType)
	sc.Step(`^the chat intent view is rendered$`, s.theChatIntentViewIsRendered)
	sc.Step(`^the chat intent view should contain "([^"]*)"$`, s.theChatIntentViewShouldContain)
	sc.Step(`^the chat intent view should not contain "([^"]*)"$`, s.theChatIntentViewShouldNotContain)
}

// registerPersistenceSteps wires up the JSONL persistence round-trip steps.
//
// Expected:
//   - sc is a valid godog ScenarioContext; s is non-nil.
//
// Side effects:
//   - Registers JSONL persistence step definitions on the scenario context.
func registerPersistenceSteps(sc *godog.ScenarioContext, s *swarmActivityTimelineSteps) {
	sc.Step(`^3 SwarmEvents with distinct types and metadata$`, s.threeSwarmEventsWithDistinctTypes)
	sc.Step(`^the events are written to a buffer via WriteEventsJSONL$`, s.theEventsAreWrittenViaWriteEventsJSONL)
	sc.Step(`^the buffer is read back via ReadEventsJSONL$`, s.theBufferIsReadBackViaReadEventsJSONL)
	sc.Step(`^the read events should match the original events$`, s.theReadEventsShouldMatchOriginals)
}

// --- SwarmEvent model steps ---

// aSwarmEventOfType creates a SwarmEvent with the given type, status, and agent.
//
// Expected:
//   - evType, status, agent are non-empty strings.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Populates s.event.
func (s *swarmActivityTimelineSteps) aSwarmEventOfType(evType, status, agent string) error {
	s.event = streaming.SwarmEvent{
		ID:        "test-event-1",
		Type:      streaming.SwarmEventType(evType),
		Status:    status,
		Timestamp: time.Now(),
		AgentID:   agent,
		Metadata:  make(map[string]interface{}),
	}
	return nil
}

// theEventTypeShouldBe asserts the event type matches the expected value.
//
// Expected:
//   - expected is a non-empty SwarmEventType string.
//
// Returns:
//   - nil if matching; error otherwise.
//
// Side effects:
//   - None.
func (s *swarmActivityTimelineSteps) theEventTypeShouldBe(expected string) error {
	if string(s.event.Type) != expected {
		return fmt.Errorf("expected event type %q, got %q", expected, s.event.Type)
	}
	return nil
}

// theEventStatusShouldBe asserts the event status matches the expected value.
//
// Expected:
//   - expected is a non-empty status string.
//
// Returns:
//   - nil if matching; error otherwise.
//
// Side effects:
//   - None.
func (s *swarmActivityTimelineSteps) theEventStatusShouldBe(expected string) error {
	if s.event.Status != expected {
		return fmt.Errorf("expected event status %q, got %q", expected, s.event.Status)
	}
	return nil
}

// theEventAgentShouldBe asserts the event agent ID matches the expected value.
//
// Expected:
//   - expected is a non-empty agent ID string.
//
// Returns:
//   - nil if matching; error otherwise.
//
// Side effects:
//   - None.
func (s *swarmActivityTimelineSteps) theEventAgentShouldBe(expected string) error {
	if s.event.AgentID != expected {
		return fmt.Errorf("expected event agent %q, got %q", expected, s.event.AgentID)
	}
	return nil
}

// theEventTimestampShouldBeSet asserts the event timestamp is non-zero.
//
// Expected:
//   - s.event has been populated by a prior step.
//
// Returns:
//   - nil if set; error otherwise.
//
// Side effects:
//   - None.
func (s *swarmActivityTimelineSteps) theEventTimestampShouldBeSet() error {
	if s.event.Timestamp.IsZero() {
		return errors.New("expected event timestamp to be set, got zero value")
	}
	return nil
}

// theEventStatusIsUpdatedTo simulates updating the event status.
//
// Expected:
//   - newStatus is a non-empty status string.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Updates s.event.Status.
func (s *swarmActivityTimelineSteps) theEventStatusIsUpdatedTo(newStatus string) error {
	s.event.Status = newStatus
	return nil
}

// theEventHasMetadataKey adds a metadata key-value pair to the event.
//
// Expected:
//   - key and value are non-empty strings.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Adds to s.event.Metadata.
func (s *swarmActivityTimelineSteps) theEventHasMetadataKey(key, value string) error {
	if s.event.Metadata == nil {
		s.event.Metadata = make(map[string]interface{})
	}
	s.event.Metadata[key] = value
	return nil
}

// theEventMetadataShouldContainKey asserts the event metadata contains the
// expected key-value pair.
//
// Expected:
//   - key is a non-empty metadata key; expectedValue is its expected value.
//
// Returns:
//   - nil if matching; error otherwise.
//
// Side effects:
//   - None.
func (s *swarmActivityTimelineSteps) theEventMetadataShouldContainKey(key, expectedValue string) error {
	val, ok := s.event.Metadata[key]
	if !ok {
		return fmt.Errorf("expected metadata key %q not found", key)
	}
	if fmt.Sprintf("%v", val) != expectedValue {
		return fmt.Errorf("expected metadata key %q to have value %q, got %q", key, expectedValue, val)
	}
	return nil
}

// --- SwarmActivityPane rendering steps ---

// aSwarmActivityPaneWith3Events creates a SwarmActivityPane backed by 3
// events of different types (delegation, tool_call, plan).
//
// Expected:
//   - None.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Populates s.pane.
func (s *swarmActivityTimelineSteps) aSwarmActivityPaneWith3Events() error {
	events := []streaming.SwarmEvent{
		{ID: "ev-1", Type: streaming.EventDelegation, Status: "started", Timestamp: time.Now(), AgentID: "senior-engineer"},
		{ID: "ev-2", Type: streaming.EventToolCall, Status: "completed", Timestamp: time.Now(), AgentID: "junior-engineer"},
		{ID: "ev-3", Type: streaming.EventPlan, Status: "created", Timestamp: time.Now(), AgentID: "tech-lead"},
	}
	s.pane = swarmactivity.NewSwarmActivityPane().WithEvents(events)
	return nil
}

// theActivityPaneIsRenderedAt renders the pane at the given dimensions.
//
// Expected:
//   - width and height are positive integers; s.pane has been initialised.
//
// Returns:
//   - nil on success; error if the pane has not been initialised.
//
// Side effects:
//   - Sets s.paneRender.
func (s *swarmActivityTimelineSteps) theActivityPaneIsRenderedAt(width, height int) error {
	if s.pane == nil {
		return errors.New("SwarmActivityPane has not been initialised")
	}
	s.paneRender = s.pane.Render(width, height)
	return nil
}

// theRenderedPaneShouldContain asserts the rendered pane contains the expected
// substring.
//
// Expected:
//   - expected is a non-empty substring; s.paneRender has been captured.
//
// Returns:
//   - nil if found; error otherwise.
//
// Side effects:
//   - None.
func (s *swarmActivityTimelineSteps) theRenderedPaneShouldContain(expected string) error {
	if s.paneRender == "" {
		return errors.New("no pane render captured; ensure the pane was rendered first")
	}
	if !strings.Contains(s.paneRender, expected) {
		return fmt.Errorf("expected rendered pane to contain %q, got:\n%s", expected, s.paneRender)
	}
	return nil
}

// theRenderedPaneShouldNotContain asserts the rendered pane does NOT contain
// the expected substring.
//
// Expected:
//   - unexpected is a non-empty substring; s.paneRender has been captured.
//
// Returns:
//   - nil if absent; error otherwise.
//
// Side effects:
//   - None.
func (s *swarmActivityTimelineSteps) theRenderedPaneShouldNotContain(unexpected string) error {
	if s.paneRender == "" {
		return errors.New("no pane render captured; ensure the pane was rendered first")
	}
	if strings.Contains(s.paneRender, unexpected) {
		return fmt.Errorf("expected rendered pane NOT to contain %q, got:\n%s", unexpected, s.paneRender)
	}
	return nil
}

// --- Ctrl+E drill-down modal steps ---

// aChatIntentWithSwarmEvent creates a chat.Intent and populates its swarm
// store with a single event by sending a StreamChunkMsg through the public
// Update method so Ctrl+E has something to open.
//
// Expected:
//   - evType and status are non-empty strings describing the event.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Populates s.chatIntent with a configured chat.Intent.
func (s *swarmActivityTimelineSteps) aChatIntentWithSwarmEvent(evType, status string) error {
	s.chatIntent = chat.NewIntent(chat.IntentConfig{
		AgentID:      "test-agent",
		SessionID:    "test-session",
		ProviderName: "openai",
		ModelName:    "gpt-4o",
		TokenBudget:  4096,
	})
	s.chatIntent.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	// Send a tool-call StreamChunkMsg through Update to populate the swarm
	// store via the normal event pipeline. The ToolCallName field triggers
	// swarmEventFromChunk to create a tool_call SwarmEvent.
	s.chatIntent.Update(chat.StreamChunkMsg{
		ToolCallName: "ReadFile",
		ToolStatus:   status,
		Done:         true,
	})
	return nil
}

// theOperatorPressesCtrlE sends Ctrl+E to the chat intent via the public
// Update method and captures the returned tea.Cmd.
//
// Expected:
//   - s.chatIntent has been initialised by a prior step.
//
// Returns:
//   - nil on success; error if the chat intent has not been created.
//
// Side effects:
//   - Stores the returned tea.Cmd in s.lastCmd.
func (s *swarmActivityTimelineSteps) theOperatorPressesCtrlE() error {
	if s.chatIntent == nil {
		return errors.New("chat intent has not been created")
	}
	s.lastCmd = s.chatIntent.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	return nil
}

// aShowModalMsgShouldBeEmitted asserts the Cmd returned by Ctrl+E produces
// a ShowModalMsg containing an eventdetails.Intent.
//
// Expected:
//   - s.lastCmd is a non-nil tea.Cmd from the Ctrl+E key press.
//
// Returns:
//   - nil if the assertion passes; error otherwise.
//
// Side effects:
//   - None.
func (s *swarmActivityTimelineSteps) aShowModalMsgShouldBeEmitted() error {
	if s.lastCmd == nil {
		return errors.New("expected a tea.Cmd from Ctrl+E, got nil")
	}
	msg := s.lastCmd()
	if msg == nil {
		return errors.New("expected a tea.Msg from Cmd execution, got nil")
	}
	showModal, ok := msg.(tuiintents.ShowModalMsg)
	if !ok {
		return fmt.Errorf("expected ShowModalMsg, got %T", msg)
	}
	if showModal.Modal == nil {
		return errors.New("ShowModalMsg.Modal is nil")
	}
	_, ok = showModal.Modal.(*eventdetails.Intent)
	if !ok {
		return fmt.Errorf("expected modal to be *eventdetails.Intent, got %T", showModal.Modal)
	}
	return nil
}

// --- Event details modal steps ---

// theEventDetailsIntentIsCreated creates an eventdetails.Intent from the
// current event fixture.
//
// Expected:
//   - s.event has been populated by a prior step.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Populates s.detailsIntent.
func (s *swarmActivityTimelineSteps) theEventDetailsIntentIsCreated() error {
	s.detailsIntent = eventdetails.New(s.event)
	return nil
}

// theEventDetailsViewShouldContain asserts the event details View output
// contains the expected substring.
//
// Expected:
//   - expected is a non-empty substring; s.detailsIntent has been created.
//
// Returns:
//   - nil if found; error otherwise.
//
// Side effects:
//   - None.
func (s *swarmActivityTimelineSteps) theEventDetailsViewShouldContain(expected string) error {
	if s.detailsIntent == nil {
		return errors.New("event details intent has not been created")
	}
	view := s.detailsIntent.View()
	if !strings.Contains(view, expected) {
		return fmt.Errorf("expected event details view to contain %q, got:\n%s", expected, view)
	}
	return nil
}

// --- Event filter steps ---

// aSwarmActivityPaneWithEventTypes creates a SwarmActivityPane backed by
// events of the three specified types.
//
// Expected:
//   - type1, type2, type3 are valid SwarmEventType strings.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Populates s.pane.
func (s *swarmActivityTimelineSteps) aSwarmActivityPaneWithEventTypes(type1, type2, type3 string) error {
	events := []streaming.SwarmEvent{
		{ID: "ev-f1", Type: streaming.SwarmEventType(type1), Status: "started", Timestamp: time.Now(), AgentID: "agent-a"},
		{ID: "ev-f2", Type: streaming.SwarmEventType(type2), Status: "completed", Timestamp: time.Now(), AgentID: "agent-b"},
		{ID: "ev-f3", Type: streaming.SwarmEventType(type3), Status: "created", Timestamp: time.Now(), AgentID: "agent-c"},
	}
	s.pane = swarmactivity.NewSwarmActivityPane().WithEvents(events)
	return nil
}

// theVisibilityFilterHides applies a visibility filter that hides the
// specified event type while keeping all others visible.
//
// Expected:
//   - hiddenType is a valid SwarmEventType string; s.pane has been initialised.
//
// Returns:
//   - nil on success; error if the pane has not been initialised.
//
// Side effects:
//   - Applies WithVisibleTypes to the pane.
func (s *swarmActivityTimelineSteps) theVisibilityFilterHides(hiddenType string) error {
	if s.pane == nil {
		return errors.New("SwarmActivityPane has not been initialised")
	}
	filter := map[streaming.SwarmEventType]bool{
		streaming.EventDelegation: true,
		streaming.EventToolCall:   true,
		streaming.EventPlan:       true,
		streaming.EventReview:     true,
	}
	filter[streaming.SwarmEventType(hiddenType)] = false
	s.pane.WithVisibleTypes(filter)
	return nil
}

// theRenderedPaneShouldContainCountSummary asserts the rendered pane contains
// a "showing X of Y" count summary indicating that filtering is active.
//
// Expected:
//   - s.paneRender has been captured by a prior render step.
//
// Returns:
//   - nil if found; error otherwise.
//
// Side effects:
//   - None.
func (s *swarmActivityTimelineSteps) theRenderedPaneShouldContainCountSummary() error {
	if s.paneRender == "" {
		return errors.New("no pane render captured; ensure the pane was rendered first")
	}
	if !strings.Contains(s.paneRender, "showing") || !strings.Contains(s.paneRender, "of") {
		return fmt.Errorf("expected count summary 'showing X of Y' in render, got:\n%s", s.paneRender)
	}
	return nil
}

// --- JSONL persistence steps ---

// threeSwarmEventsWithDistinctTypes creates 3 SwarmEvents with different
// types and metadata for the persistence round-trip scenario.
//
// Expected:
//   - None.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Populates s.originalEvents.
func (s *swarmActivityTimelineSteps) threeSwarmEventsWithDistinctTypes() error {
	now := time.Now().UTC().Truncate(time.Second)
	s.originalEvents = []streaming.SwarmEvent{
		{
			ID:        "persist-1",
			Type:      streaming.EventDelegation,
			Status:    "started",
			Timestamp: now,
			AgentID:   "senior-engineer",
			Metadata:  map[string]interface{}{"source_agent": "orchestrator"},
		},
		{
			ID:        "persist-2",
			Type:      streaming.EventToolCall,
			Status:    "completed",
			Timestamp: now.Add(time.Second),
			AgentID:   "junior-engineer",
			Metadata:  map[string]interface{}{"tool_name": "ReadFile"},
		},
		{
			ID:        "persist-3",
			Type:      streaming.EventPlan,
			Status:    "created",
			Timestamp: now.Add(2 * time.Second),
			AgentID:   "tech-lead",
			Metadata:  map[string]interface{}{"content": "Wave 3 plan"},
		},
	}
	return nil
}

// theEventsAreWrittenViaWriteEventsJSONL writes the original events to a
// buffer using the JSONL persistence function.
//
// Expected:
//   - s.originalEvents has been populated by a prior step.
//
// Returns:
//   - nil on success; error if writing fails.
//
// Side effects:
//   - Populates s.jsonlBuffer.
func (s *swarmActivityTimelineSteps) theEventsAreWrittenViaWriteEventsJSONL() error {
	s.jsonlBuffer.Reset()
	return streaming.WriteEventsJSONL(&s.jsonlBuffer, s.originalEvents)
}

// theBufferIsReadBackViaReadEventsJSONL reads the JSONL buffer back into
// SwarmEvents.
//
// Expected:
//   - s.jsonlBuffer contains JSONL data from a prior write step.
//
// Returns:
//   - nil on success; error if reading fails.
//
// Side effects:
//   - Populates s.readEvents.
func (s *swarmActivityTimelineSteps) theBufferIsReadBackViaReadEventsJSONL() error {
	var err error
	s.readEvents, err = streaming.ReadEventsJSONL(&s.jsonlBuffer)
	return err
}

// theReadEventsShouldMatchOriginals asserts the round-tripped events match
// the originals on all significant fields.
//
// Expected:
//   - s.readEvents and s.originalEvents are both populated.
//
// Returns:
//   - nil if matching; error otherwise.
//
// Side effects:
//   - None.
func (s *swarmActivityTimelineSteps) theReadEventsShouldMatchOriginals() error {
	if len(s.readEvents) != len(s.originalEvents) {
		return fmt.Errorf("expected %d events, got %d", len(s.originalEvents), len(s.readEvents))
	}
	for i, orig := range s.originalEvents {
		read := s.readEvents[i]
		if read.ID != orig.ID {
			return fmt.Errorf("event %d: expected ID %q, got %q", i, orig.ID, read.ID)
		}
		if read.Type != orig.Type {
			return fmt.Errorf("event %d: expected type %q, got %q", i, orig.Type, read.Type)
		}
		if read.Status != orig.Status {
			return fmt.Errorf("event %d: expected status %q, got %q", i, orig.Status, read.Status)
		}
		if read.AgentID != orig.AgentID {
			return fmt.Errorf("event %d: expected agent %q, got %q", i, orig.AgentID, read.AgentID)
		}
		// Compare timestamps with second-level precision (JSON encoding may
		// lose sub-second precision depending on format).
		if !read.Timestamp.Truncate(time.Second).Equal(orig.Timestamp.Truncate(time.Second)) {
			return fmt.Errorf("event %d: expected timestamp %v, got %v", i, orig.Timestamp, read.Timestamp)
		}
		// Verify at least one metadata key survived the round-trip.
		for key, origVal := range orig.Metadata {
			readVal, ok := read.Metadata[key]
			if !ok {
				return fmt.Errorf("event %d: expected metadata key %q not found after round-trip", i, key)
			}
			if fmt.Sprintf("%v", readVal) != fmt.Sprintf("%v", origVal) {
				return fmt.Errorf("event %d: metadata key %q: expected %v, got %v", i, key, origVal, readVal)
			}
		}
	}
	return nil
}

// reset clears all per-scenario state. Called from a Before hook so fixtures
// do not leak between scenarios.
//
// Side effects:
//   - Clears every field on the receiver.
func (s *swarmActivityTimelineSteps) reset() {
	s.event = streaming.SwarmEvent{}
	s.pane = nil
	s.paneRender = ""
	s.detailsIntent = nil
	s.chatIntent = nil
	s.lastCmd = nil
	s.originalEvents = nil
	s.readEvents = nil
	s.jsonlBuffer.Reset()
}

// --- ADR-label regression steps (Swarm Activity Event Model ADR +
// Multi-Agent Chat UX Plan T5/T21). These back the scenarios that assert
// the timeline uses the ADR human-readable type labels and never leaks the
// wire identifiers "tool_call" / "tool_result" into rendered output.

// aSwarmActivityPaneWithFourEventTypes creates a SwarmActivityPane backed
// by one event of each of the four types specified by the ADR label map:
// delegation, tool_call, plan, review. Mirrors the 3-type variant used by
// the existing filter scenarios so assertions can cover the full ADR matrix
// in one render.
//
// Expected:
//   - type1..type4 are valid SwarmEventType strings.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Populates s.pane.
func (s *swarmActivityTimelineSteps) aSwarmActivityPaneWithFourEventTypes(type1, type2, type3, type4 string) error {
	events := []streaming.SwarmEvent{
		{ID: "ev-adr-1", Type: streaming.SwarmEventType(type1), Status: "started", Timestamp: time.Now(), AgentID: "agent-a"},
		{ID: "ev-adr-2", Type: streaming.SwarmEventType(type2), Status: "completed", Timestamp: time.Now(), AgentID: "agent-b"},
		{ID: "ev-adr-3", Type: streaming.SwarmEventType(type3), Status: "created", Timestamp: time.Now(), AgentID: "agent-c"},
		{ID: "ev-adr-4", Type: streaming.SwarmEventType(type4), Status: "completed", Timestamp: time.Now(), AgentID: "agent-d"},
	}
	s.pane = swarmactivity.NewSwarmActivityPane().WithEvents(events)
	return nil
}

// aChatIntentSeededWithOneEventOfEachADRType constructs a chat.Intent sized
// for the dual-pane path and feeds a StreamChunkMsg for each of the four
// ADR-specified SwarmEventTypes (delegation, tool_call, plan, review)
// through the public Update API so the Intent's swarm store holds one
// event of every type the ADR label map covers.
//
// Returns:
//   - nil on success; error if a chunk construction fails.
//
// Side effects:
//   - Populates s.chatIntent and mutates its state via Update.
func (s *swarmActivityTimelineSteps) aChatIntentSeededWithOneEventOfEachADRType() error {
	s.chatIntent = chat.NewIntent(chat.IntentConfig{
		AgentID:      "test-agent",
		SessionID:    "test-session",
		ProviderName: "openai",
		ModelName:    "gpt-4o",
		TokenBudget:  4096,
	})
	s.chatIntent.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	for _, t := range []string{"delegation", "tool_call", "plan", "review"} {
		chunk, err := chunkForSwarmType(t)
		if err != nil {
			return err
		}
		s.chatIntent.Update(chunk)
	}
	return nil
}

// chunkForSwarmType returns the StreamChunkMsg shape that the chat Intent's
// swarmEventFromChunk converter maps onto the requested SwarmEventType.
//
// Expected:
//   - swarmType is one of "tool_call", "plan", "review", "delegation".
//
// Returns:
//   - A populated StreamChunkMsg; error if swarmType is unrecognised.
//
// Side effects:
//   - None.
func chunkForSwarmType(swarmType string) (chat.StreamChunkMsg, error) {
	switch swarmType {
	case "tool_call":
		return chat.StreamChunkMsg{
			ToolCallName:       "BDDProbeTool",
			ToolStatus:         "completed",
			ToolCallID:         "bdd-adr-" + swarmType,
			InternalToolCallID: "bdd-adr-internal-" + swarmType,
			Done:               true,
		}, nil
	case "plan":
		return chat.StreamChunkMsg{EventType: streaming.EventTypePlanArtifact, Done: true}, nil
	case "review":
		return chat.StreamChunkMsg{EventType: streaming.EventTypeReviewVerdict, Done: true}, nil
	case "delegation":
		return chat.StreamChunkMsg{
			DelegationInfo: &provider.DelegationInfo{
				SourceAgent: "bdd-source",
				TargetAgent: "bdd-target",
				ChainID:     "bdd-chain-adr",
				Status:      "started",
			},
			Done: true,
		}, nil
	default:
		return chat.StreamChunkMsg{}, fmt.Errorf("unsupported swarm type %q for chunk fixture", swarmType)
	}
}

// theChatIntentViewIsRendered drives s.chatIntent.View and stashes the
// render on s.paneRender so the downstream ADR-label contain / not-contain
// assertions can reuse the existing container-style helpers.
//
// Returns:
//   - nil on success; error if the chat intent has not been initialised
//     or the view is empty.
//
// Side effects:
//   - Sets s.paneRender to the rendered view output.
func (s *swarmActivityTimelineSteps) theChatIntentViewIsRendered() error {
	if s.chatIntent == nil {
		return errors.New("chat intent has not been initialised")
	}
	view := s.chatIntent.View()
	if view == "" {
		return errors.New("chat.Intent.View returned an empty string")
	}
	s.paneRender = view
	return nil
}

// theChatIntentViewShouldContain asserts the last captured chat intent
// view contains the supplied substring.
//
// Expected:
//   - expected is a non-empty substring.
//
// Returns:
//   - nil if the substring is present; error otherwise.
//
// Side effects:
//   - None.
func (s *swarmActivityTimelineSteps) theChatIntentViewShouldContain(expected string) error {
	if s.paneRender == "" {
		return errors.New("no chat intent view captured; render it first")
	}
	if !strings.Contains(s.paneRender, expected) {
		return fmt.Errorf("expected chat intent view to contain %q, got:\n%s", expected, s.paneRender)
	}
	return nil
}

// theChatIntentViewShouldNotContain asserts the last captured chat intent
// view does NOT contain the supplied substring. Used to guard against the
// wire identifiers "tool_call" and "tool_result" leaking into user-facing
// copy.
//
// Expected:
//   - unexpected is a non-empty substring.
//
// Returns:
//   - nil if the substring is absent; error otherwise.
//
// Side effects:
//   - None.
func (s *swarmActivityTimelineSteps) theChatIntentViewShouldNotContain(unexpected string) error {
	if s.paneRender == "" {
		return errors.New("no chat intent view captured; render it first")
	}
	if strings.Contains(s.paneRender, unexpected) {
		return fmt.Errorf("expected chat intent view NOT to contain %q, got:\n%s", unexpected, s.paneRender)
	}
	return nil
}
