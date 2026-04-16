Feature: Hierarchical Session Navigation
  """
  FlowState's multi-agent chat supports hierarchical session navigation.
  A session trail displays the ancestry of the current session (root to
  leaf), truncating long chains via a first-2 + ellipsis + last-3 policy.
  Operators can open a session tree modal with Ctrl+G to visualise the
  full hierarchy, navigate with arrow keys, and jump to any session by
  pressing Enter. This feature captures the behaviour of the session
  trail component, the session tree modal, and session jumping.
  """

  @navigation @session-tree @wave2
  Scenario: Session trail displays session ancestry
    Given a session trail with a 3-level hierarchy of "root", "parent", "child"
    When the session trail is rendered at width 80
    Then the trail output should be "root > parent > child"

  @navigation @session-tree @wave2
  Scenario: Session trail truncates long chains
    Given a session trail with an 8-level hierarchy
    When the session trail is rendered at width 120
    Then the trail output should contain the first 2 labels
    And the trail output should contain an ellipsis separator
    And the trail output should contain the last 3 labels

  @navigation @session-tree @wave2
  Scenario: Ctrl+G opens session tree modal
    Given a session tree with 3 sessions in a linear chain
    When the session tree intent is created with current session "child-1"
    Then the session tree view should contain "Session Tree"

  @navigation @session-tree @wave2
  Scenario: Session tree shows hierarchy with indentation
    Given a session tree with a root "orchestrator" and children "engineer", "researcher"
    When the session tree intent is created with current session "orchestrator"
    Then the session tree view should contain "orchestrator"
    And the session tree view should contain "engineer"
    And the session tree view should contain "researcher"
    And the session tree view should contain a tree connector

  @navigation @session-tree @wave2
  Scenario: Up/Down navigate session tree cursor
    Given a session tree with 3 sessions in a linear chain
    And the session tree intent is created with current session "root"
    When the operator presses Down twice in the session tree
    Then the session tree cursor should be on the third item

  @navigation @session-tree @wave2
  Scenario: Enter jumps to selected session
    Given a session tree with 3 sessions in a linear chain
    And the session tree intent is created with current session "root"
    And the operator presses Down once in the session tree
    When the operator presses Enter in the session tree
    Then the session tree should emit a SelectedMsg with session "child-0"
