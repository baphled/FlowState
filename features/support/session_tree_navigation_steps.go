package support

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/tui/intents/sessiontree"
	"github.com/baphled/flowstate/internal/tui/uikit/navigation"
)

// sessionTreeSteps holds state for the session tree navigation BDD scenarios.
//
// It exercises the SessionTrail component and the sessiontree Intent directly,
// without constructing a full chat.Intent. The scenarios validate:
//   - trail rendering and truncation (SessionTrail)
//   - tree modal display, cursor navigation, and selection (sessiontree.Intent)
type sessionTreeSteps struct {
	// SessionTrail state
	trail       *navigation.SessionTrail
	trailItems  []navigation.SessionTrailItem
	trailRender string

	// Session tree intent state
	treeIntent *sessiontree.Intent
	sessions   []sessiontree.SessionNode
	lastCmd    tea.Cmd
}

// RegisterSessionTreeNavigationSteps registers step definitions for the
// hierarchical session navigation BDD scenarios.
//
// Expected:
//   - sc is a valid godog ScenarioContext.
//
// Side effects:
//   - Registers all session trail, tree, and jump step definitions.
//   - Resets per-scenario state via a Before hook.
func RegisterSessionTreeNavigationSteps(sc *godog.ScenarioContext) {
	s := &sessionTreeSteps{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return ctx, nil
	})
	registerSessionTrailSteps(sc, s)
	registerSessionTreeSteps(sc, s)
	registerSessionJumpSteps(sc, s)
}

// registerSessionTrailSteps wires up the session trail rendering and
// truncation assertion steps.
//
// Expected:
//   - sc is a valid godog ScenarioContext; s is non-nil.
//
// Side effects:
//   - Registers session trail step definitions on the scenario context.
func registerSessionTrailSteps(sc *godog.ScenarioContext, s *sessionTreeSteps) {
	sc.Step(`^a session trail with a 3-level hierarchy of "([^"]*)", "([^"]*)", "([^"]*)"$`, s.aSessionTrailWith3LevelHierarchy)
	sc.Step(`^a session trail with an 8-level hierarchy$`, s.aSessionTrailWith8LevelHierarchy)
	sc.Step(`^the session trail is rendered at width (\d+)$`, s.theSessionTrailIsRenderedAtWidth)
	sc.Step(`^the trail output should be "([^"]*)"$`, s.theTrailOutputShouldBe)
	sc.Step(`^the trail output should contain the first 2 labels$`, s.theTrailOutputShouldContainFirst2Labels)
	sc.Step(`^the trail output should contain an ellipsis separator$`, s.theTrailOutputShouldContainEllipsis)
	sc.Step(`^the trail output should contain the last 3 labels$`, s.theTrailOutputShouldContainLast3Labels)
}

// registerSessionTreeSteps wires up the session tree modal display, rendering,
// and navigation steps.
//
// Expected:
//   - sc is a valid godog ScenarioContext; s is non-nil.
//
// Side effects:
//   - Registers session tree step definitions on the scenario context.
func registerSessionTreeSteps(sc *godog.ScenarioContext, s *sessionTreeSteps) {
	sc.Step(`^a session tree with 3 sessions in a linear chain$`, s.aSessionTreeWith3SessionsInLinearChain)
	sc.Step(`^a session tree with a root "([^"]*)" and children "([^"]*)", "([^"]*)"$`, s.aSessionTreeWithRootAndChildren)
	sc.Step(`^the session tree intent is created with current session "([^"]*)"$`, s.theSessionTreeIntentIsCreatedWithCurrentSession)
	sc.Step(`^the session tree view should contain "([^"]*)"$`, s.theSessionTreeViewShouldContain)
	sc.Step(`^the session tree view should contain a tree connector$`, s.theSessionTreeViewShouldContainTreeConnector)
	sc.Step(`^the operator presses Down twice in the session tree$`, s.theOperatorPressesDownTwice)
	sc.Step(`^the operator presses Down once in the session tree$`, s.theOperatorPressesDownOnce)
	sc.Step(`^the session tree cursor should be on the third item$`, s.theSessionTreeCursorShouldBeOnThirdItem)
}

