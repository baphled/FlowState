Feature: Per-turn stream deadline for OpenAI-compatible providers
  As a FlowState engine
  I need a total wall-clock cap on a single provider streaming turn
  So that a trickle-forever upstream cannot hold a delegation open for hours

  Background:
    Given an OpenAI-compatible provider named "openai"

  Scenario: A stream that never finishes is closed with an explicit deadline error
    When the provider trickles chunks forever
    And the per-turn stream deadline is set to 50 milliseconds
    Then the stream terminates with a terminal error chunk
    And the terminal error carries the turn deadline sentinel
    And the terminal error is a retriable network error

  Scenario: A stream that completes before the deadline is unaffected
    When the provider streams a complete response with finish reason "end_turn"
    And the per-turn stream deadline is set to 5 seconds
    Then the stream terminates with a done chunk and no error

  Scenario: A disabled deadline never trips
    When the provider trickles chunks forever
    And the per-turn stream deadline is disabled
    And the stream is read for 150 milliseconds
    Then no terminal error chunk has been emitted
