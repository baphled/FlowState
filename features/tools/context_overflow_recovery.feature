Feature: Context-window overflow recovery
  As an agent using FlowState
  I want the engine to recover gracefully when the provider rejects a request due to context-window overflow
  So that sessions do not crash hard and can continue after context is compacted

  Background:
    Given an agent manifest with tool support

  @smoke
  Scenario: Provider returns context-window-exceeded error — engine exits cleanly
    Given the provider will return a context-window-exceeded error on the first call
    When the engine streams a turn
    Then the output channel closes without panicking
    And the engine does not retry the provider

  @smoke @wip
  Scenario: Context-window overflow does not trigger todo-continuation
    Given the provider will return a context-window-exceeded error on the first call
    And the session has a pending todo item "deploy the release"
    When the engine streams a turn
    Then the output channel closes without panicking
    And the todo-continuation is not attempted
    And the engine does not call the provider more than once

  Scenario: Provider recovers after compaction fires
    Given the provider will return a context-window-exceeded error on the first call
    And a compactor is configured that can reduce the context
    When the engine streams a turn
    Then the engine retries after compacting
    And the final response contains the retry content

  Scenario: Overflow retry is bounded — engine does not loop indefinitely
    Given the provider will return a context-window-exceeded error on every call
    And a compactor is configured that can reduce the context
    When the engine streams a turn
    Then the output channel closes without panicking
    And the engine attempts at most two provider calls

  Scenario: Normal turn with pending todos still triggers todo-continuation
    Given the provider ends the first turn cleanly without completing its work
    And the session has a pending todo item "write the report"
    When the engine streams a turn
    Then the todo-continuation is attempted
    And the final response indicates completion

  @wip
  Scenario: Over-budget continuation is refused locally instead of blind-sent to the provider
    Given the session context is over budget after a todo continuation
    And compaction is unavailable
    When the engine attempts the continuation retry
    Then the provider does not receive the over-budget request
    And a local context-window error is surfaced