// registerSessionJumpSteps wires up the Enter-selection and session jump steps.
//
// Expected:
//   - sc is a valid godog ScenarioContext; s is non-nil.
//
// Side effects:
//   - Registers session jump step definitions on the scenario context.
func registerSessionJumpSteps(sc *godog.ScenarioContext, s *sessionTreeSteps) {
	sc.Step(`^the operator presses Enter in the session tree$`, s.theOperatorPressesEnter)
	sc.Step(`^the session tree should emit a SelectedMsg with session "([^"]*)"$`, s.theSessionTreeShouldEmitSelectedMsg)
}

// --- Session trail steps ---

// aSessionTrailWith3LevelHierarchy creates a session trail with three items.
//
// Expected:
//   - root, parent, child are non-empty label strings.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Populates s.trail and s.trailItems.
func (s *sessionTreeSteps) aSessionTrailWith3LevelHierarchy(root, parent, child string) error {
	s.trailItems = []navigation.SessionTrailItem{
		{SessionID: "sess-root", AgentID: "agent-root", Label: root},
		{SessionID: "sess-parent", AgentID: "agent-parent", Label: parent},
		{SessionID: "sess-child", AgentID: "agent-child", Label: child},
	}
	s.trail = navigation.NewSessionTrail().WithItems(s.trailItems)
	return nil
}

// aSessionTrailWith8LevelHierarchy creates a session trail with eight items
// to exercise the truncation policy.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Populates s.trail and s.trailItems with 8 entries.
func (s *sessionTreeSteps) aSessionTrailWith8LevelHierarchy() error {
	s.trailItems = make([]navigation.SessionTrailItem, 8)
	for i := range 8 {
		label := fmt.Sprintf("level-%d", i)
		s.trailItems[i] = navigation.SessionTrailItem{
			SessionID: fmt.Sprintf("sess-%d", i),
			AgentID:   fmt.Sprintf("agent-%d", i),
			Label:     label,
		}
	}
	s.trail = navigation.NewSessionTrail().WithItems(s.trailItems)
	return nil
}

// theSessionTrailIsRenderedAtWidth renders the trail at the specified width.
//
// Expected:
//   - width is a positive integer.
//
// Returns:
//   - nil on success; error if the trail has not been initialised.
//
// Side effects:
//   - Sets s.trailRender to the rendered output.
func (s *sessionTreeSteps) theSessionTrailIsRenderedAtWidth(width int) error {
	if s.trail == nil {
		return errors.New("session trail has not been initialised")
	}
	s.trailRender = s.trail.Render(width)
	return nil
}

// theTrailOutputShouldBe asserts exact equality of the trail render.
//
// Expected:
//   - expected is the exact string the trail should render to.
//
// Returns:
//   - nil if equal; error otherwise.
//
// Side effects:
//   - None.
func (s *sessionTreeSteps) theTrailOutputShouldBe(expected string) error {
	if s.trailRender != expected {
		return fmt.Errorf("expected trail %q, got %q", expected, s.trailRender)
	}
	return nil
}

// theTrailOutputShouldContainFirst2Labels asserts the rendered trail contains
// the first two item labels from the original 8-level hierarchy.
//
// Returns:
//   - nil if both labels are found; error otherwise.
//
// Side effects:
//   - None.
func (s *sessionTreeSteps) theTrailOutputShouldContainFirst2Labels() error {
	for i := range 2 {
		if !strings.Contains(s.trailRender, s.trailItems[i].Label) {
			return fmt.Errorf("expected trail to contain label %q (item %d), got %q",
				s.trailItems[i].Label, i, s.trailRender)
		}
	}
	return nil
}

// theTrailOutputShouldContainEllipsis asserts the rendered trail contains the
// ellipsis character used for truncation.
//
// Returns:
//   - nil if found; error otherwise.
//
// Side effects:
//   - None.
func (s *sessionTreeSteps) theTrailOutputShouldContainEllipsis() error {
	if !strings.Contains(s.trailRender, "…") {
		return fmt.Errorf("expected trail to contain ellipsis, got %q", s.trailRender)
	}
	return nil
}

