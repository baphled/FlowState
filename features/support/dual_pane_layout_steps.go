package support

import (
	"errors"
	"fmt"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/tui/uikit/layout"
	"github.com/baphled/flowstate/internal/ui/terminal"
)

// Pending reasons for dual-pane layout scenarios. These describe downstream
// work items whose implementation will unblock the corresponding steps.
const (
	pendingReasonDualPaneRender = "pending: implement once Content renders dual-pane (blocked on T2)"
	pendingReasonToggleBinding  = "pending: implement once toggle keybinding lands (blocked on T7)"
)

// DualPaneLayoutSteps holds state for dual-pane ScreenLayout BDD scenarios.
//
// These scenarios cover Wave 1 / T8 of the Multi-Agent Chat UX
// implementation plan and remain pending until the render path and
// keybinding land in later tasks.
type DualPaneLayoutSteps struct {
	layout           *layout.ScreenLayout
	primaryContent   string
	secondaryContent string
}

// RegisterDualPaneLayoutSteps registers step definitions for dual-pane
// ScreenLayout BDD scenarios.
//
// Expected:
//   - sc is a valid godog ScenarioContext.
//
// Side effects:
//   - Registers dual-pane step definitions on the provided scenario context.
func RegisterDualPaneLayoutSteps(sc *godog.ScenarioContext) {
	s := &DualPaneLayoutSteps{}
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

// registerDualPaneRenderSteps wires up every step that depends on T2's
// dual-pane render path landing inside ScreenLayout.Render.
//
// Expected:
//   - sc is a valid godog ScenarioContext; s is non-nil.
//
// Side effects:
//   - Registers render-dependent step definitions on the scenario context.
func registerDualPaneRenderSteps(sc *godog.ScenarioContext, s *DualPaneLayoutSteps) {
	sc.Step(`^the ScreenLayout is rendered$`, s.renderPendingDualPane)
	sc.Step(`^the rendered output should contain two panes side by side$`, s.renderPendingDualPane)
	sc.Step(`^the rendered output should contain a single pane$`, s.renderPendingDualPane)
	sc.Step(`^the primary pane should occupy roughly 70% of the width$`, s.renderPendingDualPane)
	sc.Step(`^the secondary pane should occupy roughly 30% of the width$`, s.renderPendingDualPane)
	sc.Step(`^the primary pane should occupy the full width$`, s.renderPendingDualPane)
}

// registerDualPaneToggleSteps wires up every step that depends on T7's
// Ctrl+T keybinding landing.
//
// Expected:
//   - sc is a valid godog ScenarioContext; s is non-nil.
//
// Side effects:
//   - Registers toggle-dependent step definitions on the scenario context.
func registerDualPaneToggleSteps(sc *godog.ScenarioContext, s *DualPaneLayoutSteps) {
	sc.Step(`^the activity pane is visible$`, s.activityPanePendingToggle)
	sc.Step(`^the activity pane has been hidden via Ctrl\+T$`, s.activityPanePendingToggle)
	sc.Step(`^the operator presses Ctrl\+T$`, s.activityPanePendingToggle)
	sc.Step(`^the activity pane should be hidden$`, s.activityPanePendingToggle)
	sc.Step(`^the activity pane should be visible$`, s.activityPanePendingToggle)
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
	s.layout = layout.NewScreenLayout(&terminal.Info{Width: width, Height: height, IsValid: true})
	s.primaryContent = ""
	s.secondaryContent = ""
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

// renderPendingDualPane returns ErrPending for every step whose behaviour
// depends on T2 landing dual-pane rendering inside ScreenLayout.
//
// Returns:
//   - godog.ErrPending until T2 lands.
//
// Side effects:
//   - None.
func (s *DualPaneLayoutSteps) renderPendingDualPane() error {
	return pending(pendingReasonDualPaneRender)
}

// activityPanePendingToggle returns ErrPending for every step whose
// behaviour depends on T7 introducing the Ctrl+T keybinding.
//
// Returns:
//   - godog.ErrPending until T7 lands.
//
// Side effects:
//   - None.
func (s *DualPaneLayoutSteps) activityPanePendingToggle() error {
	return pending(pendingReasonToggleBinding)
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

// pending is a tiny helper that returns godog.ErrPending while keeping the
// human-readable reason available for future diagnostic output.
//
// Expected:
//   - reason is a short description of what is blocked and by which task.
//
// Returns:
//   - godog.ErrPending.
//
// Side effects:
//   - None.
func pending(reason string) error {
	_ = reason
	return godog.ErrPending
}
