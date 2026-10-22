@compaction @compaction-child-overflow
Feature: Delegated child session overflow recovery
  As an agent platform operator
  I need a delegated child session whose own transcript overflows the
  context window to compact and retry once before giving up, and a
  tool_loop_retry storm to trigger compaction before the transcript
  grows unbounded —
  so a delegation chain degrades through compaction instead of
  hard-failing with a misattributed context_window_exceeded error.

  Scenario: Child session overflow triggers one compact-and-retry
    Given a child-recovery engine with a summariser and a small context limit
    And a child transcript that overflows the local context gate
    When the child first stream is refused with context_window_exceeded
    Then the engine compacts the child session exactly once and retries the stream
    And the retry request skips the local context gate

  Scenario: A retry that still overflows surfaces a clear terminal error
    Given a child-recovery engine with a summariser that always fails
    And a child transcript that overflows the local context gate
    When the child first stream is refused with context_window_exceeded
    And the post-compaction retry still overflows
    Then the child stream surfaces a terminal compaction-insufficient error
    And the summariser was invoked once for the child session

  Scenario: A tool_loop_retry storm triggers compaction before overflow
    Given a child-recovery engine with a summariser and a small context limit
    And a child transcript with a long tool-result history
    When the tool-loop retry attempt count reaches the storm compaction threshold
    Then the engine compacts the child session before the next retry dispatch
