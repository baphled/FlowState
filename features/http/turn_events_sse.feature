Feature: Per-turn SSE event stream
  As an API consumer
  I want to stream a sessioned turn's events over SSE
  So that I can render live chat output without polling

  Background:
    Given FlowState is running

  Scenario: Stream emits content chunks then [DONE] on terminal
    When I start a turn and connect to its events SSE endpoint
    Then the SSE response has content type "text/event-stream"
    And the stream emits content chunk events
    And the stream terminates with the [DONE] sentinel

  Scenario: Unknown turn returns 404
    When I connect to the events SSE endpoint for unknown turn "00000000-0000-0000-0000-000000000000"
    Then the response status code is 404
