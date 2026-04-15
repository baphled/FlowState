package support

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/tui/intents/chat"
	"github.com/baphled/flowstate/internal/tui/uikit/layout"
	"github.com/baphled/flowstate/internal/ui/terminal"
)

// dualPaneSeparator is the rune ScreenLayout draws between the primary and
// secondary panes. Mirrors the constant in the layout package; duplicated
// here so the BDD assertions remain self-contained.
const dualPaneSeparator = "│"

// activityTimelineHeader is the heading the swarm activity pane renders
// when visible. Used to assert chat Intent secondary-pane state without
// reaching into unexported fields.
const activityTimelineHeader = "Activity Timeline"

// DualPaneLayoutSteps holds state for dual-pane ScreenLayout BDD scenarios.
//
// Two render targets coexist on this struct: a layout.ScreenLayout for the
// pure-render scenarios, and a chat.Intent for the Ctrl+T toggle scenarios.
// The layout fixture is created in the Background; the Intent is created
// lazily on the first toggle-related step so render-only scenarios pay no
// Intent-construction cost.
type DualPaneLayoutSteps struct {
	layout           *layout.ScreenLayout
	terminalWidth    int
	terminalHeight   int
	primaryContent   string
	secondaryContent string
	lastRender       string
	intent           *chat.Intent
}

// RegisterDualPaneLayoutSteps registers step definitions for dual-pane
// ScreenLayout BDD scenarios.
//
// Expected:
//   - sc is a valid godog ScenarioContext.
//
// Side effects:
//   - Registers dual-pane step definitions on the provided scenario context.
//   - Resets per-scenario state via a Before hook so Intent and ScreenLayout
//     fixtures do not leak between scenarios.
func RegisterDualPaneLayoutSteps(sc *godog.ScenarioContext) {
	s := &DualPaneLayoutSteps{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return ctx, nil
	})
	registerDualPaneStructuralSteps(sc, s)
	registerDualPaneRenderSteps(sc, s)
	registerDualPaneToggleSteps(sc, s)
}

// registerDualPaneStructuralSteps wires up the purely structural setup
// steps that configure the ScreenLayout ahead of rendering or toggling.
//
// Expected:
//   - sc is a valid godog ScenarioContext; s is non-nil.
//
// Side effects:
//   - Registers the structural setup step definitions on the scenario context.
func registerDualPaneStructuralSteps(sc *godog.ScenarioContext, s *DualPaneLayoutSteps) {
	sc.Step(`^a ScreenLayout is initialised with a terminal size of (\d+)x(\d+)$`, s.aScreenLayoutIsInitialisedWithATerminalSizeOf)
	sc.Step(`^the primary content is "([^"]*)"$`, s.thePrimaryContentIs)
	sc.Step(`^the secondary content is "([^"]*)"$`, s.theSecondaryContentIs)
	sc.Step(`^the secondary content is empty$`, s.theSecondaryContentIsEmpty)
}

// registerDualPaneRenderSteps wires up the render-trigger and render-shape
// assertion steps backed by layout.ScreenLayout.Render.
//
// Expected:
//   - sc is a valid godog ScenarioContext; s is non-nil.
//
// Side effects:
//   - Registers render step definitions on the scenario context.
func registerDualPaneRenderSteps(sc *godog.ScenarioContext, s *DualPaneLayoutSteps) {
	sc.Step(`^the ScreenLayout is rendered$`, s.theScreenLayoutIsRendered)
	sc.Step(`^the rendered output should contain two panes side by side$`, s.theRenderedOutputShouldContainTwoPanes)
	sc.Step(`^the rendered output should contain a single pane$`, s.theRenderedOutputShouldContainSinglePane)
	sc.Step(`^the primary pane should occupy roughly 70% of the width$`, s.thePrimaryPaneShouldOccupyRoughly70Percent)
	sc.Step(`^the secondary pane should occupy roughly 30% of the width$`, s.theSecondaryPaneShouldOccupyRoughly30Percent)
	sc.Step(`^the primary pane should occupy the full width$`, s.thePrimaryPaneShouldOccupyTheFullWidth)
}

