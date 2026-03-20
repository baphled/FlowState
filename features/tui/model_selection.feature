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
