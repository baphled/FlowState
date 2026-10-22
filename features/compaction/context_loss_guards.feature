@compaction @compaction-ctx-loss
Feature: Context-loss regression guards
  As an agent platform operator
  I need the compaction stack to degrade without losing recoverable context —
  the naive truncation fallback must keep a token-proportional tail and never
  destroy the compaction memo, compaction summaries must stay per-session,
  tool results must only be stubbed when the budget actually demands it, and
  the auto-compaction threshold defaults must not fire at half-window —
  so a session never silently loses its own context.

  Scenario: The truncation fallback keeps a token-proportional tail
    Given an engine with a summariser that is unavailable for compaction
    And a session transcript of 200 short messages
    When the naive truncation fallback rebuild runs for the session
    Then the rebuilt window retains a token-proportional tail of at least 30 percent of the session transcript
    And the fallback emission is logged loudly

  Scenario: The truncation fallback never deletes the session compaction memo
    Given a context-loss engine with a memoised compaction summary for session alpha
    And the context-loss compaction preconditions degrade with the summariser unresolvable
    When a compaction now runs for session alpha
    Then the context-loss truncation fallback summary is applied
    And the context-loss session memo is invalidated

  Scenario: The summariser is retried once before the truncation fallback
    Given an engine with a flaky summariser that succeeds on the second call
    And a session transcript of 12 short messages
    When a compaction now runs for session alpha
    Then the context-loss fresh summary is applied
    And the summariser was called exactly 2 times

  Scenario: Rehydration consumes only the current session's summary
    Given an engine holding compaction summaries for two sessions
    When rehydration is attempted for session beta
    Then the injected files are only those listed by session beta's summary

  Scenario: Tool results are not stubbed when the budget allows
    Given an engine with a large context budget
    And a context-loss transcript with an old compactable tool call and result
    When the context-loss window is rebuilt with tool stubbing
    Then the old tool result is retained verbatim

  Scenario: Tool results degrade to a capped head when the budget demands
    Given an engine with a small context budget
    And a transcript with an old compactable tool call and a long result
    When the context-loss window is rebuilt with tool stubbing
    Then the old tool result is retained up to a 2 KiB head
    And the retained stub does not reference the coordination store

  Scenario: The default auto-compaction threshold is 0.75
    Given the default compression configuration is loaded
    Then the auto-compaction threshold is 0.75
