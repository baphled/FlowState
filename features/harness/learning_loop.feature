@harness @learning
Feature: Learning Loop
  As a FlowState developer
  I want an async learning loop that captures agent outputs
  So that the system can improve over time without blocking responses

  Background:
    Given the learning loop is configured with a fake store

  @smoke
  Scenario: Failure triggers are captured when learning on failure is enabled
    Given the learning loop has learning on failure enabled
    When a failure trigger is sent for agent "executor"
    And the learning loop is stopped
    Then the store contains 1 learning entry

  Scenario: Failure triggers are ignored when learning on failure is disabled
    Given the learning loop has no failure learning configured
    When a failure trigger is sent for agent "executor"
    And the learning loop is stopped
    Then the store contains 0 learning entries

  Scenario: Novelty triggers are captured when learning on novelty is enabled
    Given the learning loop has learning on novelty enabled
    When a novelty trigger is sent for agent "executor"
    And the learning loop is stopped
    Then the store contains 1 learning entry

  Scenario: Duplicate output is not captured when a novelty detector is configured
    Given the learning loop has learning on novelty enabled
    And a novelty detector that reports all outputs as duplicates
    When a novelty trigger is sent for agent "executor"
    And the learning loop is stopped
    Then the store contains 0 learning entries

  Scenario: Notify never blocks when the buffer is full
    Given the learning loop buffer is full
    When 200 triggers are sent concurrently
    Then all Notify calls complete without blocking

  @smoke
  Scenario: Plan harness regression - plan agents still use plan evaluator
    Given an agent "planner" with harness_enabled true and mode "plan"
    When a stream request is sent for agent "planner"
    Then the plan evaluator handles the request
