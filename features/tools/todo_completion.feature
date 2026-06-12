Feature: Todo completion continuation
  As a user
  I want the engine to ensure agents complete all planned tasks
  So that agents do not silently abandon pending work when the model stops early

  Background:
    Given FlowState is running
    And the todo tool is enabled

  @smoke
  Scenario: Engine retries when the model stops with pending todos
    Given an agent has added a pending todo "finish step one"
    When the model ends its turn without completing the todo
    Then the engine should inject a continuation prompt
    And the model should be called again
    And the agent should eventually complete "finish step one"

  @smoke
  Scenario: Engine completes normally when no todos are pending
    Given an agent has no pending todos
    When the model ends its turn cleanly
    Then the engine should not inject a continuation prompt
    And the conversation should complete on the first turn

  Scenario: Engine completes normally when all todos are in a terminal state
    Given an agent has todos
      | content    | status    |
      | step one   | completed |
      | step two   | cancelled |
    When the model ends its turn cleanly
    Then the engine should not inject a continuation prompt
    And the conversation should complete on the first turn

  Scenario: Engine stops retrying after the retry budget is exhausted
    Given an agent has a pending todo "always pending"
    And the model consistently ends its turn without completing it
    When the engine exhausts its todo retry budget
    Then the engine should record the exhaustion
    And the conversation should still complete without hanging

  Scenario: Engine increases retry limit after repeated budget exhaustions
    Given an agent has a persistent pending todo
    And the engine has exhausted its retry budget twice in the same session
    When the engine checks the effective retry limit
    Then the limit should be higher than the default

  Scenario: Engine skips todo checking when no todo store is configured
    Given the engine has no todo store configured
    When the model ends its turn cleanly
    Then the engine should complete normally without any retry logic
