@failover @health-classification
Feature: Provider health classification

  The failover health store should classify provider errors into cooldowns,
  persist them across restarts, and ignore request-correctable failures.

  Scenario Outline: Generic auth, subscription, and expiry errors mark a cooldown
    Given the health manager tracks "openai" / "gpt-4o"
    And the pair receives "<error_text>"
    When the failover hook classifies the error
    Then the pair should be in cooldown
    And the cooldown should be at least 1 hour

    Examples:
      | error_text          |
      | 401                 |
      | token expired       |
      | token_expired       |
      | 403                 |
      | subscription expired|
      | account disabled    |
      | limit exhausted     |
      | quota exhausted     |
      | insufficient balance|
      | billing             |

  Scenario: A failed reactive refresh after token_expired leads to a long cooldown
    Given the health manager tracks "anthropic" / "claude-sonnet-4"
    And the pair receives "typed token_expired after a failed refresh"
    When the failover hook classifies the error
    Then the pair should be in cooldown
    And the cooldown should be at least 23 hours

  Scenario: A successful reactive refresh does not create a cooldown
    Given the health manager tracks "anthropic" / "claude-sonnet-4"
    And the pair receives "typed token_expired after a successful refresh"
    When the failover hook classifies the error
    Then the pair should not be in cooldown
    And the refresh outcome should be remembered as successful

  Scenario: A persisted cooldown record survives restart
    Given the health manager tracks "zai" / "glm-4.6"
    And the pair receives "403 quota exhausted"
    When the failover hook classifies the error
    And the health manager restarts
    Then the cooldown should survive a restart

  Scenario Outline: Request-correctable errors do not mark health
    Given the health manager tracks "openai" / "gpt-4o"
    And the pair receives "<error_text>"
    When the failover hook classifies the error
    Then the pair should not be in cooldown
    And the error should be reported as request-correctable

    Examples:
      | error_text              |
      | context window exceeded |
      | malformed request       |
