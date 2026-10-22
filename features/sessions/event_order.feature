@sessions
Feature: Session event order
  Session events preserve the order in which work was recorded across turns.

  Scenario: Read events from interleaved turns
    Given a fresh session
    When I start turn "first"
    And I start turn "second"
    And I record tool result "first-result" in turn "first"
    And I complete turn "second" with result "second-complete"
    And I cancel turn "first"
    Then session events have sequence numbers 1, 2, 3, 4, 5 in that order
    And session event payloads are "id=first", "id=second", "first-result", "second-complete", "" in that order
