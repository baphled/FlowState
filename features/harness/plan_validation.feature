Feature: Planning Harness Validation
  The planning harness enforces plan quality through automated validation
  ensuring that generated plans meet structural and content requirements.

  Scenario: Valid plan passes harness on first attempt
    Given a planner agent is configured with harness enabled
    When the planner generates a valid plan
    Then the harness accepts the plan without retry
    And the validation score is above 0.7

  Scenario: Invalid plan triggers retry with feedback
    Given a planner agent is configured with harness enabled
    When the planner generates an invalid plan missing frontmatter
    Then the harness retries with specific error feedback
    And the attempt count is greater than 1

  Scenario: Interview messages bypass validation
    Given a planner agent is in interview phase
    When the user sends a planning question
    Then the harness does not validate the response
    And the response is returned as-is

  Scenario: Maximum retries returns best-effort plan
    Given a planner agent is configured with harness enabled
    When the planner consistently generates invalid plans
    Then the harness caps retries at 3
    And returns the best-effort plan with warnings

  Scenario: Schema validation catches missing frontmatter
    Given a plan document without YAML frontmatter
    When the schema validator processes the plan
    Then the validation fails with missing frontmatter error
    And the plan score is 0.0
