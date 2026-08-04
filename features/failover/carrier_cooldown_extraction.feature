@failover
Feature: Carrier cooldown signal extraction on non-429 responses

  When a provider returns an error response (401, 402, 403, 503, 5xx) that
  carries rate-limit headers (retry-after), the failover hook should extract
  the cooldown signal from the carrier response. When the carrier is silent
  but the error code is known (e.g. invalid_api_key), the cooldown is determined
  by the auth error code table. Without either, the per-error-type table applies.

  Scenario: openaicompat 429 with retry-after: 30
    Given a failover hook with a single candidate "openaicompat" / "gpt-4o"
    And the candidate fails with a rate limit error carrying retry-after: 30
    When the failover hook processes the error
    Then the health manager marks "openaicompat" / "gpt-4o" with exactly 30s cooldown

  Scenario: openai 401 with retry-after: 60
    Given a failover hook with a single candidate "openai" / "gpt-4o"
    And the candidate fails with an auth error carrying retry-after: 60
    When the failover hook processes the error
    Then the health manager marks "openai" / "gpt-4o" with exactly 60s cooldown

  Scenario: openai 401 with error.code invalid_api_key and no retry-after
    Given a failover hook with a single candidate "openai" / "gpt-4o"
    And the candidate fails with auth error code "invalid_api_key" and no retry-after
    When the failover hook processes the error
    Then the health manager marks "openai" / "gpt-4o" with 1h cooldown

  Scenario: anthropic 400 credit balance error with no retry-after
    Given a failover hook with a single candidate "anthropic" / "claude-sonnet-4"
    And the candidate fails with a billing error and no retry-after
    When the failover hook processes the error
    Then the health manager marks "anthropic" / "claude-sonnet-4" with 24h cooldown