// registerDualPaneToggleSteps wires up the Ctrl+T toggle steps backed by
// chat.Intent and SecondaryPaneVisibleForTest.
//
// Expected:
//   - sc is a valid godog ScenarioContext; s is non-nil.
//
// Side effects:
//   - Registers toggle step definitions on the scenario context.
func registerDualPaneToggleSteps(sc *godog.ScenarioContext, s *DualPaneLayoutSteps) {
	sc.Step(`^the activity pane is visible$`, s.theActivityPaneIsVisible)
	sc.Step(`^the activity pane has been hidden via Ctrl\+T$`, s.theActivityPaneHasBeenHiddenViaCtrlT)
	sc.Step(`^the operator presses Ctrl\+T$`, s.theOperatorPressesCtrlT)
	sc.Step(`^the activity pane should be hidden$`, s.theActivityPaneShouldBeHidden)
	sc.Step(`^the activity pane should be visible$`, s.theActivityPaneShouldBeVisible)
}

// aScreenLayoutIsInitialisedWithATerminalSizeOf constructs a ScreenLayout
// with the supplied terminal dimensions and resets scenario state.
//
// Expected:
//   - width and height are positive integers.
//
// Returns:
//   - nil on success; error if dimensions are non-positive.
//
// Side effects:
//   - Creates a fresh ScreenLayout and resets scenario state.
func (s *DualPaneLayoutSteps) aScreenLayoutIsInitialisedWithATerminalSizeOf(width, height int) error {
	if width <= 0 || height <= 0 {
		return fmt.Errorf("terminal dimensions must be positive, got %dx%d", width, height)
	}
	s.terminalWidth = width
	s.terminalHeight = height
	s.layout = layout.NewScreenLayout(&terminal.Info{Width: width, Height: height, IsValid: true})
	s.primaryContent = ""
	s.secondaryContent = ""
	s.lastRender = ""
	s.intent = nil
	return nil
}

// thePrimaryContentIs stores the primary pane body and applies it to the
// ScreenLayout via WithContent.
//
// Expected:
//   - content is any string (may be empty).
//
// Returns:
//   - nil on success; error if the layout has not been initialised.
//
// Side effects:
//   - Mutates the underlying ScreenLayout Content field.
func (s *DualPaneLayoutSteps) thePrimaryContentIs(content string) error {
	if err := s.ensureLayout(); err != nil {
		return err
	}
	s.primaryContent = content
	s.layout.WithContent(content)
	return nil
}

// theSecondaryContentIs stores the secondary pane body and applies it to
// the ScreenLayout via WithSecondaryContent.
//
// Expected:
//   - content is any string (may be empty).
//
// Returns:
//   - nil on success; error if the layout has not been initialised.
//
// Side effects:
//   - Mutates the underlying ScreenLayout secondaryContent field.
func (s *DualPaneLayoutSteps) theSecondaryContentIs(content string) error {
	if err := s.ensureLayout(); err != nil {
		return err
	}
	s.secondaryContent = content
	s.layout.WithSecondaryContent(content)
	return nil
}

// theSecondaryContentIsEmpty ensures no secondary content is set on the
// ScreenLayout.
//
// Returns:
//   - nil on success; error if the layout has not been initialised.
//
// Side effects:
//   - Ensures the ScreenLayout secondaryContent field is empty.
func (s *DualPaneLayoutSteps) theSecondaryContentIsEmpty() error {
	if err := s.ensureLayout(); err != nil {
		return err
	}
	s.secondaryContent = ""
	s.layout.WithSecondaryContent("")
	return nil
}

// theScreenLayoutIsRendered drives the ScreenLayout.Render path and stashes
// the output for downstream structural assertions.
//
// Returns:
//   - nil on success; error if the layout has not been initialised or the
//     render produced an empty string (indicating a regression in T2).
//
// Side effects:
//   - Sets s.lastRender to the rendered output.
func (s *DualPaneLayoutSteps) theScreenLayoutIsRendered() error {
	if err := s.ensureLayout(); err != nil {
		return err
	}
	rendered := s.layout.Render()
	if rendered == "" {
		return errors.New("ScreenLayout.Render returned an empty string")
	}
	s.lastRender = rendered
	return nil
}

