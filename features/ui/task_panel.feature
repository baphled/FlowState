Feature: Task Panel
  As a user
  I want to see a task panel
  So that I can track progress and access commands quickly

  Background:
    Given FlowState is running
    And the task panel is visible

  @smoke
  Scenario: Task panel shows current model
    Then the task panel should display the current model name
    And the task panel should display the provider name

  Scenario: Task panel shows active tasks
    Given I have created a task "Research vacation options"
    And the task is in progress
    Then the task panel should show "Research vacation options"
    And it should be marked as in-progress

  Scenario: Task panel shows recent activity
    Given I have run a bash command "ls -la"
    And I have modified a file
    Then the task panel should show recent activity
    And activities should be in chronological order

  Scenario: Task panel shows available commands
    Then the task panel should show quick access commands
    And I should see "/analyze"
    And I should see "/research"

  Scenario: Toggle task panel visibility
    When I press the task panel toggle key
    Then the task panel should be hidden
    And the chat area should expand
    When I press the task panel toggle key again
    Then the task panel should be visible

  Scenario: Select model from task panel
    When I click on the model selector
    Then I should see available models
    When I select a different model
    Then the task panel should update to show the new model

  @wip
  Scenario: Complete a task from task panel
    Given I have an active task
    When I mark the task as complete
    Then the task should show as completed
    And it should move to the completed section
