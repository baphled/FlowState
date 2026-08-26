@wip
Feature: Delegation grace round in tool loop backstop

  Coordinators fan out work to specialist agents via the `delegate` tool.
  Each delegation consumes two tool-loop iterations (one to issue the
  call, one to absorb the result). On dense turns the 50-iteration
  backstop can trip immediately after a delegation returns, denying the
  coordinator its final synthesising round-trip — the user sees the
  delegation complete but receives no summary or findings.

  The engine grants exactly ONE grace round when the tool-loop backstop
  trips on a batch that contained a `delegate` call, so the coordinator
  always gets a chance to produce a user-facing response. The grace
  round is a per-stream, one-shot mechanism that does not interact with
  the todo-continuation path and never resets the wall-clock budget.

  Scenario: Coordinator responds after delegation when backstop is at limit
    Given the engine tool loop backstop is set to 5 iterations
    And the coordinator has a delegate tool available
    When the coordinator calls delegate 4 times consuming all iterations
    And the 5th iteration's tool batch includes a delegate call
    And the backstop trips on iteration 5
    Then the engine grants one grace round
    And the coordinator produces a final text response
    And the stream ends with a content chunk before Done

  Scenario: Grace round does not fire for non-delegation loops
    Given the engine tool loop backstop is set to 5 iterations
    When the coordinator calls read tools 5 times consuming all iterations
    And the backstop trips on iteration 5
    Then the engine does NOT grant a grace round
    And the stream ends immediately with tool_loop_exceeded

  Scenario: Grace round fires at most once per stream
    Given the engine tool loop backstop is set to 5 iterations
    And the coordinator has a delegate tool available
    When the coordinator calls delegate 5 times tripping the backstop
    And the grace round's response still contains a delegate call
    Then the engine grants exactly one grace round
    And the second backstop trip terminates the stream with tool_loop_exceeded

  Scenario: Duration backstop suppresses the grace round
    Given the engine duration backstop is set very low
    And the last tool batch included a delegate call
    When the duration backstop trips
    Then the engine does NOT grant a grace round
    And the stream ends immediately with tool_loop_exceeded
