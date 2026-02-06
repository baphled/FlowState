Feature: Model Management
  As a user
  I want to manage AI models
  So that I can choose the best model for my task

  Background:
    Given FlowState is running

  @smoke
  Scenario: List available models
    Given Ollama is available
    When I run the command "/models"
    Then I should see a list of available models
    And each model should show its name and size

  @smoke
  Scenario: Switch models mid-conversation
    Given I am using model "llama3.2"
    When I run the command "/models llama3.2:70b"
    Then the active model should change to "llama3.2:70b"
    And subsequent messages should use the new model

  Scenario: Show current model info
    When I run the command "/models info"
    Then I should see details about the current model
    And it should include context window size
    And it should include model parameters

  Scenario: Model not available shows error
    When I run the command "/models nonexistent-model"
    Then I should see an error message
    And the current model should remain unchanged

  @wip
  Scenario: Pull a new model
    Given Ollama is available
    When I run the command "/models pull mistral"
    Then I should see download progress
    And when complete, the model should be available

  Scenario: Remember last used model
    Given I select model "llama3.2:70b"
    And I quit FlowState
    When I restart FlowState
    Then the active model should be "llama3.2:70b"
