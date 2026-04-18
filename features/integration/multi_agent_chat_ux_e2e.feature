Feature: Multi-Agent Chat UX end-to-end
  """
  Comprehensive smoke test exercising the full operator journey through the
  Multi-Agent Chat UX across all three waves. Validates that dual-pane layout,
  Ctrl+T filter cycling (P11), session trail navigation, swarm event
  timeline rendering, and event filtering work together as a cohesive
  experience.
  """

  @e2e @smoke @multi-agent-ux
  Scenario: Full operator journey through dual-pane layout and filter cycle
    # Wave 1: Dual-pane layout renders correctly
    Given a chat intent with a 120x40 terminal
    And the swarm activity pane is visible by default
    When the operator views the chat
    Then the output shows a dual-pane layout

    # P11: Ctrl+T cycles the filter profile — the pane stays visible
    When the e2e operator toggles the activity pane
    Then the activity pane is restored
    When the e2e operator toggles the activity pane
    Then the activity pane is restored

  @e2e @smoke @multi-agent-ux
  Scenario: Session trail displays hierarchy during navigation
    # Wave 2: Session trail shows ancestry
    Given a session hierarchy with root "planner" and child "engineer"
    When the session trail is rendered for the hierarchy
    Then the session trail shows "planner > engineer"

  @e2e @smoke @multi-agent-ux
  Scenario: Swarm activity timeline renders and filters events
    # Wave 3: Event rendering in activity pane
    Given a swarm activity pane with a delegation event for "engineer"
    When the activity pane is rendered for the e2e scenario
    Then the timeline shows "delegation"

    # Wave 3: Event filtering
    When delegation events are hidden via filter
    And the activity pane is rendered for the e2e scenario
    Then the timeline does not show delegation events
    And the count shows filtered results
