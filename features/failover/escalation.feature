@failover
Feature: Repeated-failure escalation

  When the same provider/model pair fails repeatedly with the same error type
  before the cooldown expires, the cooldown should double (capped at 24h)
  and the consecutive failure count should increment. When the cooldown expires
  and a fresh failure occurs, the cooldown resets to the base value.

  Scenario: same pair fails 4 times with network_error
    Given a failover hook with a single candidate "openai" / "gpt-4o"
    And the candidate fails 4 times with network_error in quick succession
    When the failover hook processes the 4th error
    Then the health manager reports 4 consecutive failures for "openai" / "gpt-4o"
    And the cooldown for "openai" / "gpt-4o" is at least 4 minutes

  Scenario: cooldown expires then fresh failure resets
    Given a failover hook with a single candidate "openai" / "gpt-4o"
    And the candidate has an expired cooldown entry
    When the candidate fails with a fresh network_error
    Then the health manager reports 1 consecutive failure for "openai" / "gpt-4o"
    And the cooldown for "openai" / "gpt-4o" is the base 30s

  Scenario: carrier retry-after overrides escalation floor
    Given a failover hook with a single candidate "openai" / "gpt-4o"
    And the candidate fails with a rate limit error carrying retry-after: 60
    And the candidate fails again before cooldown expires
    When the failover hook processes the second error
    Then the cooldown for "openai" / "gpt-4o" is at least 60 seconds
