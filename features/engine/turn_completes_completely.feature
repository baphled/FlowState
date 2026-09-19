@engine
Feature: A turn finishes completely

  Invariant: whatever path ends a turn — natural completion, a tool-loop
  cap, a todo-continuation budget exhaustion, or a permission denial —
  the persisted transcript must end on the engine's own terms: a final
  assistant message is stored, tool calls are never left dangling, and
  exactly one terminal chunk closes the stream.

  These scenarios are written tests-first against the current engine.
  Scenarios marked as documenting violations are expected to FAIL; the
  failures are the deliverable and must not be "fixed" by weakening the
  scenario. See docs/invariants/TURN_LIFECYCLE_INVARIANTS.md.

  Scenario: Natural completion ends with a final assistant message
    Given a scripted model that answers the prompt directly
    When the turn is streamed to completion
    Then the persisted transcript ends with an assistant message
    And exactly one terminal chunk closes the stream

  Scenario: A turn stopped by the same-tool-pattern cap ends with a final assistant message
    Given a scripted model that re-requests one tool forever without narration
    And a pending todo is seeded for the turn-completion session
    When the turn is streamed to completion
    Then the persisted transcript ends with an assistant message

  Scenario: A turn whose todo-continuation budget is exhausted ends with a persisted terminal assistant message
    Given a scripted model that narrates while re-requesting one tool
    And a pending todo is seeded for the turn-completion session
    When the turn is streamed to completion
    Then a persisted assistant message carries stop reason "StopReasonToolLoopExceeded"
    And the persisted transcript ends with an assistant message

  Scenario: A capped turn still emits exactly one terminal chunk on the wire
    Given a scripted model that re-requests one tool forever without narration
    When the turn is streamed to completion
    Then exactly one terminal chunk closes the stream

  Scenario: A permission-denied tool call leaves no dangling tool call in the transcript
    Given a scripted model that requests a permission-denied tool once
    When the turn is streamed to completion
    Then every tool call in the persisted transcript has a matching tool result
