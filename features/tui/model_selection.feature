@smoke @tui @model-selection
Feature: Model Selection
  As a FlowState user
  I want to select models interactively
  So I can choose which provider and model to use for my chat sessions

  @model-selector-open
  Scenario: Open model picker with Ctrl+P
    Given FlowState TUI is running
    When I press Ctrl+P
    Then I should see "Select Model" header
    And I should see grouped model list with provider names

  @model-group-expand
  Scenario: Expand provider group to see models
    Given FlowState TUI is running
    And I have pressed Ctrl+P
    When I press Enter on "ollama" group
    Then the group should expand to show available models
    And I should see model names listed under the group

  @model-select
  Scenario: Select a model from picker
    Given FlowState TUI is running
    And I have pressed Ctrl+P
    And I have expanded "ollama" group
    When I select "llama3.2"
    Then the status bar should show "ollama" and "llama3.2"
    And I should be returned to the chat view

  @add-provider
  Scenario: Add provider from picker
    Given FlowState TUI is running
    And I have pressed Ctrl+P
    When I press "a"
    Then I should see provider setup screen

  @cancel-selection
  Scenario: Cancel model selection with Escape
    Given FlowState TUI is running
    And I have pressed Ctrl+P
    When I press Escape
    Then I should be returned to the chat view
    And no model should be selected

  @navigation
  Scenario: Navigate between models with arrow keys
    Given FlowState TUI is running
    And I have pressed Ctrl+P
    And I have expanded "openai" group
    When I press Down arrow
    Then the selection should move to the next model
    When I press Up arrow
    Then the selection should move to the previous model

  @collapse-group
  Scenario: Collapse expanded group
    Given FlowState TUI is running
    And I have pressed Ctrl+P
    And I have expanded "ollama" group
    When I press Enter on "ollama" group
    Then the group should collapse
    And models should not be visible

  @group-navigation
  Scenario: Navigate between provider groups with Down arrow
    Given FlowState TUI is running
    And I have pressed Ctrl+P
    When I navigate down the provider list
    Then the second provider group should be highlighted

  @group-navigation
  Scenario: Navigate to second provider and expand to see its models
    Given FlowState TUI is running
    And I have pressed Ctrl+P
    When I navigate down the provider list
    And I press Enter to expand the group
    Then I should see models from the second provider

  @group-navigation
  Scenario: Select a model from non-default provider
    Given FlowState TUI is running
    And I have pressed Ctrl+P
    When I navigate down the provider list
    And I press Enter to expand the group
    And I select "gpt-4o" from the expanded group
    Then the status bar should show "openai" and "gpt-4o"
    And I should be returned to the chat view

  @group-navigation
  Scenario: Cannot navigate past first or last provider group
    Given FlowState TUI is running
    And I have pressed Ctrl+P
    When I navigate up the provider list
    Then the first provider group should remain highlighted
    When I navigate to the last provider group
    And I navigate down the provider list
    Then the last provider group should remain highlighted
