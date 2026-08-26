@harness-e2e
Feature: Plan writer harness evaluation
  As the planning system
  I want the plan harness to validate plan-writer output
  So that invalid plans are rejected before reaching the reviewer

  @harness-e2e @planning-wip
  Scenario: Plan-writer produces invalid plan then corrects on retry
    Given a planning session is in progress
    And the plan-writer produces an invalid plan on the first attempt
    When the harness evaluates the plan-writer output
    Then the harness retries with validation feedback
    And the plan-writer produces a valid plan on retry
    And the plan passes harness evaluation

  @harness-e2e @planning-wip
  Scenario: Plan passes schema but critic rejects it
    Given a planning session is in progress
    And the plan-writer produces a plan that passes schema validation
    When the harness critic evaluates the plan
    Then the critic rejects the plan with feedback
    And the harness retries with critic feedback
    And the plan-writer produces an improved plan that the critic approves

  @harness-e2e @planning-wip
  Scenario: Plan-writer exhausts all harness retries
    Given a planning session is in progress
    And the plan-writer repeatedly produces invalid plans
    When the harness exhausts all retry attempts
    Then the harness emits a harness_complete event with validation errors
    And the planner escalates to the user
