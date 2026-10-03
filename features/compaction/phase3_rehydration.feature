@compaction @compaction-phase3
Feature: Force-fire compaction memo policy and rehydrated rebuild (Phase 3)
  As an agent platform operator
  I need forced compactions to always produce a fresh summary instead of
  reusing a stale memo, and the post-compaction rebuild on the
  explicit/session path to re-inject structural context — active plan,
  recently-modified files, and session coordination keys — alongside the
  summary and a token-bounded verbatim tail, so important state survives
  compaction and the rebuilt window fits the usable budget.

  Scenario: A forced compaction bypasses the memoised summary and re-summarises
    Given a phase3 engine with auto-compaction enabled and a scripted summariser
    And the session has compacted once with a cold range of 4 messages
    And the summariser is re-scripted to return a different summary
    When a forced compaction fires on the same cold range
    Then the summariser is invoked again
    And the returned summary is the fresh summary

  Scenario: A non-forced ratio compaction still reuses the memoised summary
    Given a phase3 engine with auto-compaction enabled and a scripted summariser
    And the session has compacted once with a cold range of 4 messages
    And the summariser is re-scripted to return a different summary
    When a non-forced ratio compaction fires on the same cold range
    Then the summariser is not invoked again
    And the returned summary is the memoised summary

  Scenario: A summariser chain failure invalidates the memoised summary
    Given a phase3 engine with auto-compaction enabled and a scripted summariser
    And the session has compacted once with a cold range of 4 messages
    And the summariser fails on the next call
    When a forced compaction fires on the same cold range
    Then the memo for the session is invalidated
    And the returned summary is the truncation fallback summary

  Scenario: The rehydrated rebuild re-injects structural context before the summary
    Given a phase3 engine with structural context carrying an active plan, 2 modified files, and 2 coordination keys
    And a compaction summary and a verbatim tail of 3 messages
    When the rehydrated window is rebuilt
    Then the rebuilt window contains the active plan block
    And the rebuilt window contains the 2 modified file paths
    And the rebuilt window contains the 2 coordination keys
    And the rebuilt window contains the compaction summary
    And the rebuilt window retains the 3 verbatim tail messages

  Scenario: Compacted tool calls leave one-line stubs in the tail
    Given a phase3 engine with structural context carrying an active plan, 0 modified files, and 0 coordination keys
    And a message slice with 2 compactable tool results and 2 recent user turns
    When the rehydrated window is rebuilt
    Then the rebuilt window contains one-line stubs for the compacted tool calls
    And the non-compactable tool calls are retained verbatim

  Scenario: The rehydrated rebuild trims oldest stubs first to fit the budget
    Given a phase3 engine with a 60-token usable budget and structural context carrying an active plan, 0 modified files, and 0 coordination keys
    And a message slice with 6 stubbed tool results each costing 20 tokens followed by a user turn
    When the rehydrated window is rebuilt
    Then the rehydrated window fits the usable budget
    And the rebuilt window retains the newest user turn

  Scenario: The summary prompt names the five keep-list sections
    When the summary prompt is rendered for 2 messages
    Then the prompt names the keep-list sections "intent", "key_decisions", "errors", "next_steps", and "critical_data"