// theRenderedOutputShouldContainTwoPanes asserts that the most recent
// render contains the dual-pane separator on at least one line and that
// both primary and secondary content appear in the output.
//
// Returns:
//   - nil if both panes are detected; error otherwise.
//
// Side effects:
//   - None.
func (s *DualPaneLayoutSteps) theRenderedOutputShouldContainTwoPanes() error {
	if err := s.ensureRender(); err != nil {
		return err
	}
	if !strings.Contains(s.lastRender, dualPaneSeparator) {
		return fmt.Errorf("expected dual-pane separator %q in render, got:\n%s", dualPaneSeparator, s.lastRender)
	}
	if s.primaryContent != "" && !strings.Contains(s.lastRender, s.primaryContent) {
		return fmt.Errorf("expected primary content %q in render", s.primaryContent)
	}
	if s.secondaryContent != "" && !strings.Contains(s.lastRender, s.secondaryContent) {
		return fmt.Errorf("expected secondary content %q in render", s.secondaryContent)
	}
	return nil
}

// theRenderedOutputShouldContainSinglePane asserts that the most recent
// render does NOT contain the dual-pane separator and that the primary
// content is present.
//
// Returns:
//   - nil if the render is single-pane; error otherwise.
//
// Side effects:
//   - None.
func (s *DualPaneLayoutSteps) theRenderedOutputShouldContainSinglePane() error {
	if err := s.ensureRender(); err != nil {
		return err
	}
	if strings.Contains(s.lastRender, dualPaneSeparator) {
		return fmt.Errorf("expected single-pane render but found separator %q", dualPaneSeparator)
	}
	if s.primaryContent != "" && !strings.Contains(s.lastRender, s.primaryContent) {
		return fmt.Errorf("expected primary content %q in single-pane render", s.primaryContent)
	}
	return nil
}

// thePrimaryPaneShouldOccupyRoughly70Percent asserts that the dual-pane
// split places the separator within the documented 70/30 window. When the
// scenario is driven by a chat.Intent (toggle scenarios), the Intent's
// View output is interrogated; otherwise the ScreenLayout render is used.
//
// Returns:
//   - nil if the separator falls inside the 65%-75% column band on at
//     least one rendered line; error otherwise.
//
// Side effects:
//   - None.
func (s *DualPaneLayoutSteps) thePrimaryPaneShouldOccupyRoughly70Percent() error {
	output, width, err := s.activeRenderAndWidth()
	if err != nil {
		return err
	}
	return assertSeparatorWithin70PercentBand(output, width)
}

// theSecondaryPaneShouldOccupyRoughly30Percent asserts that the secondary
// content body appears in the active render output. The 70/30 contract is
// enforced by thePrimaryPaneShouldOccupyRoughly70Percent; this step
// guarantees the secondary pane is visibly populated.
//
// Returns:
//   - nil if the secondary body is visible in the render; error otherwise.
//
// Side effects:
//   - None.
func (s *DualPaneLayoutSteps) theSecondaryPaneShouldOccupyRoughly30Percent() error {
	output, _, err := s.activeRenderAndWidth()
	if err != nil {
		return err
	}
	if s.intent != nil {
		if !strings.Contains(output, activityTimelineHeader) {
			return fmt.Errorf("expected %q in Intent view to confirm secondary pane is occupied", activityTimelineHeader)
		}
		return nil
	}
	if s.secondaryContent == "" {
		return errors.New("secondary content was never set on the ScreenLayout fixture")
	}
	if !strings.Contains(output, s.secondaryContent) {
		return fmt.Errorf("expected secondary content %q in render", s.secondaryContent)
	}
	return nil
}

