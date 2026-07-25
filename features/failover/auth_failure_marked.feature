@failover
Feature: Auth failure marks provider pair for table-default cooldown

  When a provider returns an authentication failure (HTTP 401 token_expired),
  the failover hook must mark that provider/model pair for the table-default
  cooldown so subsequent retry rounds skip the dead credential pair instead
  of re-attempting every turn for the entire session lifetime.

  This inverts the H8 contract: pre-S1, auth failures were exempt from
  cooldown under the assumption they were "user-correctable" (fix the key).
  Post-S1, the cooldown applies because the reactive refresh path (S2)
  handles the recoverable case, and the operator reset affordance (S5)
  handles the typo case without waiting 24h.

  Scenario: 401 token_expired marks the pair for 24h cooldown
    Given a failover hook with a single candidate "openai" / "gpt-4o"
    And the candidate returns an auth failure error with code "token_expired"
    When the failover hook executes a chat request
    Then the health manager marks "openai" / "gpt-4o" as rate-limited
    And the cooldown for "openai" / "gpt-4o" is at least 23 hours
