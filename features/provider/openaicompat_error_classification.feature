Feature: OpenAI-compatible provider context-window error classification
  As a FlowState engine
  I need context-window overflow errors from OpenAI-compatible providers to be classified correctly
  So that the overflow recovery path fires for OpenAI, GitHub Copilot, and Z.AI providers

  Background:
    Given an OpenAI-compatible provider named "openai"

  @smoke
  Scenario: 400 with context_length_exceeded code is classified as context-window overflow
    When the provider returns HTTP 400 with code "context_length_exceeded"
    Then the error type is "context_window_exceeded"
    And the error is not retriable

  @smoke
  Scenario: 400 with message containing "context window" is classified as context-window overflow
    When the provider returns HTTP 400 with message "This model's context window is too small"
    Then the error type is "context_window_exceeded"
    And the error is not retriable

  Scenario: 400 with message containing "context length" is classified as context-window overflow
    When the provider returns HTTP 400 with message "Maximum context length exceeded"
    Then the error type is "context_window_exceeded"
    And the error is not retriable

  Scenario: 400 with message containing "prompt is too long" is classified as context-window overflow
    When the provider returns HTTP 400 with message "The prompt is too long for this model"
    Then the error type is "context_window_exceeded"
    And the error is not retriable

  Scenario: 400 with unrelated message is classified as unknown error
    When the provider returns HTTP 400 with message "Invalid request: unknown field"
    Then the error type is "unknown"
    And the error is not retriable

  Scenario: 400 with unrelated code is classified as unknown error
    When the provider returns HTTP 400 with code "invalid_request_error"
    Then the error type is "unknown"
    And the error is not retriable