// thePrimaryPaneShouldOccupyTheFullWidth asserts that the active render is
// single-pane: no dual-pane separator appears as a structural column
// marker. For Intent-driven scenarios this also requires that the
// activity timeline header is absent.
//
// Returns:
//   - nil if the render is single-pane; error otherwise.
//
// Side effects:
//   - None.
func (s *DualPaneLayoutSteps) thePrimaryPaneShouldOccupyTheFullWidth() error {
	output, _, err := s.activeRenderAndWidth()
	if err != nil {
		return err
	}
	if s.intent != nil {
		if strings.Contains(output, activityTimelineHeader) {
			return fmt.Errorf("expected single-pane Intent view but found %q", activityTimelineHeader)
		}
		return nil
	}
	if strings.Contains(output, dualPaneSeparator) {
		return fmt.Errorf("expected full-width single-pane render but found separator %q", dualPaneSeparator)
	}
	return nil
}

// theActivityPaneIsVisible ensures a chat.Intent is constructed for the
// scenario and asserts the secondary pane starts visible. Visibility is
// asserted via the rendered View output rather than internal state so the
// helper depends only on the public chat.Intent surface.
//
// Returns:
//   - nil if the Intent renders the activity pane; error otherwise.
//
// Side effects:
//   - Lazily constructs s.intent and refreshes s.lastRender.
func (s *DualPaneLayoutSteps) theActivityPaneIsVisible() error {
	if err := s.ensureIntent(); err != nil {
		return err
	}
	view := s.intent.View()
	s.lastRender = view
	if !strings.Contains(view, activityTimelineHeader) {
		return fmt.Errorf("expected %q in initial Intent view", activityTimelineHeader)
	}
	return nil
}

// theActivityPaneHasBeenHiddenViaCtrlT constructs the chat.Intent (if
// needed), sends a Ctrl+T key, and asserts the secondary pane is now
// hidden. Used as a precondition for the "press Ctrl+T again" scenario.
//
// Returns:
//   - nil if the pane is hidden after the toggle; error otherwise.
//
// Side effects:
//   - Lazily constructs s.intent and applies one Ctrl+T toggle.
func (s *DualPaneLayoutSteps) theActivityPaneHasBeenHiddenViaCtrlT() error {
	if err := s.ensureIntent(); err != nil {
		return err
	}
	s.intent.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	view := s.intent.View()
	if strings.Contains(view, activityTimelineHeader) {
		return fmt.Errorf("expected %q to be absent after Ctrl+T precondition", activityTimelineHeader)
	}
	s.lastRender = view
	return nil
}

// theOperatorPressesCtrlT sends a Ctrl+T key to the chat.Intent and
// refreshes the cached render so subsequent assertions read the latest
// View output.
//
// Returns:
//   - nil on success; error if the Intent has not been constructed.
//
// Side effects:
//   - Mutates s.intent state and refreshes s.lastRender.
func (s *DualPaneLayoutSteps) theOperatorPressesCtrlT() error {
	if err := s.ensureIntent(); err != nil {
		return err
	}
	s.intent.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	s.lastRender = s.intent.View()
	return nil
}

// theActivityPaneShouldBeHidden asserts the chat.Intent's rendered View no
// longer contains the activity timeline header. The View output is the
// authoritative public signal that the secondary pane has collapsed.
//
// Returns:
//   - nil if the header is absent from View; error otherwise.
//
// Side effects:
//   - None.
func (s *DualPaneLayoutSteps) theActivityPaneShouldBeHidden() error {
	if err := s.ensureIntent(); err != nil {
		return err
	}
	if strings.Contains(s.intent.View(), activityTimelineHeader) {
		return fmt.Errorf("expected %q to be absent after Ctrl+T", activityTimelineHeader)
	}
	return nil
}

// theActivityPaneShouldBeVisible asserts the chat.Intent's rendered View
// contains the activity timeline header again after a restore.
//
// Returns:
//   - nil if the header is present in View; error otherwise.
//
// Side effects:
//   - None.
func (s *DualPaneLayoutSteps) theActivityPaneShouldBeVisible() error {
	if err := s.ensureIntent(); err != nil {
		return err
	}
	if !strings.Contains(s.intent.View(), activityTimelineHeader) {
		return fmt.Errorf("expected %q in View after second Ctrl+T", activityTimelineHeader)
	}
	return nil
}

