Feature: No Provider or Model JSON Leakage
  As a user
  I want the chat view to display only the assistant's prose
  So that internal provider routing details never appear in my conversation

  Background:
    Given FlowState is running
    And Ollama is available with model "llama3.2"

  @smoke @chat
  Scenario: Streaming response contains no raw provider JSON
    Given I am in insert mode
    When I type "What is 2 + 2?"
    And I press Enter
    And I wait for the response to complete
    Then the response should not contain a JSON object with a "model" key
    And the response should not contain a JSON object with a "provider" key
    And the response should not contain a JSON object with a "model_config" key

  @smoke @chat
  Scenario: Error responses omit provider internals
    Given the model service is unreachable
    And I am in insert mode
    When I type "Hello"
    And I press Enter
    Then I should see a user-facing error message
    But the error message should not contain "model"
    And the error message should not contain "provider"
    And the error message should not contain "base_url"
