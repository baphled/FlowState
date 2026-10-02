@compaction @compaction-phase2
Feature: Hot tail budget and drop-oldest overflow retry (Phase 2)
  As an agent platform operator
  I need the post-compaction rebuild to select the hot tail by token
  budget — never message count alone — and to shrink an over-budget
  window by dropping the oldest tail messages one at a time, so a
  compaction turn never dispatches an oversized context to the provider.

  Scenario: The hot tail grows backwards from the minimum floor until the budget is reached
    Given an engine with a token counter and compaction budget of 40 tokens
    And a hot tail budget of 20 tokens and a minimum floor of 3 recent messages
    And 6 recent messages each costing 5 tokens
    When the token-bounded hot tail is selected
    Then the hot tail has 4 messages
    And the hot tail retains the most recent messages

  Scenario: The hot tail never exceeds the budget when the floor alone fits
    Given an engine with a token counter and compaction budget of 40 tokens
    And a hot tail budget of 20 tokens and a minimum floor of 3 recent messages
    And 3 recent messages each costing 5 tokens
    When the token-bounded hot tail is selected
    Then the hot tail has 3 messages
    And the hot tail retains the most recent messages

  Scenario: When even the minimum floor exceeds the budget the tail keeps the newest messages that fit
    Given an engine with a token counter and compaction budget of 40 tokens
    And a hot tail budget of 8 tokens and a minimum floor of 3 recent messages
    And 4 recent messages each costing 5 tokens
    When the token-bounded hot tail is selected
    Then the hot tail has 1 messages
    And the hot tail retains the most recent messages

  Scenario: An over-budget final context drops the oldest hot-tail messages until it fits
    Given an engine with a token counter and compaction budget of 4200 tokens
    And a hot tail budget of 40 tokens and a minimum floor of 3 recent messages
    And 6 recent messages each costing 5 tokens
    And a compaction summary costing 8 tokens
    When the post-compaction window is rebuilt from the token-bounded hot tail
    Then the rebuilt window fits the usable budget
    And the rebuilt window retains the newest live message

  Scenario: A context that cannot fit even with one message fails the compaction turn loudly
    Given an engine with a token counter and compaction budget of 100 tokens
    And a hot tail budget of 40 tokens and a minimum floor of 3 recent messages
    And 4 recent messages each costing 50 tokens
    And a compaction summary costing 80 tokens
    When the post-compaction window is rebuilt from the token-bounded hot tail
    Then the rebuild fails loudly with a compaction insufficient error