// theTrailOutputShouldContainLast3Labels asserts the rendered trail contains
// the last three item labels from the original 8-level hierarchy.
//
// Returns:
//   - nil if all three labels are found; error otherwise.
//
// Side effects:
//   - None.
func (s *sessionTreeSteps) theTrailOutputShouldContainLast3Labels() error {
	n := len(s.trailItems)
	for i := n - 3; i < n; i++ {
		if !strings.Contains(s.trailRender, s.trailItems[i].Label) {
			return fmt.Errorf("expected trail to contain label %q (item %d), got %q",
				s.trailItems[i].Label, i, s.trailRender)
		}
	}
	return nil
}

// --- Session tree steps ---

// aSessionTreeWith3SessionsInLinearChain creates a 3-node linear chain:
// root -> child-0 -> child-1.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Populates s.sessions.
func (s *sessionTreeSteps) aSessionTreeWith3SessionsInLinearChain() error {
	s.sessions = []sessiontree.SessionNode{
		{SessionID: "root", AgentID: "orchestrator", ParentID: ""},
		{SessionID: "child-0", AgentID: "engineer", ParentID: "root"},
		{SessionID: "child-1", AgentID: "researcher", ParentID: "child-0"},
	}
	return nil
}

// aSessionTreeWithRootAndChildren creates a tree with one root and two
// direct children (a fan-out topology).
//
// Expected:
//   - root, child1, child2 are non-empty agent ID strings.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Populates s.sessions.
func (s *sessionTreeSteps) aSessionTreeWithRootAndChildren(root, child1, child2 string) error {
	s.sessions = []sessiontree.SessionNode{
		{SessionID: root, AgentID: root, ParentID: ""},
		{SessionID: child1, AgentID: child1, ParentID: root},
		{SessionID: child2, AgentID: child2, ParentID: root},
	}
	return nil
}

// theSessionTreeIntentIsCreatedWithCurrentSession constructs the sessiontree
// Intent from the pre-configured sessions list.
//
// Expected:
//   - currentSession is a session ID present in s.sessions.
//
// Returns:
//   - nil on success; error if sessions have not been set.
//
// Side effects:
//   - Creates s.treeIntent.
func (s *sessionTreeSteps) theSessionTreeIntentIsCreatedWithCurrentSession(currentSession string) error {
	if len(s.sessions) == 0 {
		return errors.New("sessions have not been configured; use a Given step first")
	}
	s.treeIntent = sessiontree.New(currentSession, s.sessions)
	return nil
}

// theSessionTreeViewShouldContain asserts the tree View output contains the
// expected substring.
//
// Expected:
//   - expected is a non-empty substring to search for in the tree view.
//
// Returns:
//   - nil if found; error otherwise.
//
// Side effects:
//   - None.
func (s *sessionTreeSteps) theSessionTreeViewShouldContain(expected string) error {
	if s.treeIntent == nil {
		return errors.New("session tree intent has not been created")
	}
	view := s.treeIntent.View()
	if !strings.Contains(view, expected) {
		return fmt.Errorf("expected session tree view to contain %q, got:\n%s", expected, view)
	}
	return nil
}

// theSessionTreeViewShouldContainTreeConnector asserts the tree View output
// contains at least one box-drawing connector (either "├─" or "└─").
//
// Returns:
//   - nil if a connector is found; error otherwise.
//
// Side effects:
//   - None.
func (s *sessionTreeSteps) theSessionTreeViewShouldContainTreeConnector() error {
	if s.treeIntent == nil {
		return errors.New("session tree intent has not been created")
	}
	view := s.treeIntent.View()
	if !strings.Contains(view, "├─") && !strings.Contains(view, "└─") {
		return fmt.Errorf("expected tree connector (├─ or └─) in view, got:\n%s", view)
	}
	return nil
}

