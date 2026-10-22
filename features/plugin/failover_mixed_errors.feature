@wip
Feature: Failover retry with mixed transient and permanent provider errors

  When multiple providers are configured, some may fail with permanent errors
  (auth failure, billing, quota, model-not-found, context-window exceeded)
  while others fail with transient errors (rate limit, overload, network, server).

  The failover retry mechanism should continue retrying only the transient
  providers after the appropriate backoff, while permanently-failed providers
  are excluded from subsequent retry rounds. This prevents sessions from
  stopping unnecessarily when a subset of providers are temporarily rate-limited.

  Scenario: Retry only transient providers when mixed error types present
    Given the failover manager has 3 healthy candidates:
      | provider  | model     |
      | anthropic | claude-3  |
      | zai       | glm-5     |
      | openai    | gpt-4o    |
    And anthropic fails with a permanent billing error (HTTP 400, code 1113)
    And zai fails with a transient rate limit error (HTTP 429, code 1001)
    And openai fails with a permanent auth error (HTTP 401)
    And the retry backoff config allows 2 rounds
    And the rate limit cooldown for zai is 1 second
    When the failover hook executes the request
    Then the hook attempts all 3 providers in the first round
    And the hook marks anthropic and openai as permanently failed
    And the hook marks zai as transiently failed
    And the hook backs off for at least 1 second (zai's cooldown)
    And the hook retries only zai in the second round (anthropic and openai excluded)
    And the hook does NOT retry anthropic or openai in subsequent rounds

  Scenario: Fail immediately when all providers have permanent errors
    Given the failover manager has 2 healthy candidates:
      | provider  | model    |
      | anthropic | claude-3 |
      | openai    | gpt-4o   |
    And anthropic fails with a permanent auth error (HTTP 401)
    And openai fails with a permanent billing error (HTTP 400, code 1113)
    And the retry backoff config allows 2 rounds
    When the failover hook executes the request
    Then the hook attempts both providers in the first round
    And the hook marks both as permanently failed
    And the hook does NOT retry (no transient providers to retry)
    And the hook returns "all providers failed" with the last error

  Scenario: Retry normally when all providers have transient errors (existing behavior)
    Given the failover manager has 2 healthy candidates:
      | provider  | model    |
      | anthropic | claude-3 |
      | openai    | gpt-4o   |
    And anthropic fails with a transient rate limit error (HTTP 429)
    And openai fails with a transient network error
    And the retry backoff config allows 2 rounds
    And the rate limit cooldown for anthropic is 1 second
    When the failover hook executes the request
    Then the hook attempts both providers in the first round
    And the hook marks both as transiently failed
    And the hook backs off for the soonest cooldown
    And the hook retries both providers in the second round
    And the hook's hasAnyTransient flag is true

  Scenario: Exclude permanently-failed providers from nextRoundCandidates
    Given the failover manager has 3 healthy candidates:
      | provider  | model     |
      | anthropic | claude-3  |
      | zai       | glm-5     |
      | openai    | gpt-4o    |
    And anthropic failed with a permanent auth error in round 1
    And zai failed with a transient rate limit in round 1
    And openai failed with a permanent billing error in round 1
    And the retry backoff config allows a second round
    When nextRoundCandidates is called for round 2
    Then the candidate list contains only zai
    And the candidate list does NOT contain anthropic
    And the candidate list does NOT contain openai
