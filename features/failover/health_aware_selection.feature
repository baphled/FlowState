@failover @health-selection
Feature: Health-aware provider selection

  The failover manager should prefer the lower-risk healthy candidate, keep the
  original preference order when risk is equal, re-check health before each
  attempt, and fail clearly when nothing healthy remains.

  Scenario: Lower-risk healthy candidate is selected first
    Given the following healthy candidates are configured:
      | provider  | model               | failure_score |
      | openai    | gpt-4o              | 3             |
      | anthropic | claude-sonnet-4     | 1             |
    When the next round candidates are prepared
    Then the selected provider should be "anthropic" / "claude-sonnet-4"
    And the candidate order should stay as configured

  Scenario: Equal scores preserve preference order
    Given the following healthy candidates are configured:
      | provider | model      | failure_score |
      | openai   | gpt-4o     | 2             |
      | openzen  | gpt-4o     | 2             |
    When the next round candidates are prepared
    Then the candidate order should stay as configured
    And the selection should remain stable between attempts

  Scenario: A candidate that cools down mid-round is skipped before its turn
    Given the following healthy candidates are configured:
      | provider  | model               | failure_score |
      | openai    | gpt-4o              | 0             |
      | anthropic | claude-sonnet-4     | 0             |
    When the next round candidates are prepared
    And openai becomes cooldowned before its attempt
    Then the leading candidate should be skipped before its attempt
    And the selected provider should be "anthropic" / "claude-sonnet-4"

  Scenario: A cooled-down round fails with a clear error and no attempt is made
    Given the following healthy candidates are configured:
      | provider | model  | failure_score |
      | openai   | gpt-4o | 0             |
      | openzen  | glm-4  | 0             |
    And every candidate is already in cooldown
    When the next round candidates are prepared
    Then the selection should fail with "no healthy providers available"
    And no attempt should be made
