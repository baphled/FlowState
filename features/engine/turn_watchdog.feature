@engine
Feature: Turn watchdog cancels stuck tool loops

  Scenario: A turn that stops making progress is cancelled by the watchdog
    Given an engine with a turn watchdog of 100ms
    And a provider that completes one tool round then never opens the next stream
    When the tool batch completes and the loop stalls
    Then the turn ends with StopReason "StopReasonToolLoopExceeded"
    And the engine log contains "engine turn watchdog fired"

  Scenario: Progress events keep a slow-but-healthy turn alive
    Given an engine with a turn watchdog of 1s
    And a tool that sleeps 50ms per call without ever finishing the task
    When the cumulative tool execution time exceeds a 200ms cap
    Then the tool loop trips the cap with reason "total_tool_time_backstop"
    And the engine log does not contain "engine turn watchdog fired"

  Scenario: A single long-running tool does not trip the watchdog
    Given an engine with a turn watchdog of 200ms
    And a tool that sleeps 600ms once and then completes the task
    When the turn runs
    Then the turn completes naturally
    And the engine log does not contain "engine turn watchdog fired"

  Scenario: A flowing stream longer than the watchdog window completes naturally
    Given an engine with a turn watchdog of 200ms
    And a provider that streams a chunk every 40ms for 600ms and then completes
    When the turn runs
    Then the turn completes naturally
    And the engine log does not contain "engine turn watchdog fired"

  Scenario: A provider that never opens the initial stream trips the watchdog
    Given an engine with a turn watchdog of 100ms
    And a provider that accepts the initial request but never responds
    When the turn runs
    Then the turn ends with StopReason "StopReasonToolLoopExceeded"
    And the engine log contains "engine turn watchdog fired"
