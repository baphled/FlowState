@engine
Feature: Loop-based budget guard distinguishes productive work from loops

  Scenario: Steady varied productive tool calls complete under a low iteration budget
    Given an engine with a loop guard iteration budget of 3
    And a provider that makes varied productive tool calls each round
    When the guarded turn runs
    Then the turn completes without StopReason "StopReasonToolLoopExceeded"

  Scenario: Repeated identical tool calls with identical arguments are stopped
    Given an engine with a loop guard iteration budget of 10
    And a provider that repeats one identical tool call with identical arguments
    When the guarded turn runs
    Then the guarded turn terminates with StopReason "StopReasonToolLoopExceeded"

  Scenario: Many distinct tool calls over many rounds do not stop the turn
    Given an engine with a loop guard iteration budget of 20
    And a provider that makes 8 distinct tool calls
    When the guarded turn runs
    Then the turn completes without StopReason "StopReasonToolLoopExceeded"

  Scenario: Consecutive identical tool calls with the same arguments are stopped even under a high budget
    Given an engine with a loop guard iteration budget of 20
    And a provider that repeats one identical tool call with identical arguments
    When the guarded turn runs
    Then the guarded turn terminates with StopReason "StopReasonToolLoopExceeded"

  Scenario: Changed arguments reset the identical-call repetition counter
    Given an engine with a loop guard iteration budget of 20
    And a provider that repeats arguments twice then changes them
    When the guarded turn runs
    Then the turn completes without StopReason "StopReasonToolLoopExceeded"

  Scenario: Soft continuation compacts history before continuing
    Given an engine with a loop guard iteration budget of 2 and compaction enabled
    And a provider that makes varied productive tool calls each round without finishing
    When the guarded turn runs
    Then the turn compacts before each soft continuation
    And the turn completes without StopReason "StopReasonToolLoopExceeded"

  Scenario: Duration budget still forces a summary after soft continuations
    Given an engine with a loop guard duration budget of 50ms and compaction enabled
    And a provider that makes varied productive tool calls each round without finishing
    When the guarded turn runs
    Then the soft continuations do not reset the duration budget
    And the guarded turn terminates with StopReason "StopReasonToolLoopExceeded"

  Scenario: Soft continuation is refused when history already exceeds the compaction threshold
    Given an engine with a loop guard iteration budget of 2 and compaction enabled
    And a provider whose history already exceeds the compaction threshold
    When the guarded turn runs
    Then the guarded turn terminates with StopReason "StopReasonToolLoopExceeded"

  Scenario: Todo discipline guard stays non-fatal on shutdown bookkeeping
    Given an engine with a todo store and a productive provider
    And the provider did real work, then calls todo_clear followed by a final todo_update marking completion
    When the guarded turn runs
    Then the turn completes without StopReason "StopReasonToolLoopExceeded"
    And the todo bookkeeping calls are not rejected as "no work done"
