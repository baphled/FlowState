@compaction @compaction-reliability
Feature: Context-loss regression reliability fixes
  As an agent platform operator
  I need the compaction pipeline to survive summariser failures, isolate
  sessions, stub tool results only under genuine budget pressure, and
  persist compaction state across restarts, so context is never silently
  lost mid-session.

  Background:
    Given a reliability-fix engine with auto-compaction enabled

  Scenario: Summariser failure retries once before falling back
    Given a summariser that fails on the first call and succeeds on retry
    And a session transcript above the compaction threshold for session alpha
    When a reliability compaction runs for session alpha
    Then the summariser was invoked twice
    And the reliability fresh summary is applied

  Scenario: Fallback rebuild keeps a token-proportional tail instead of a fixed message count
    Given a summariser that always fails after retry for session beta
    And a long transcript with many small turns for session beta
    And a model context limit of 1000 tokens for session beta
    When the fallback window is rebuilt for session beta
    Then the rebuilt tail keeps at most 25 percent of the context limit in tokens
    And the tail retains more messages than a fixed window of 5

  Scenario: Fallback rebuild does not delete the compaction memo
    Given a memoised compaction summary for reliability session gamma
    And a summariser that always fails after retry for session gamma
    When a compaction runs for session gamma and falls back
    Then the memoised summary for session gamma is still queryable

  Scenario: Compaction summaries are isolated per session
    Given a memoised compaction summary with intent gamma for reliability session gamma
    And a summariser that always succeeds for session delta
    And a session transcript above the compaction threshold for session delta
    When a reliability compaction runs for session delta
    Then rehydration for session delta uses the delta summary
    And rehydration for session gamma uses the gamma summary

  Scenario: No stubbing when the dispatch budget allows
    Given a transcript with many small tool results for session epsilon
    And a model context limit of 100000 tokens for session epsilon
    When budget-gated tool-result compaction is applied
    Then no tool result is stubbed

  Scenario: Compactable tool results retain a capped head instead of a one-line stub
    Given a transcript with a read tool result longer than 2 KiB for session zeta
    When the transcript is stubbed for dispatch
    Then the read tool result retains its first 2 KiB followed by a stub marker

  Scenario: Auto-compaction default threshold is 0.75
    Then the default auto-compaction threshold should be 0.75

  Scenario: Pruning for summarisation never mutates the source messages
    Given a transcript with an old read tool result larger than the prune char limit
    When the Stage-1 prune pass runs over the transcript
    Then the source transcript messages are unmodified
    And the pruned copy truncates the old tool result

  Scenario: Compaction summary state survives a restart via the session meta sidecar
    Given a compaction summary with files to restore for session eta
    When the compaction state is persisted to the session sidecar
    And the engine state is restored from the sidecar
    Then the restored summary matches the persisted summary
    And rehydration for session eta reads the restored files
