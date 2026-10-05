@compaction @compaction-phase3
Feature: Stale memoised summary bypass on force-fire and memo invalidation on summariser failure (Phase 3)
  As an agent platform operator
  I need force-fired compactions to always regenerate a fresh summary rather
  than reuse a memoised one, and the memo to be invalidated when the
  SummariserChain fails, so a failed compaction turn never poisons later
  turns with a stale or empty memo.

  Scenario: A force-fired compaction bypasses the memoised summary
    Given an engine with a memoised compaction summary for session alpha
    And the cold-range hash is unchanged
    When a force-fired compaction runs for session alpha
    Then the summariser is invoked again
    And the compaction does not reuse the memoised summary

  Scenario: A non-forced ratio compaction still reuses the memoised summary
    Given an engine with a memoised compaction summary for session alpha
    And the cold-range hash is unchanged
    When a non-forced ratio compaction runs for session alpha
    Then the memoised summary is reused without a new summariser call

  Scenario: A summariser failure invalidates the session memo
    Given an engine with a memoised compaction summary for session alpha
    And the summariser chain fails
    When a compaction runs for session alpha
    Then the truncation fallback summary is applied
    And the session memo is invalidated

  Scenario: After memo invalidation a later compaction regenerates rather than reusing
    Given an engine with a memoised compaction summary for session alpha
    And the summariser chain fails
    And the summariser chain recovers
    When a compaction runs for session alpha
    Then the summariser is invoked again
    And the fresh summary is applied
