@failover @adaptive-health
Feature: Adaptive provider health — hard-down circuit breaker

  A provider whose credential cannot recover on its own (billing
  exhausted, account deactivated) should be broken permanently instead
  of being retried into a wall every turn. Transient classes keep
  today's cooldown semantics and never trip the breaker. Hard-down is
  permanent until `flowstate health reset` — there is no auto re-probe.

  Scenario: A typed billing failure breaks the provider after one failure
    Given the health manager tracks "zai" / "glm-5"
    And the pair receives a typed billing error
    When the failover hook classifies the error
    Then the pair should be in cooldown
    And the pair should be hard-down

  Scenario: A typed auth failure with a billing-class account code breaks the provider after one failure
    Given the health manager tracks "openai" / "gpt-4o"
    And the pair receives a typed auth failure with an account code
    When the failover hook classifies the error
    Then the pair should be in cooldown
    And the pair should be hard-down

  Scenario: A typed auth failure breaks the provider after three consecutive failures
    Given the health manager tracks "openai" / "gpt-4o"
    And the health manager records a typed auth failure for the pair
    And the health manager records a typed auth failure for the pair
    And the health manager records a typed auth failure for the pair
    Then the pair should be hard-down

  Scenario: Two typed auth failures alone do not break the provider
    Given the health manager tracks "openai" / "gpt-4o"
    And the health manager records a typed auth failure for the pair
    And the health manager records a typed auth failure for the pair
    Then the pair should not be hard-down

  Scenario: A rate-limit failure never breaks the provider
    Given the health manager tracks "zai" / "glm-5"
    And the pair receives "rate limit exceeded"
    When the failover hook classifies the error
    Then the pair should be in cooldown
    And the pair should not be hard-down

  Scenario: A hard-down pair survives a restart past any expiry
    Given the health manager tracks "zai" / "glm-5"
    And the pair receives a typed billing error
    When the failover hook classifies the error
    And the health manager restarts
    Then the pair should be hard-down after a restart

  Scenario: Resetting provider health clears the hard-down state
    Given the health manager tracks "zai" / "glm-5"
    And the pair receives a typed billing error
    When the failover hook classifies the error
    And the health manager resets the pair
    Then the pair should not be hard-down
    And the pair should not be in cooldown
