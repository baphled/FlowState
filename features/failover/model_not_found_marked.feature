@failover
Feature: Model-not-found marks provider pair for table-default cooldown

  When a provider returns a model-not-found error (HTTP 404), the failover
  hook must mark that provider/model pair for the table-default cooldown so
  subsequent retry rounds skip the invalid model without affecting other
  models from the same provider.

  Scenario: Model-not-found marks the pair for 24h cooldown
    Given a failover hook with a single candidate "openai" / "gpt-4o"
    And the candidate returns a model-not-found error
    When the failover hook executes a chat request
    Then the health manager marks "openai" / "gpt-4o" as rate-limited
    And the cooldown for "openai" / "gpt-4o" is at least 23 hours
