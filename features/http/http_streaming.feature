Feature: HTTP Streaming
  As an API consumer
  I want to receive streaming responses via SSE
  So that I can build real-time chat interfaces

  Background:
    Given FlowState is running

  @smoke
  Scenario: Stream response via SSE
    Given the HTTP server is running on port 8080
    When I POST to "/api/chat" with message "Hello"
    Then I should receive an SSE stream
    And the stream should contain chunks with content

  @smoke
  Scenario: SSE stream completes successfully
    Given the HTTP server is running on port 8080
    When I POST to "/api/chat" with message "What is 2+2?"
    Then I should receive an SSE stream
    And the stream should contain chunks with content

  Scenario: SSE handles connection interruption
    Given the HTTP server is running on port 8080
    When I POST to "/api/chat" with message "Tell me a long story"
    Then I should receive an SSE stream