// theOperatorPressesDownTwice sends two Down key messages to the tree intent.
//
// Returns:
//   - nil on success; error if the tree intent has not been created.
//
// Side effects:
//   - Moves the cursor down twice via intent.Update.
func (s *sessionTreeSteps) theOperatorPressesDownTwice() error {
	if s.treeIntent == nil {
		return errors.New("session tree intent has not been created")
	}
	s.treeIntent.Update(tea.KeyMsg{Type: tea.KeyDown})
	s.treeIntent.Update(tea.KeyMsg{Type: tea.KeyDown})
	return nil
}

// theOperatorPressesDownOnce sends one Down key message to the tree intent.
//
// Returns:
//   - nil on success; error if the tree intent has not been created.
//
// Side effects:
//   - Moves the cursor down once via intent.Update.
func (s *sessionTreeSteps) theOperatorPressesDownOnce() error {
	if s.treeIntent == nil {
		return errors.New("session tree intent has not been created")
	}
	s.treeIntent.Update(tea.KeyMsg{Type: tea.KeyDown})
	return nil
}

// theSessionTreeCursorShouldBeOnThirdItem asserts the cursor has moved to
// the third session in the linear chain (index 2).
//
// Returns:
//   - nil if the cursor is on the expected session; error otherwise.
//
// Side effects:
//   - None.
func (s *sessionTreeSteps) theSessionTreeCursorShouldBeOnThirdItem() error {
	if s.treeIntent == nil {
		return errors.New("session tree intent has not been created")
	}
	// In a linear chain root -> child-0 -> child-1, the third item is child-1.
	expected := s.sessions[2].SessionID
	actual := s.treeIntent.CursorID()
	if actual != expected {
		return fmt.Errorf("expected cursor on %q (third item), got %q", expected, actual)
	}
	return nil
}

// --- Session jump steps ---

// theOperatorPressesEnter sends an Enter key message to the tree intent and
// captures the returned tea.Cmd for downstream assertion.
//
// Returns:
//   - nil on success; error if the tree intent has not been created.
//
// Side effects:
//   - Stores the returned tea.Cmd in s.lastCmd.
func (s *sessionTreeSteps) theOperatorPressesEnter() error {
	if s.treeIntent == nil {
		return errors.New("session tree intent has not been created")
	}
	s.lastCmd = s.treeIntent.Update(tea.KeyMsg{Type: tea.KeyEnter})
	return nil
}

// theSessionTreeShouldEmitSelectedMsg asserts that pressing Enter produced a
// SelectedMsg with the expected session ID. The sessiontree Intent records
// the selection in its Result, which we interrogate here rather than
// executing the tea.Cmd (which requires a Bubble Tea runtime).
//
// Expected:
//   - expectedSession is the session ID that should have been selected.
//
// Returns:
//   - nil if the result matches; error otherwise.
//
// Side effects:
//   - None.
func (s *sessionTreeSteps) theSessionTreeShouldEmitSelectedMsg(expectedSession string) error {
	if s.treeIntent == nil {
		return errors.New("session tree intent has not been created")
	}

	result := s.treeIntent.Result()
	if result == nil {
		return errors.New("expected a result after Enter, got nil")
	}

	treeResult, ok := result.Data.(sessiontree.Result)
	if !ok {
		return fmt.Errorf("expected result data to be sessiontree.Result, got %T", result.Data)
	}

	if treeResult.Cancelled {
		return errors.New("expected selection, but result was cancelled")
	}

	if treeResult.SelectedSessionID != expectedSession {
		return fmt.Errorf("expected SelectedSessionID %q, got %q",
			expectedSession, treeResult.SelectedSessionID)
	}

	return nil
}

// reset clears all per-scenario state.
//
// Side effects:
//   - Clears every field on the receiver.
func (s *sessionTreeSteps) reset() {
	s.trail = nil
	s.trailItems = nil
	s.trailRender = ""
	s.treeIntent = nil
	s.sessions = nil
	s.lastCmd = nil
}
