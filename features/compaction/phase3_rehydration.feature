@compaction @compaction-phase3
Feature: Tool-result rehydration and structural context on the explicit/session compaction path (Phase 3)
  As an agent platform operator
  I need the explicit/session compaction path to rebuild the post-compaction
  window from re-injected structural context — active plan text, recently
  modified file paths with brief status, coordination keys written this
  session — plus the summary and a verbatim token-bounded tail, with
  compacted tool calls leaving one-line stubs, so important information
  survives compaction on every path, not just the store-bound one.

  Scenario: Compacted tool calls leave one-line stubs naming tool, target and outcome
    Given an engine with a rehydrating explicit-path rebuild
    And a transcript with an old compactable tool call and result
    When the post-compaction window is rebuilt with tool stubbing
    Then the old tool result is replaced by a one-line stub
    And the stub names the tool, its target and its outcome

  Scenario: Non-compactable tool results are retained verbatim under the token cap
    Given an engine with a rehydrating explicit-path rebuild
    And a transcript with a coordination_store tool call and a small result
    When the post-compaction window is rebuilt with tool stubbing
    Then the coordination_store tool result is retained verbatim

  Scenario: Non-compactable tool results above the token cap are stubbed with a key reference
    Given an engine with a rehydrating explicit-path rebuild
    And a transcript with a delegate tool call whose result exceeds the cap
    When the post-compaction window is rebuilt with tool stubbing
    Then the delegate tool result is replaced by a stub with a key reference

  Scenario: Structural context is re-injected into the rebuilt window
    Given an engine with a rehydrating explicit-path rebuild
    And an active plan with plan text
    And recently modified files recorded for the session
    And coordination keys recorded for the session
    When the post-compaction window is rebuilt with structural context
    Then the rebuilt window contains the active plan text
    And the rebuilt window lists the recently modified file paths with brief status
    And the rebuilt window lists the coordination keys written this session

  Scenario: Recently modified files above the token cap degrade to path-only
    Given an engine with a rehydrating explicit-path rebuild
    And recently modified files recorded for the session whose full status exceeds 5000 tokens
    When the post-compaction window is rebuilt with structural context
    Then the rebuilt window lists the recently modified file paths only

  Scenario: The rebuilt window is token-budgeted with oldest stubs trimmed first
    Given an engine with a rehydrating explicit-path rebuild
    And a transcript with several old tool calls and results
    And a compaction summary
    When the post-compaction window is rebuilt with structural context
    Then the rebuilt window fits the usable budget after stub trimming
    And the newest live message is retained
    And the oldest stubs are trimmed first
