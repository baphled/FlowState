@failover @budget
Feature: Pre-dispatch budget gate
  Scenario: Oversized request is trimmed before dispatch without health penalty
    Given the engine resolves a token budget of 8192 tokens for the candidate model
    And the estimated input tokens are 7000
    And the request max tokens reserve is 4096
    When the engine prepares to dispatch the request
    Then the request should be trimmed before dispatch
    And no provider health penalty should be recorded