// ensureLayout guarantees that the ScreenLayout has been initialised
// before subsequent steps interact with it.
//
// Returns:
//   - nil on success; error if the layout has not been initialised.
//
// Side effects:
//   - None.
func (s *DualPaneLayoutSteps) ensureLayout() error {
	if s.layout == nil {
		return errors.New("ScreenLayout has not been initialised")
	}
	return nil
}

// ensureRender guarantees that a render has been captured before render-
// shape assertions run.
//
// Returns:
//   - nil on success; error if no render has been captured yet.
//
// Side effects:
//   - None.
func (s *DualPaneLayoutSteps) ensureRender() error {
	if s.lastRender == "" {
		return errors.New("no rendered output captured; ensure 'the ScreenLayout is rendered' ran first")
	}
	return nil
}

// ensureIntent lazily constructs a chat.Intent using the same configuration
// pattern as the swarm-activity Ginkgo suites and applies a window-size
// message so the dual-pane render path activates.
//
// Returns:
//   - nil on success; error if the ScreenLayout fixture is missing.
//
// Side effects:
//   - On first call: constructs s.intent and sends the initial WindowSizeMsg.
func (s *DualPaneLayoutSteps) ensureIntent() error {
	if s.intent != nil {
		return nil
	}
	if err := s.ensureLayout(); err != nil {
		return err
	}
	s.intent = chat.NewIntent(chat.IntentConfig{
		AgentID:      "test-agent",
		SessionID:    "test-session",
		ProviderName: "openai",
		ModelName:    "gpt-4o",
		TokenBudget:  4096,
	})
	s.intent.Update(tea.WindowSizeMsg{Width: s.terminalWidth, Height: s.terminalHeight})
	return nil
}

// activeRenderAndWidth returns the render output and width that downstream
// width-assertion steps should interrogate. Intent-driven scenarios use
// the live Intent view; otherwise the cached ScreenLayout render is used.
//
// Returns:
//   - The rendered output, the width it was rendered at, and any error
//     describing why no render is available.
//
// Side effects:
//   - None.
func (s *DualPaneLayoutSteps) activeRenderAndWidth() (string, int, error) {
	if s.intent != nil {
		return s.intent.View(), s.terminalWidth, nil
	}
	if err := s.ensureRender(); err != nil {
		return "", 0, err
	}
	return s.lastRender, s.terminalWidth, nil
}

// reset clears all per-scenario state. Called from a Before hook so the
// Intent and ScreenLayout fixtures do not bleed between scenarios.
//
// Side effects:
//   - Clears every field on the receiver.
func (s *DualPaneLayoutSteps) reset() {
	s.layout = nil
	s.terminalWidth = 0
	s.terminalHeight = 0
	s.primaryContent = ""
	s.secondaryContent = ""
	s.lastRender = ""
	s.intent = nil
}

// assertSeparatorWithin70PercentBand scans the rendered output for at
// least one line whose dual-pane separator column falls inside the 65%-75%
// band of the terminal width. The narrow band rejects accidental
// separators (e.g. status-bar glyphs) while tolerating the integer-math
// 70/30 split documented in the ScreenLayout ADR.
//
// Expected:
//   - output is the rendered string from ScreenLayout.Render or
//     chat.Intent.View.
//   - width is the terminal width the render was performed at.
//
// Returns:
//   - nil if a qualifying line is found; error otherwise.
//
// Side effects:
//   - None.
func assertSeparatorWithin70PercentBand(output string, width int) error {
	if width <= 0 {
		return fmt.Errorf("invalid render width %d", width)
	}
	lowerBound := (width * 65) / 100
	upperBound := (width * 75) / 100
	for _, line := range strings.Split(output, "\n") {
		runes := []rune(line)
		for col, r := range runes {
			if string(r) != dualPaneSeparator {
				continue
			}
			if col >= lowerBound && col <= upperBound {
				return nil
			}
		}
	}
	return fmt.Errorf("expected dual-pane separator within columns %d-%d at width %d, none found", lowerBound, upperBound, width)
}
