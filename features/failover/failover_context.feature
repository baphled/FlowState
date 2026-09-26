@failover
Feature: Context-aware failover — size errors are user-correctable, not provider-health faults

  Oversized-prompt failures (context-length errors) are caused by the
  request, not the provider: every candidate in the chain would refuse the
  same prompt. They must not put the provider pair into cooldown, and the
  failover hook should trim the conversation before redispatching to the
  next candidate instead of replaying the same oversized window.

  Scenario: Context-window error does not mark the pair for cooldown
    Given a failover hook with a single candidate "openai" / "gpt-4o"
    And the candidate "openai" / "gpt-4o" returns a context window exceeded error
    When the failover hook executes a chat request
    Then the health manager does NOT mark "openai" / "gpt-4o" as rate-limited
    And the pair should not be in cooldown

  Scenario: Size-error text classification does not trigger a cooldown
    Given the health manager tracks "openai" / "gpt-4o"
    And the pair receives "request too large: context_length_exceeded (403 billing hint)"
    When the failover hook classifies the error
    Then the pair should not be in cooldown

  Scenario: Candidate 2 receives fewer messages after candidate 1's size error
    Given a failover hook with a single candidate "openai" / "gpt-4o"
    And the candidate "openai" / "gpt-4o" returns a context window exceeded error
    And the candidate "anthropic" / "claude" succeeds
    And the failover chain is "openai" / "gpt-4o" then "anthropic" / "claude"
    When the failover hook executes a chat request with a long message history
    Then the turn should complete on "anthropic"
    And the succeeding candidate should receive fewer messages than the failed candidate
