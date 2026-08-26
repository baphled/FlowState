Feature: Plan Management
  As a FlowState user
  I want to manage plans
  So that I can organize AI workflows and agent configurations

  Scenario: List plans when none exist
    Given the plan store is empty
    When I run the plan list command
    Then I should see "No plans found"

  Scenario: Create and list a plan
    Given a plan exists with title "My Test Plan"
    When I run the plan list command
    Then I should see "My Test Plan"

  Scenario: Select a plan by ID
    Given a plan exists with id "my-test-plan"
    When I select plan "my-test-plan"
    Then I should see the plan details for "my-test-plan"

  Scenario: Delete a plan
    Given a plan exists with id "my-test-plan"
    When I delete plan "my-test-plan"
    Then plan "my-test-plan" should not exist

  Scenario: Select non-existent plan shows error
    When I select plan "does-not-exist"
    Then I should see an error containing "not found"
