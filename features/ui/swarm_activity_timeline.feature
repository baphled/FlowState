Feature: Swarm Activity Timeline
  FlowState's multi-agent chat captures swarm events (delegations, tool calls,
  plans, reviews) into a timeline rendered in the secondary activity pane.
  Operators can view event details via a drill-down modal, filter visible event
  types, and persist/restore timelines as JSONL. This feature covers the
  SwarmEvent model lifecycle, activity pane rendering, event details modal,
  filtering, and JSONL persistence round-trip.

  @ui @events @swarm-activity @wave3
  Scenario: SwarmEvent captures delegation lifecycle
    Given a SwarmEvent of type "delegation" with status "started" and agent "senior-engineer"
    Then the event type should be "delegation"
    And the event status should be "started"
    And the event agent should be "senior-engineer"
    And the event timestamp should be set
    When the event status is updated to "completed"
    Then the event status should be "completed"

  @ui @events @swarm-activity @wave3
  Scenario: SwarmEvent captures tool call lifecycle
    Given a SwarmEvent of type "tool_call" with status "started" and agent "junior-engineer"
    And the event has metadata key "tool_name" with value "ReadFile"
    Then the event type should be "tool_call"
    And the event metadata should contain key "tool_name" with value "ReadFile"

  @ui @events @swarm-activity @wave3 @human-labels
  Scenario: Activity timeline renders human-readable event labels (no wire identifiers)
    Regression: the pane was rendering the raw wire identifiers
    ("tool_call", "tool_result", etc.) produced by the streaming layer.
    The UI must surface human-readable labels — the wire tokens are
    implementation detail, not user-facing copy.
    Given a SwarmActivityPane with 3 events of different types
    When the activity pane is rendered at 80x20
    Then the rendered pane should contain "▸ Delegation"
    And the rendered pane should contain "▸ Tool Call"
    And the rendered pane should contain "▸ Plan"
    And the rendered pane should not contain "tool_call"
    And the rendered pane should not contain "tool_result"

  @ui @events @swarm-activity @wave3 @human-labels
  Scenario: Coalesced tool events never surface raw wire identifiers
    Given a SwarmActivityPane with events of types "tool_call", "tool_result", "delegation"
    When the activity pane is rendered at 80x20
    Then the rendered pane should not contain "tool_call"
    And the rendered pane should not contain "tool_result"

  @ui @events @swarm-activity @wave3 @human-labels @adr-labels
  Scenario: Activity timeline renders the ADR-specified human labels
    ADR — Swarm Activity Event Model (+ Multi-Agent Chat UX Plan T5/T21).
    Row format: icon + HUMAN-READABLE type label + agent + status
    (e.g. a tool-call row reads as ▸ Tool Call · planner · Running).
    Event type to label mapping (spec): delegation -> Delegation,
    tool_call -> Tool Call, plan -> Plan, review -> Review. The literal
    wire strings "tool_call" and "tool_result" MUST NEVER appear in a
    rendered row — they are streaming-layer implementation detail, not
    user-facing copy.
    Given a SwarmActivityPane with events of types "delegation", "tool_call", "plan", "review"
    When the activity pane is rendered at 120x20
    Then the rendered pane should contain "Delegation"
    And the rendered pane should contain "Tool Call"
    And the rendered pane should contain "Plan"
    And the rendered pane should contain "Review"
    And the rendered pane should not contain "tool_call"
    And the rendered pane should not contain "tool_result"

  @ui @events @swarm-activity @wave3 @human-labels @adr-labels
  Scenario: Chat Intent view surfaces ADR-specified labels, never raw wire strings
    End-to-end guard: the bug reproduces in chat.Intent.View, not just
    the standalone SwarmActivityPane. Render through the Intent with a
    known sequence of swarm events — one of each ADR-specified type —
    and assert the ADR labels surface while the wire strings stay internal.
    Given a chat intent seeded with one swarm event of each ADR type
    When the chat intent view is rendered
    Then the chat intent view should contain "Delegation"
    And the chat intent view should contain "Tool Call"
    And the chat intent view should contain "Plan"
    And the chat intent view should contain "Review"
    And the chat intent view should not contain "tool_call"
    And the chat intent view should not contain "tool_result"

  @ui @events @swarm-activity @wave3
  Scenario: Ctrl+E opens event details modal
    Given a chat intent with a swarm event of type "delegation" and status "started"
    When the operator presses Ctrl+E on the chat intent
    Then a ShowModalMsg should be emitted with an eventdetails Intent

  @ui @events @swarm-activity @wave3
  Scenario: Event details modal shows metadata key-value pairs
    Given a SwarmEvent of type "tool_call" with status "completed" and agent "mid-engineer"
    And the event has metadata key "tool_name" with value "EditFile"
    And the event has metadata key "is_error" with value "false"
    When the event details intent is created from the event
    Then the event details view should contain "tool_name"
    And the event details view should contain "EditFile"
    And the event details view should contain "is_error"

  @ui @events @swarm-activity @wave3
  Scenario: Event filter hides specified types
    The filter input still references the wire identifier "tool_call" because
    that's how SwarmEventType is serialised and visibility keyed. User-facing
    assertions, however, must use the human-readable labels rendered by the
    pane.
    Given a SwarmActivityPane with events of types "delegation", "tool_call", "plan"
    And the visibility filter hides "tool_call"
    When the activity pane is rendered at 80x20
    Then the rendered pane should contain "▸ Delegation"
    And the rendered pane should not contain "▸ Tool"
    And the rendered pane should not contain "tool_call"
    And the rendered pane should contain a count summary

  @ui @events @swarm-activity @wave3
  Scenario: JSONL persistence round-trips events
    Given 3 SwarmEvents with distinct types and metadata
    When the events are written to a buffer via WriteEventsJSONL
    And the buffer is read back via ReadEventsJSONL
    Then the read events should match the original events
