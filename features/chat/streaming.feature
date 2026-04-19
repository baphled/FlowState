Feature: Streaming Responses
  As a user
  I want to see responses stream in real-time
  So that I can see progress while the AI generates its response

  Background:
    Given FlowState is running

  @smoke
  Scenario: Streaming tokens appear incrementally
    Given I am in insert mode
    When I type "Tell me a short story"
    And I press Enter
    Then I should see tokens appearing
    And I should see a complete response

  @smoke
  Scenario: Streaming can be interrupted by double-Escape
    Given I am in insert mode
    And the agent streams a long response
    When I send "Write a very long essay"
    And I see tokens appearing
    And I press Escape twice within 500ms
    Then the stream should be cancelled
    And no error should be shown
    And the response should be incomplete

  Scenario: Stream error is displayed to user
    Given I am in insert mode
    When I send a message that will fail with "connection refused"
    Then I should see "[ERROR: connection refused]" in the chat
    And no response should be appended to messages

  Scenario: Partial response preserved when error occurs
    Given I am in insert mode
    When I send a message that receives "Hello " then fails with "provider timeout"
    Then I should see "Hello [ERROR: provider timeout]" in the chat
    And the partial content should be preserved

  Scenario: Critical errors are logged
    Given I am in insert mode
    When I send a message that fails with "API key invalid"
    Then I should see "[ERROR: API key invalid]" in the chat
    And the error should be logged to stderr

  @smoke
  Scenario: Assistant text appears before tool call indicator during streaming
    Given I am in insert mode
    When a streaming response contains assistant text and a tool call
    Then the assistant text should appear before the tool call indicator

